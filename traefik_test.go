package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAllEndpointFetchesFailed(t *testing.T) {
	if allEndpointFetchesFailed(nil) {
		t.Fatal("nil data must not report all endpoints failed")
	}
	if allEndpointFetchesFailed(&traefikDashboardData{}) {
		t.Fatal("empty endpoint errors must not report all endpoints failed")
	}

	partial := &traefikDashboardData{EndpointErrors: map[string]string{
		"tlsCerts": "not found (404)",
	}}
	if allEndpointFetchesFailed(partial) {
		t.Fatal("partial endpoint errors must not report all endpoints failed")
	}

	all := &traefikDashboardData{EndpointErrors: make(map[string]string)}
	for _, n := range traefikEndpointNames {
		all.EndpointErrors[n] = "connection refused"
	}
	if !allEndpointFetchesFailed(all) {
		t.Fatal("all endpoint errors must report all endpoints failed")
	}
}

func TestTraefikErrorSignatureDeterministic(t *testing.T) {
	a := &traefikDashboardData{EndpointErrors: map[string]string{
		"version":     "connection refused",
		"overview":    "connection refused",
		"entrypoints": "connection refused",
	}}
	b := &traefikDashboardData{EndpointErrors: map[string]string{
		"entrypoints": "connection refused",
		"version":     "connection refused",
		"overview":    "connection refused",
	}}

	sigA, firstA := traefikErrorSignature(a)
	sigB, firstB := traefikErrorSignature(b)

	if sigA != sigB {
		t.Fatalf("signature not stable across map order: %q != %q", sigA, sigB)
	}
	// Sorted endpoint order puts "entrypoints" first.
	if firstA != "connection refused" || firstB != "connection refused" {
		t.Fatalf("unexpected first error: %q / %q", firstA, firstB)
	}
	if !strings.HasPrefix(sigA, "entrypoints=") {
		t.Fatalf("signature should start with the first sorted endpoint, got %q", sigA)
	}
}

func TestTraefikErrorSignatureEmpty(t *testing.T) {
	if sig, first := traefikErrorSignature(nil); sig != "" || first != "" {
		t.Fatalf("nil data should have empty signature, got %q / %q", sig, first)
	}
	if sig, first := traefikErrorSignature(&traefikDashboardData{}); sig != "" || first != "" {
		t.Fatalf("empty errors should have empty signature, got %q / %q", sig, first)
	}
}

func TestParseTraefikMajorVersion(t *testing.T) {
	tests := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"v3.7.13", 3, true},
		{"3.7.13", 3, true},
		{"v2.2", 2, true},
		{"2.11.0", 2, true},
		{"V1.7", 1, true},
		{"v3", 3, true},
		{"  v3.1.0  ", 3, true},
		{"", 0, false},
		{"garbage", 0, false},
		{"v", 0, false},
		{"vX.Y", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseTraefikMajorVersion(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseTraefikMajorVersion(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestFetchTraefikDashboardVersionSupport(t *testing.T) {
	makeServer := func(version string) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Version":"` + version + `"}`))
		})
		return httptest.NewServer(mux)
	}

	v3srv := makeServer("v3.7.13")
	defer v3srv.Close()
	d3 := fetchTraefikDashboard(v3srv.Client(), v3srv.URL, "")
	if !d3.VersionSupported || d3.MajorVersion != 3 {
		t.Fatalf("v3 should be supported, got major=%d supported=%v", d3.MajorVersion, d3.VersionSupported)
	}

	v2srv := makeServer("v2.2.0")
	defer v2srv.Close()
	d2 := fetchTraefikDashboard(v2srv.Client(), v2srv.URL, "")
	if d2.VersionSupported || d2.MajorVersion != 2 {
		t.Fatalf("v2 should not be supported, got major=%d supported=%v", d2.MajorVersion, d2.VersionSupported)
	}
}

func TestFetchTraefikDashboardVersionSupportUnparseable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Version":"dev"}`))
	}))
	defer srv.Close()
	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	if d.VersionSupported || d.MajorVersion != 0 {
		t.Fatalf("unparseable version should be unsupported with major 0, got major=%d supported=%v", d.MajorVersion, d.VersionSupported)
	}
}

func TestFetchTraefikDashboardVersionSupportWhenVersionFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	if d.VersionSupported || d.MajorVersion != 0 {
		t.Fatalf("missing version should be unsupported with major 0, got major=%d supported=%v", d.MajorVersion, d.VersionSupported)
	}
}

func TestTraefikLogSignature(t *testing.T) {
	v2 := &traefikDashboardData{Version: &traefikVersion{Version: "v2.2.0"}, MajorVersion: 2, VersionSupported: false}
	v3 := &traefikDashboardData{Version: &traefikVersion{Version: "v3.7.13"}, MajorVersion: 3, VersionSupported: true}

	sigV2 := traefikLogSignature(v2, false)
	sigV3 := traefikLogSignature(v3, false)
	if sigV2 == sigV3 {
		t.Fatalf("supported and unsupported signatures must differ, both %q", sigV2)
	}
	if sigV3 == "" {
		t.Fatal("supported v3 signature should be non-empty (marker)")
	}
	if !strings.Contains(sigV2, "unsupported") {
		t.Fatalf("v2 signature should mark unsupported, got %q", sigV2)
	}

	// Stable across repeated calls.
	if traefikLogSignature(v2, false) != sigV2 {
		t.Fatal("signature must be stable across calls")
	}

	// When all endpoints failed there is no version to report, so the signature
	// falls back to the error signature only.
	allFailedV2 := &traefikDashboardData{
		Version:          v2.Version,
		VersionSupported: false,
		EndpointErrors:   map[string]string{"version": "connection refused"},
	}
	if sig := traefikLogSignature(allFailedV2, true); strings.Contains(sig, "unsupported") {
		t.Fatalf("all-failed signature must not include the version marker, got %q", sig)
	}
}

func TestTraefikCandidateURLs(t *testing.T) {
	inspect := &dockerInspectResponse{}
	inspect.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
	}{
		"traefik": {IPAddress: "172.18.0.4"},
	}
	inspect.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"80/tcp":   {{HostPort: "80"}},
		"8080/tcp": {{HostPort: "8081"}},
	}

	primary := "http://172.18.0.4:8080"
	got := traefikCandidateURLs(inspect, primary)

	want := []string{
		"http://172.18.0.4:8080",
		"http://127.0.0.1:8081",
		"http://172.18.0.4:8081",
		"http://127.0.0.1:80",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}

	// No duplicates when the primary already equals a candidate.
	if got := traefikCandidateURLs(nil, ""); len(got) != 0 {
		t.Fatalf("nil inspect should yield no candidates, got %v", got)
	}
}

func TestProbeTraefikAPIURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !probeTraefikAPIURL(nil, srv.URL) {
		t.Fatal("reachable server should probe true")
	}
	if probeTraefikAPIURL(nil, "http://127.0.0.1:1") {
		t.Fatal("closed port should probe false")
	}
}

func TestTraefikOverviewUnmarshal(t *testing.T) {
	// Traefik v2 shape: bare integers.
	v2 := []byte(`{"http":{"routers":5,"services":4,"middlewares":2},"tcp":{"routers":0,"services":0,"middlewares":0}}`)
	var ov2 traefikOverview
	if err := json.Unmarshal(v2, &ov2); err != nil {
		t.Fatalf("v2 overview unmarshal: %v", err)
	}
	if ov2.HTTP.Routers.Int() != 5 || ov2.HTTP.Services.Int() != 4 || ov2.HTTP.Middlewares.Int() != 2 {
		t.Fatalf("v2 counts wrong: %d/%d/%d", ov2.HTTP.Routers, ov2.HTTP.Services, ov2.HTTP.Middlewares)
	}

	// Traefik v3 shape: objects with a "total" field.
	v3 := []byte(`{"http":{"routers":{"total":7,"warnings":0,"errors":0},"services":{"total":6},"middlewares":{"total":3}},"tcp":{"routers":{"total":1},"services":{"total":1},"middlewares":{"total":0}}}`)
	var ov3 traefikOverview
	if err := json.Unmarshal(v3, &ov3); err != nil {
		t.Fatalf("v3 overview unmarshal: %v", err)
	}
	if ov3.HTTP.Routers.Int() != 7 || ov3.HTTP.Services.Int() != 6 || ov3.HTTP.Middlewares.Int() != 3 {
		t.Fatalf("v3 counts wrong: %d/%d/%d", ov3.HTTP.Routers, ov3.HTTP.Services, ov3.HTTP.Middlewares)
	}
	if ov3.TCP.Routers.Int() != 1 {
		t.Fatalf("v3 tcp routers wrong: %d", ov3.TCP.Routers)
	}

	// A missing "total" falls back to zero rather than failing.
	var ovEmpty traefikOverview
	if err := json.Unmarshal([]byte(`{"http":{"routers":{}}}`), &ovEmpty); err != nil {
		t.Fatalf("empty object overview unmarshal: %v", err)
	}
	if ovEmpty.HTTP.Routers.Int() != 0 {
		t.Fatalf("expected 0 for missing total, got %d", ovEmpty.HTTP.Routers.Int())
	}
}

func TestFetchTraefikDashboardClassifies404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Version":"v3.7.13"}`))
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"http":{"routers":{"total":2}}}`))
	})
	mux.HandleFunc("/api/tls/certificates", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	found := false
	for _, n := range d.NotConfigured {
		if n == "tlsCerts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tlsCerts 404 should be in NotConfigured, got %v", d.NotConfigured)
	}
	if len(d.EndpointErrors) != 0 {
		t.Fatalf("all-404 endpoints must not be endpoint errors, got %v", d.EndpointErrors)
	}
	if allEndpointFetchesFailed(d) {
		t.Fatal("a reachable API with 404s must not report all endpoints failed")
	}
	if d.Version == nil || d.Version.Version != "v3.7.13" {
		t.Fatalf("version should decode, got %+v", d.Version)
	}
}

// TestTraefikStateFeedsIssues verifies that the mutex-protected snapshot the
// HTTP handler uses is the same input the issue collector sees.
func TestTraefikStateFeedsIssues(t *testing.T) {
	resetTraefikState()
	defer resetTraefikState()

	traefikMu.Lock()
	traefikContainerFound = true
	traefikAPIURL = "http://traefik:8080"
	traefikData = &traefikDashboardData{
		Version:          &traefikVersion{Version: "v3.7.13"},
		VersionSupported: true,
		MajorVersion:     3,
		HTTPRouters:      []traefikRouter{{Name: "off", Status: "disabled"}},
	}
	traefikDataErr = ""
	traefikMu.Unlock()

	snap := getTraefikState()
	issues, _, complete := collectTraefikIssues(snap.Found, snap.Data, snap.Err)
	if !complete {
		t.Fatal("a healthy snapshot was reported as incomplete")
	}
	if len(issues) != 1 || issues[0].Category != categoryTraefikEntity {
		t.Fatalf("issues = %+v, want one traefik-entity finding", issues)
	}
}

// roundTripFunc adapts a function to http.RoundTripper so a test can answer
// Docker API calls without a daemon.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// traefikTestInspectBody is a running Traefik container with no API label, no
// network IP, and no published port: URL resolution always fails for it.
const traefikTestInspectBody = `{"Id":"0123456789abcdef","Name":"/traefik","State":{"Status":"running"},"Config":{"Image":"traefik:v3","Labels":{},"Env":[],"Cmd":[]},"NetworkSettings":{"Networks":{},"Ports":{}}}`

func traefikTestInspectResponse(t *testing.T, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTraefikGateAttemptBudget(t *testing.T) {
	var g traefikGate
	if !g.shouldAttempt() {
		t.Fatal("a fresh gate must allow attempts")
	}

	for want := 1; want <= traefikAttemptLimit-1; want++ {
		attempt, disabled := g.fail("boom")
		if attempt != want || disabled {
			t.Fatalf("attempt %d: got (%d, %v), want (%d, false)", want, attempt, disabled, want)
		}
	}

	attempt, disabled := g.fail("boom")
	if attempt != traefikAttemptLimit || !disabled {
		t.Fatalf("final attempt: got (%d, %v), want (%d, true)", attempt, disabled, traefikAttemptLimit)
	}
	if g.shouldAttempt() {
		t.Fatal("a disabled gate must refuse attempts")
	}
	if g.disabledReason != "boom" {
		t.Fatalf("disabledReason = %q, want boom", g.disabledReason)
	}

	if attempt, disabled := g.fail("later"); attempt != 0 || disabled {
		t.Fatalf("a disabled gate recorded another attempt: (%d, %v)", attempt, disabled)
	}
	if g.attempts != traefikAttemptLimit {
		t.Fatalf("attempts = %d, want %d", g.attempts, traefikAttemptLimit)
	}
}

func TestTraefikGatePassIsPermanent(t *testing.T) {
	var g traefikGate
	g.pass()
	if attempt, disabled := g.fail("boom"); attempt != 0 || disabled || g.attempts != 0 {
		t.Fatalf("a passed gate must not count failures: (%d, %v), attempts %d", attempt, disabled, g.attempts)
	}
	if !g.shouldAttempt() {
		t.Fatal("a passed gate must keep attempting")
	}

	var disabledGate traefikGate
	for i := 0; i < traefikAttemptLimit; i++ {
		disabledGate.fail("boom")
	}
	disabledGate.pass()
	if !disabledGate.disabled {
		t.Fatal("pass must never re-enable a disabled gate")
	}
}

func TestRefreshTraefikDisablesAfterThreeAttempts(t *testing.T) {
	resetTraefikState()
	defer resetTraefikState()
	t.Setenv("TRAEFIK_API_URL", "")

	var requests int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return traefikTestInspectResponse(t, traefikTestInspectBody), nil
	})}

	var logs bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(origWriter)

	containers := []dockerContainer{{ID: "0123456789abcdef", Image: "traefik:v3", State: "running"}}
	for i := 0; i < traefikAttemptLimit; i++ {
		refreshTraefik(client, containers, nil)
	}

	snap := getTraefikState()
	if !snap.Disabled {
		t.Fatalf("gate not disabled after %d attempts: %+v", traefikAttemptLimit, snap)
	}
	if snap.Attempts != traefikAttemptLimit {
		t.Fatalf("attempts = %d, want %d", snap.Attempts, traefikAttemptLimit)
	}
	if !strings.Contains(snap.DisabledReason, "resolve API URL") {
		t.Fatalf("disabledReason = %q, want the resolution failure", snap.DisabledReason)
	}
	if !strings.Contains(logs.String(), "attempt 3/3 failed") {
		t.Fatalf("missing third attempt log line:\n%s", logs.String())
	}
	if got := strings.Count(logs.String(), "unable to access Traefik API after 3 attempts"); got != 1 {
		t.Fatalf("disable line logged %d times, want 1:\n%s", got, logs.String())
	}

	// A disabled gate performs no Docker inspect and no API request, and it
	// does not log again.
	before := atomic.LoadInt32(&requests)
	beforeLogs := logs.String()
	refreshTraefik(client, containers, nil)
	refreshTraefik(client, containers, nil)
	if got := atomic.LoadInt32(&requests); got != before {
		t.Fatalf("disabled gate made %d extra Docker requests", got-before)
	}
	if logs.String() != beforeLogs {
		t.Fatalf("disabled gate logged again:\n%s", logs.String()[len(beforeLogs):])
	}
	if !getTraefikState().Found {
		t.Fatal("container detection must stay current while the gate is disabled")
	}
}

func TestRefreshTraefikNoContainerSpendsNoAttempt(t *testing.T) {
	resetTraefikState()
	defer resetTraefikState()

	var requests int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return traefikTestInspectResponse(t, traefikTestInspectBody), nil
	})}

	for i := 0; i < 5; i++ {
		refreshTraefik(client, nil, nil)
	}
	if got := getTraefikState(); got.Attempts != 0 || got.Disabled {
		t.Fatalf("polls without a container spent attempts: %+v", got)
	}
	if requests != 0 {
		t.Fatalf("polls without a container made %d Docker requests", requests)
	}

	// A Traefik that starts later still receives the full budget.
	t.Setenv("TRAEFIK_API_URL", "")
	containers := []dockerContainer{{ID: "0123456789abcdef", Image: "traefik:v3", State: "running"}}
	refreshTraefik(client, containers, nil)
	if got := getTraefikState(); got.Attempts != 1 || got.Disabled {
		t.Fatalf("first real attempt: %+v, want attempts 1 and not disabled", got)
	}
}

func TestRefreshTraefikSuccessDisarmsGate(t *testing.T) {
	resetTraefikState()
	defer resetTraefikState()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/api/version"):
			io.WriteString(w, `{"Version":"v3.1.0"}`)
		case strings.HasSuffix(r.URL.Path, "/api/overview"):
			io.WriteString(w, `{"http":{"routers":{"total":1}}}`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer srv.Close()
	t.Setenv("TRAEFIK_API_URL", srv.URL)

	var failInspect atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if failInspect.Load() {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("inspect failed")),
			}, nil
		}
		return traefikTestInspectResponse(t, traefikTestInspectBody), nil
	})}

	var logs bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(origWriter)

	containers := []dockerContainer{{ID: "0123456789abcdef", Image: "traefik:v3", State: "running"}}
	refreshTraefik(client, containers, nil)
	if got := getTraefikState(); got.Disabled || got.Data == nil {
		t.Fatalf("a reachable API must pass the gate: %+v", got)
	}

	failInspect.Store(true)
	for i := 0; i < traefikAttemptLimit+1; i++ {
		refreshTraefik(client, containers, nil)
	}
	if got := getTraefikState(); got.Disabled {
		t.Fatalf("a passed gate must never disable: %+v", got)
	}
	if strings.Contains(logs.String(), "attempt 1/3 failed") {
		t.Fatalf("a passed gate must not log boot attempts:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "traefik: inspect") {
		t.Fatalf("post-boot inspect failures must keep their own log line:\n%s", logs.String())
	}
}
