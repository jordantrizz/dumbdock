package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubIntegration is a minimal integration used to exercise the framework
// without a vendor file.
type stubIntegration struct {
	id   string
	min  time.Duration
	img  string
	name string
}

func (s stubIntegration) ID() string          { return s.id }
func (s stubIntegration) DisplayName() string { return s.name }
func (s stubIntegration) Description() string { return "stub" }
func (s stubIntegration) Detect(c dockerContainer) bool {
	return matchesImageRepository(c.Image, s.img)
}
func (s stubIntegration) Settings(inspect *dockerInspectResponse, cfg *overrideConfig) integrationSettings {
	var labels map[string]string
	if inspect != nil {
		labels = inspect.Config.Labels
	}
	return resolveIntegrationSettings(s.id, cfg, labels)
}
func (s stubIntegration) Collect(rt integrationRuntime, target integrationTarget) integrationState {
	return baseIntegrationState(s, target.Settings, nil)
}
func (s stubIntegration) Issues(integrationState) ([]hostIssue, []string, bool) {
	return nil, nil, true
}
func (s stubIntegration) MinPollInterval() time.Duration { return s.min }

func stub(id string) stubIntegration {
	return stubIntegration{id: id, img: id, name: id}
}

func TestIntegrationImageRepositoryMatching(t *testing.T) {
	cases := []struct {
		image string
		want  string
		match bool
	}{
		{"traefik:v3.1", "traefik", true},
		{"traefik", "traefik", true},
		{"library/traefik:latest", "traefik", true},
		{"docker.io/library/traefik:latest", "traefik", true},
		{"localhost:5000/traefik:latest", "traefik", true},
		{"ghcr.io/containrrr/watchtower:1.7", "containrrr/watchtower", true},
		{"containrrr/watchtower", "containrrr/watchtower", true},
		{"docker.io/containrrr/watchtower@sha256:abc", "containrrr/watchtower", true},
		{"anoniemerd/prunemate:latest", "anoniemerd/prunemate", true},
		{"thomseddon/traefik-forward-auth:latest", "traefik", false},
		{"traefik-certs-dumper:latest", "traefik", false},
		{"mywatchtower:latest", "containrrr/watchtower", false},
		{"watchtower-helper:latest", "containrrr/watchtower", false},
		{"prunemate-copy:latest", "anoniemerd/prunemate", false},
		{"", "traefik", false},
	}
	for _, tc := range cases {
		if got := matchesImageRepository(tc.image, tc.want); got != tc.match {
			t.Errorf("matchesImageRepository(%q, %q) = %v, want %v", tc.image, tc.want, got, tc.match)
		}
	}
}

func TestIntegrationImageRepositoryNormalisation(t *testing.T) {
	cases := map[string]string{
		"traefik:v3":                                    "traefik",
		"docker.io/library/traefik:latest":              "docker.io/library/traefik",
		"ghcr.io/containrrr/watchtower@sha256:deadbeef": "ghcr.io/containrrr/watchtower",
		"localhost:5000/team/app:1.2.3":                 "localhost:5000/team/app",
		"registry.example.com:8443/a/b:c":               "registry.example.com:8443/a/b",
		"UPPER/Case:Tag":                                "upper/case",
	}
	for in, want := range cases {
		if got := imageRepository(in); got != want {
			t.Errorf("imageRepository(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIntegrationSettingsPrecedence(t *testing.T) {
	t.Setenv("DUMBDOCK_INTEGRATION_WATCHTOWER_URL", "http://env:8080")
	t.Setenv("DUMBDOCK_INTEGRATION_WATCHTOWER_ENABLED", "true")
	t.Setenv("DUMBDOCK_INTEGRATION_WATCHTOWER_TOKEN", "env-token")

	labels := map[string]string{
		"dumbdock.integration.watchtower.url":     "http://label:8080",
		"dumbdock.integration.watchtower.enabled": "false",
	}
	ins := &dockerInspectResponse{}
	ins.Config.Labels = labels

	// Label beats env, env fills what the label omits.
	s := resolveIntegrationSettings("watchtower", nil, labels)
	if !s.Forced {
		t.Error("forced must be set when an override supplies a URL")
	}
	if s.URL != "http://label:8080" || s.source("url") != sourceLabel {
		t.Errorf("url = %q from %q, want the label value", s.URL, s.source("url"))
	}
	if s.Enabled {
		t.Error("label enabled=false must win over env enabled=true")
	}
	if s.Token != "env-token" || s.source("token") != sourceEnv {
		t.Errorf("token must fall through to the env layer, got %q from %q", s.Token, s.source("token"))
	}

	// Config beats both.
	enabled := true
	cfg := &overrideConfig{Integrations: map[string]integrationConfig{
		"watchtower": {Enabled: &enabled, URL: "http://config:8080", Container: "wt-2"},
	}}
	s = resolveIntegrationSettings("watchtower", cfg, labels)
	if !s.Enabled || s.source("enabled") != sourceConfig {
		t.Errorf("config enabled must win, got %v from %q", s.Enabled, s.source("enabled"))
	}
	if s.URL != "http://config:8080" || s.source("url") != sourceConfig {
		t.Errorf("config url must win, got %q", s.URL)
	}
	if s.Container != "wt-2" || s.source("container") != sourceConfig {
		t.Errorf("config pin must win, got %q", s.Container)
	}

	// Nothing configured: enabled, not forced, no sources.
	s = resolveIntegrationSettings("prunemate", nil, nil)
	if !s.Enabled || s.Forced || s.URL != "" || s.source("url") != "" {
		t.Errorf("default settings must be enabled and unforced, got %+v", s)
	}
}

func TestIntegrationSettingsInspectLabels(t *testing.T) {
	ins := &dockerInspectResponse{}
	ins.Config.Labels = map[string]string{
		"dumbdock.integration.prunemate.enabled":   "off",
		"dumbdock.integration.prunemate.container": "pm-pinned",
	}
	s := resolveIntegrationSettings("prunemate", nil, ins.Config.Labels)
	if s.Enabled {
		t.Error("enabled=off must disable the integration")
	}
	if s.Container != "pm-pinned" || s.source("container") != sourceLabel {
		t.Errorf("label pin not applied: %q from %q", s.Container, s.source("container"))
	}
}

func TestIntegrationURLValidation(t *testing.T) {
	cases := []struct {
		url  string
		ok   bool
		note string
	}{
		{"", true, "unset is valid"},
		{"http://prunemate:8080", true, "container name"},
		{"https://watchtower.example.com", true, "https"},
		{"http://127.0.0.1:8080", true, "published port"},
		{"http://[::1]:8080", true, "IPv6 loopback"},
		{"javascript:alert(1)", false, "scheme"},
		{"file:///etc/passwd", false, "scheme"},
		{"ftp://host:21", false, "scheme"},
		{"http://user:pass@host:8080", false, "userinfo"},
		{"http://169.254.169.254/latest/meta-data", false, "metadata IP"},
		{"http://[fe80::1]:8080", false, "link-local IPv6"},
		{"http://", false, "missing host"},
	}
	for _, tc := range cases {
		err := validateIntegrationURL(tc.url)
		if tc.ok && err != nil {
			t.Errorf("validateIntegrationURL(%q) = %v, want nil (%s)", tc.url, err, tc.note)
		}
		if !tc.ok && err == nil {
			t.Errorf("validateIntegrationURL(%q) = nil, want an error (%s)", tc.url, tc.note)
		}
	}
}

func TestIntegrationDetectionPinAndDeterminism(t *testing.T) {
	it := stubIntegration{id: "watchtower", img: "containrrr/watchtower", name: "Watchtower"}

	containers := []dockerContainer{
		{ID: "aaaaaaaaaaaaaaaa", Names: []string{"/wt-b"}, Image: "containrrr/watchtower", State: "running"},
		{ID: "bbbbbbbbbbbbbbbb", Names: []string{"/wt-a"}, Image: "containrrr/watchtower", State: "running"},
		{ID: "cccccccccccccccc", Names: []string{"/wt-off"}, Image: "containrrr/watchtower", State: "exited"},
		{ID: "dddddddddddddddd", Names: []string{"/forward"}, Image: "thomseddon/traefik-forward-auth", State: "running"},
	}

	if got := detectIntegrationContainer(it, containers, integrationSettings{Enabled: true}); got == nil || containerName(*got) != "wt-a" {
		t.Fatalf("auto-detection must pick the lowest running name, got %+v", got)
	}

	// A pin wins over image matching and ignores the sort order.
	got := detectIntegrationContainer(it, containers, integrationSettings{Enabled: true, Container: "wt-b"})
	if got == nil || containerName(*got) != "wt-b" {
		t.Fatalf("pin must win, got %+v", got)
	}

	// A pin forces detection even when the image does not match.
	custom := []dockerContainer{{ID: "eeeeeeeeeeeeeeee", Names: []string{"/mine"}, Image: "mywatchtower:dev", State: "running"}}
	got = detectIntegrationContainer(it, custom, integrationSettings{Enabled: true, Container: "mine"})
	if got == nil || got.ID != "eeeeeeeeeeeeeeee" {
		t.Fatalf("pin must force detection for a non-matching image, got %+v", got)
	}
	if detectIntegrationContainer(it, custom, integrationSettings{Enabled: true}) != nil {
		t.Fatal("an unmatched image must not be auto-detected")
	}

	// A stopped container is never selected.
	stopped := []dockerContainer{{ID: "ffffffffffffffff", Names: []string{"/wt-off"}, Image: "containrrr/watchtower", State: "exited"}}
	if detectIntegrationContainer(it, stopped, integrationSettings{Enabled: true}) != nil {
		t.Fatal("a stopped container must not be detected")
	}
}

func TestIntegrationGateBudgetAndIndependence(t *testing.T) {
	store := newIntegrationStore()
	wt := stub("watchtower")
	pm := stub("prunemate")

	gate := store.gate(wt)
	if gate.attempts != 0 || !gate.shouldAttempt() {
		t.Fatal("a fresh gate must be armed with no attempts")
	}
	if attempt, disabled := gate.fail("down"); attempt != 1 || disabled {
		t.Fatalf("first failure = (%d, %v), want (1, false)", attempt, disabled)
	}
	if attempt, disabled := gate.fail("down"); attempt != 2 || disabled {
		t.Fatalf("second failure = (%d, %v), want (2, false)", attempt, disabled)
	}
	if attempt, disabled := gate.fail("down"); attempt != 3 || !disabled {
		t.Fatalf("third failure = (%d, %v), want (3, true)", attempt, disabled)
	}
	if gate.shouldAttempt() {
		t.Fatal("a disabled gate must not attempt")
	}
	if _, disabled := store.gate(pm).fail("down"); disabled {
		t.Fatal("one integration's failures must not disable another")
	}
}

func TestIntegrationGatePassIsPermanent(t *testing.T) {
	store := newIntegrationStore()
	gate := store.gate(stub("traefik"))
	gate.pass()
	if attempt, disabled := gate.fail("down"); attempt != 0 || disabled {
		t.Fatal("a passed gate must ignore later failures")
	}
	if !gate.shouldAttempt() {
		t.Fatal("a passed gate stays armed")
	}
}

func TestIntegrationStoreConcurrentAccess(t *testing.T) {
	store := newIntegrationStore()
	it := stub("traefik")
	store.ensure(it)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			store.apply(integrationState{ID: "traefik", DisplayName: "Traefik", Detected: n%2 == 0, AttemptLimit: integrationAttemptLimit})
		}(i)
		go func() {
			defer wg.Done()
			if st, ok := store.get("traefik"); ok && st.ID != "traefik" {
				t.Errorf("unexpected state id %q", st.ID)
			}
			for _, st := range store.all() {
				if st.ID == "" {
					t.Error("all() published an empty id")
				}
			}
		}()
	}
	wg.Wait()
}

func TestIntegrationStorePollCadence(t *testing.T) {
	store := newIntegrationStore()
	everyPoll := stub("traefik")
	throttled := stubIntegration{id: "watchtower", img: "watchtower", name: "Watchtower", min: integrationMinPollInterval}

	now := time.Now()
	if !store.due(everyPoll, now) {
		t.Fatal("an integration with no minimum must be polled every cycle")
	}
	if !store.due(throttled, now) {
		t.Fatal("the first poll is always due")
	}
	store.markPolled("watchtower", now)
	if store.due(throttled, now.Add(10*time.Second)) {
		t.Fatal("a throttled integration must not be polled inside its interval")
	}
	if !store.due(throttled, now.Add(integrationMinPollInterval)) {
		t.Fatal("a throttled integration must be due once the interval elapsed")
	}
}

func TestIntegrationRegistryIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range integrationRegistry {
		id := it.ID()
		if id == "" {
			t.Fatal("integration with empty id")
		}
		if id != strings.ToLower(id) {
			t.Errorf("integration id %q must be lower-case", id)
		}
		if seen[id] {
			t.Errorf("duplicate integration id %q", id)
		}
		seen[id] = true
		if it.DisplayName() == "" || it.Description() == "" {
			t.Errorf("integration %q must carry a display name and description", id)
		}
	}
	for _, id := range integrationIDs() {
		it, ok := integrationByID(id)
		if !ok || it.ID() != id {
			t.Errorf("integrationByID(%q) failed", id)
		}
	}
	if _, ok := integrationByID("does-not-exist"); ok {
		t.Error("integrationByID must reject an unknown id")
	}
	if len(integrationRegistry) > 0 && len(integrationIDs()) != len(integrationRegistry) {
		t.Error("integrationIDs must return one id per registered integration")
	}
}

func TestIntegrationBaseStateFields(t *testing.T) {
	it := stub("watchtower")
	enabled := true
	s := integrationSettings{Enabled: enabled, Forced: true, Sources: map[string]string{"url": sourceConfig}, URL: "http://wt:8080"}
	c := dockerContainer{ID: "0123456789abcdef", Names: []string{"/watchtower"}, Image: "containrrr/watchtower:1.7", State: "running"}

	st := baseIntegrationState(it, s, &c)
	if !st.Detected || st.ContainerID != "0123456789ab" || st.ContainerName != "watchtower" {
		t.Errorf("container identity not derived: %+v", st)
	}
	if st.Image != "containrrr/watchtower:1.7" || !st.Enabled || !st.Forced {
		t.Errorf("settings not carried: %+v", st)
	}
	if st.AttemptLimit != integrationAttemptLimit {
		t.Errorf("attempt limit = %d, want %d", st.AttemptLimit, integrationAttemptLimit)
	}

	hidden := baseIntegrationState(it, integrationSettings{Enabled: false}, nil)
	if !hidden.Hidden || hidden.Detected {
		t.Errorf("disabled integration must be hidden and undetected: %+v", hidden)
	}
}

// TestIntegrationsHandlers covers the two cache-read routes.
func TestIntegrationsHandlers(t *testing.T) {
	// The registry is populated by the vendor init functions.
	if len(integrationIDs()) < 3 {
		t.Fatalf("registry holds %v, want traefik, watchtower, and prunemate", integrationIDs())
	}
	integrationStoreState.apply(integrationState{
		ID: "watchtower", DisplayName: "Watchtower", Enabled: true, Detected: true,
		ContainerName: "watchtower", AttemptLimit: integrationAttemptLimit,
	})
	integrationStoreState.apply(integrationState{
		ID: "prunemate", DisplayName: "PruneMate", Enabled: false, Hidden: true, AttemptLimit: integrationAttemptLimit,
	})
	defer resetIntegrations()

	rec := httptest.NewRecorder()
	integrationsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/integrations", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	var resp integrationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Summary.Registered != len(integrationIDs()) {
		t.Fatalf("registered = %d, want %d", resp.Summary.Registered, len(integrationIDs()))
	}
	if len(resp.Integrations) != len(integrationIDs()) {
		t.Fatalf("payload holds %d integrations", len(resp.Integrations))
	}
	if resp.Summary.Detected != 1 || resp.Summary.Hidden != 1 {
		t.Fatalf("summary = %+v", resp.Summary)
	}
	if strings.Contains(rec.Body.String(), "vendorData") {
		t.Fatal("the vendor payload must not be serialised")
	}
	// Registry order is preserved.
	for i, id := range integrationIDs() {
		if resp.Integrations[i].ID != id {
			t.Errorf("integration %d = %q, want %q", i, resp.Integrations[i].ID, id)
		}
	}

	// One integration.
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/watchtower", nil)
	req.SetPathValue("id", "watchtower")
	rec = httptest.NewRecorder()
	integrationByIDHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("one-integration status = %d", rec.Code)
	}
	var one integrationState
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || one.ID != "watchtower" || !one.Detected {
		t.Fatalf("one = %+v err=%v", one, err)
	}

	// Unknown id is a 404 with a JSON error.
	req = httptest.NewRequest(http.MethodGet, "/api/integrations/does-not-exist", nil)
	req.SetPathValue("id", "does-not-exist")
	rec = httptest.NewRecorder()
	integrationByIDHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown integration") {
		t.Fatalf("404 body = %q", rec.Body.String())
	}
}

// TestIntegrationLabelPinWinsOverDetection covers a pin expressed as a label on
// any container: it is resolved without inspecting anything and wins over the
// image match.
func TestIntegrationLabelPinWinsOverDetection(t *testing.T) {
	it := stubIntegration{id: "watchtower", img: "containrrr/watchtower", name: "Watchtower"}
	plain := []dockerContainer{
		{ID: "aaaaaaaaaaaaaaaa", Names: []string{"/wt-auto"}, Image: "containrrr/watchtower", State: "running"},
		{ID: "bbbbbbbbbbbbbbbb", Names: []string{"/wt-pinned"}, Image: "containrrr/watchtower", State: "running"},
	}
	// Without the label the lowest name wins.
	if got := detectIntegrationContainer(it, plain, integrationSettings{Enabled: true}); got == nil || containerName(*got) != "wt-auto" {
		t.Fatalf("auto-detection = %+v, want wt-auto", got)
	}

	// A label on any container pins the integration, without an inspect.
	pinned := append(append([]dockerContainer{}, plain...), dockerContainer{
		ID: "cccccccccccccccc", Names: []string{"/helper"}, Image: "busybox", State: "running",
		Labels: map[string]string{"dumbdock.integration.watchtower.container": "wt-pinned"},
	})
	if got := detectIntegrationContainer(it, pinned, integrationSettings{Enabled: true}); got == nil || got.ID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("label pin = %+v, want wt-pinned", got)
	}
}

// TestIntegrationInvalidOverrideIsRefused proves the poll-time enforcement: an
// unsafe URL from a container label never reaches a vendor call.
func TestIntegrationInvalidOverrideIsRefused(t *testing.T) {
	resetIntegrations()
	defer resetIntegrations()

	containers := []dockerContainer{{
		ID: "0123456789abcdef", Names: []string{"/watchtower"}, Image: "containrrr/watchtower:1.7", State: "running",
		Labels: map[string]string{"dumbdock.integration.watchtower.url": "http://169.254.169.254/latest/meta-data"},
	}}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return traefikTestInspectResponse(t, `{"Id":"0123456789abcdef","Config":{"Image":"containrrr/watchtower:1.7","Labels":{"dumbdock.integration.watchtower.url":"http://169.254.169.254/latest/meta-data"},"Env":["WATCHTOWER_HTTP_API_METRICS=true"],"Cmd":[]},"NetworkSettings":{"Networks":{"edge":{"IPAddress":"172.18.0.5"}},"Ports":{}}}`), nil
	})}

	refreshIntegrations(client, containers, nil)

	st, ok := integrationStoreState.get("watchtower")
	if !ok {
		t.Fatal("no watchtower state published")
	}
	if !strings.Contains(st.Error, "invalid override URL") {
		t.Fatalf("error = %q, want a refused override", st.Error)
	}
	if st.URL != "" {
		t.Fatalf("a refused URL must not be published, got %q", st.URL)
	}
	if st.Attempts != 0 {
		t.Fatalf("a refused override must not spend a boot attempt, got %d", st.Attempts)
	}
	if len(st.Warnings) == 0 || !strings.Contains(st.Warnings[0], "URL override was refused") {
		t.Fatalf("warnings = %v", st.Warnings)
	}
}

// TestIntegrationInvalidBooleanIsReported keeps a typo visible instead of
// silently ignoring the override.
func TestIntegrationInvalidBooleanIsReported(t *testing.T) {
	labels := map[string]string{"dumbdock.integration.prunemate.enabled": "maybe"}
	s := resolveIntegrationSettings("prunemate", nil, labels)
	if !s.Enabled {
		t.Fatal("an invalid boolean must not disable the integration")
	}
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "not a boolean") {
		t.Fatalf("warnings = %v", s.Warnings)
	}

	t.Setenv("DUMBDOCK_INTEGRATION_PRUNEMATE_ENABLED", "sometimes")
	s = resolveIntegrationSettings("prunemate", nil, nil)
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "DUMBDOCK_INTEGRATION_PRUNEMATE_ENABLED") {
		t.Fatalf("env warning = %v", s.Warnings)
	}
}

// TestIntegrationLooksLikeImageID pins the shapes Docker may report instead of
// a repository reference.
func TestIntegrationLooksLikeImageID(t *testing.T) {
	id64 := strings.Repeat("a", 64)
	cases := map[string]bool{
		"sha256:" + id64:                true,
		id64:                            true,
		"traefik:latest":                false,
		"traefik":                       false,
		"ghcr.io/containrrr/watchtower": false,
		"":                              false,
		strings.Repeat("z", 64):         false,
	}
	for image, want := range cases {
		if got := looksLikeImageID(image); got != want {
			t.Errorf("looksLikeImageID(%q) = %v, want %v", image, got, want)
		}
	}
}

// TestIntegrationImageReferenceResolution covers the live-verification defect:
// Docker can report an image ID, which would defeat repository matching.
func TestIntegrationImageReferenceResolution(t *testing.T) {
	resetImageReferenceCache()
	defer resetImageReferenceCache()

	id := "sha256:" + strings.Repeat("b", 64)
	var imageCalls int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/images/") {
			atomic.AddInt32(&imageCalls, 1)
			return traefikTestInspectResponse(t, `{"RepoTags":["traefik:latest","registry.example.com/team/traefik:v3"]}`), nil
		}
		return traefikTestInspectResponse(t, `{}`), nil
	})}

	containers := []dockerContainer{
		{ID: "0123456789abcdef", Names: []string{"/traefik"}, Image: id, ImageID: id, State: "running"},
		{ID: "fedcba9876543210", Names: []string{"/app"}, Image: "app:1.0", ImageID: "sha256:other", State: "running"},
	}
	resolved := resolveImageReferences(client, containers)
	if resolved[0].Image != "traefik:latest" {
		t.Fatalf("resolved image = %q, want traefik:latest", resolved[0].Image)
	}
	if resolved[1].Image != "app:1.0" {
		t.Fatalf("a named reference must not be touched, got %q", resolved[1].Image)
	}
	// The original slice is not mutated.
	if containers[0].Image != id {
		t.Fatalf("input slice was mutated: %q", containers[0].Image)
	}

	// Detection now matches, and the second pass is answered from the cache.
	if !traefikIntegrationInstance().Detect(resolved[0]) {
		t.Fatal("a resolved container must be detected as Traefik")
	}
	before := atomic.LoadInt32(&imageCalls)
	resolveImageReferences(client, containers)
	if got := atomic.LoadInt32(&imageCalls); got != before {
		t.Fatalf("cache miss on the second pass: %d extra image lookups", got-before)
	}
}

// TestIntegrationDanglingImageFallsBackToContainer proves a dangling image is
// still resolved: its tags are gone, but the container records the reference it
// was created from.
func TestIntegrationDanglingImageFallsBackToContainer(t *testing.T) {
	resetImageReferenceCache()
	defer resetImageReferenceCache()

	id := "sha256:" + strings.Repeat("c", 64)
	var imageCalls, inspectCalls int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "/images/"):
			atomic.AddInt32(&imageCalls, 1)
			return traefikTestInspectResponse(t, `{"RepoTags":["<none>:<none>"],"RepoDigests":[]}`), nil
		case strings.Contains(r.URL.Path, "/containers/"):
			atomic.AddInt32(&inspectCalls, 1)
			return traefikTestInspectResponse(t, `{"Id":"0123456789abcdef","Config":{"Image":"traefik:latest","Labels":{},"Env":[],"Cmd":[]}}`), nil
		}
		return traefikTestInspectResponse(t, `{}`), nil
	})}

	containers := []dockerContainer{{ID: "0123456789abcdef", Names: []string{"/traefik"}, Image: id, ImageID: id, State: "running"}}
	resolved := resolveImageReferences(client, containers)
	if resolved[0].Image != "traefik:latest" {
		t.Fatalf("dangling image resolution = %q, want traefik:latest", resolved[0].Image)
	}
	if !traefikIntegrationInstance().Detect(resolved[0]) {
		t.Fatal("a dangling-image container must be detected after resolution")
	}

	// The second pass is fully cached: no image and no container inspect.
	beforeImage, beforeInspect := atomic.LoadInt32(&imageCalls), atomic.LoadInt32(&inspectCalls)
	resolveImageReferences(client, containers)
	if atomic.LoadInt32(&imageCalls) != beforeImage || atomic.LoadInt32(&inspectCalls) != beforeInspect {
		t.Fatal("the second pass must be served from the cache")
	}
}

// TestIntegrationUnresolvableImageStaysUnknown proves a container with no
// usable reference is left alone rather than being mislabelled.
func TestIntegrationUnresolvableImageStaysUnknown(t *testing.T) {
	resetImageReferenceCache()
	defer resetImageReferenceCache()

	id := "sha256:" + strings.Repeat("d", 64)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return traefikTestInspectResponse(t, `{"RepoTags":[],"Config":{"Image":"`+id+`"}}`), nil
	})}
	containers := []dockerContainer{{ID: "0123456789abcdef", Names: []string{"/anon"}, Image: id, ImageID: id, State: "running"}}
	resolved := resolveImageReferences(client, containers)
	if resolved[0].Image != id {
		t.Fatalf("an unresolvable image must keep its ID, got %q", resolved[0].Image)
	}
	if traefikIntegrationInstance().Detect(resolved[0]) {
		t.Fatal("an unresolved image must not be detected")
	}
}
