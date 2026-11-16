package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func watchtowerTestInspect(t *testing.T, body string) *dockerInspectResponse {
	t.Helper()
	var ins dockerInspectResponse
	if err := json.Unmarshal([]byte(body), &ins); err != nil {
		t.Fatalf("decode inspect fixture: %v", err)
	}
	return &ins
}

// watchtowerHealthyInspect enables the metrics API, keeps a secret token, and
// gives the container a network address.
const watchtowerHealthyInspect = `{
  "Id": "0123456789abcdef",
  "Name": "/watchtower",
  "State": {"Status": "running"},
  "Config": {
    "Image": "containrrr/watchtower:1.7",
    "Labels": {},
    "Env": [
      "WATCHTOWER_HTTP_API_METRICS=true",
      "WATCHTOWER_HTTP_API_TOKEN=super-secret-token",
      "WATCHTOWER_SCHEDULE=0 0 4 * * *",
      "WATCHTOWER_CLEANUP=true",
      "WATCHTOWER_SCOPE=edge"
    ],
    "Cmd": []
  },
  "NetworkSettings": {"Networks": {"edge_default": {"IPAddress": "172.18.0.5"}}, "Ports": {}}
}`

const watchtowerNoAPIInspect = `{
  "Id": "0123456789abcdef",
  "Name": "/watchtower",
  "State": {"Status": "running"},
  "Config": {"Image": "containrrr/watchtower:1.7", "Labels": {}, "Env": [], "Cmd": []},
  "NetworkSettings": {"Networks": {"edge_default": {"IPAddress": "172.18.0.5"}}, "Ports": {}}
}`

const watchtowerMetricsBody = `# HELP watchtower_containers_scanned Containers scanned in the last scan
# TYPE watchtower_containers_scanned gauge
watchtower_containers_scanned 12
watchtower_containers_updated 2
watchtower_containers_failed 1
watchtower_scans_total 5
watchtower_scans_skipped 0
some_other_metric{label="x"} 99
watchtower_containers_updated{broken="y"} NaN
`

func TestWatchtowerDetection(t *testing.T) {
	it := watchtowerIntegrationInstance()
	for _, image := range []string{"containrrr/watchtower", "containrrr/watchtower:1.7.2", "ghcr.io/containrrr/watchtower:latest", "docker.io/containrrr/watchtower@sha256:abc"} {
		if !it.Detect(dockerContainer{Image: image, State: "running"}) {
			t.Errorf("Detect(%q) = false, want true", image)
		}
	}
	for _, image := range []string{"mywatchtower:latest", "watchtower-helper:1.0", "containrrr/watchtower-forwarder", "traefik:v3"} {
		if it.Detect(dockerContainer{Image: image, State: "running"}) {
			t.Errorf("Detect(%q) = true, want false", image)
		}
	}
	if it.ID() != "watchtower" || it.DisplayName() != "Watchtower" || it.Description() == "" {
		t.Fatalf("identity = %q/%q/%q", it.ID(), it.DisplayName(), it.Description())
	}
	if it.MinPollInterval() != integrationMinPollInterval {
		t.Fatalf("min poll interval = %v, want %v", it.MinPollInterval(), integrationMinPollInterval)
	}
}

func TestWatchtowerConfigFromContainer(t *testing.T) {
	cfg := watchtowerConfigFrom(watchtowerTestInspect(t, watchtowerHealthyInspect))
	if !cfg.APIMetrics || !cfg.Cleanup || cfg.Scope != "edge" || cfg.Schedule != "0 0 4 * * *" {
		t.Fatalf("env parsing failed: %+v", cfg)
	}
	if cfg.Token != "super-secret-token" {
		t.Fatalf("token not read from the container env")
	}
	if cfg.APIUpdate {
		t.Fatal("update API must stay off unless configured")
	}

	cmdBody := `{"Config":{"Image":"containrrr/watchtower","Labels":{},"Env":[],"Cmd":["--http-api-metrics","--http-api-update","--http-api-periodic-polls","--label-enable","--cleanup","--schedule=0 30 3 * * *","--interval=300","--scope=prod"]},"NetworkSettings":{"Networks":{},"Ports":{}}}`
	cfg = watchtowerConfigFrom(watchtowerTestInspect(t, cmdBody))
	if !cfg.APIMetrics || !cfg.APIUpdate || !cfg.PeriodicPoll || !cfg.LabelEnable || !cfg.Cleanup {
		t.Fatalf("cmd flag parsing failed: %+v", cfg)
	}
	if cfg.Schedule != "0 30 3 * * *" || cfg.PollInterval != "300" || cfg.Scope != "prod" {
		t.Fatalf("cmd value parsing failed: %+v", cfg)
	}
}

func TestParseWatchtowerMetrics(t *testing.T) {
	m := parseWatchtowerMetrics(watchtowerMetricsBody)
	if m.ContainersScanned != 12 || m.ContainersUpdated != 2 || m.ContainersFailed != 1 || m.ScansTotal != 5 || m.ScansSkipped != 0 {
		t.Fatalf("parsed metrics = %+v", m)
	}
	if !m.Present["watchtower_containers_scanned"] || !m.Present["watchtower_scans_total"] {
		t.Fatal("presence markers missing")
	}
	if m.Present["some_other_metric"] {
		t.Fatal("unknown metrics must not be tracked")
	}
	if m.Present["watchtower_containers_updated"] && m.ContainersUpdated == 0 {
		t.Fatal("a NaN line must not overwrite the earlier value")
	}
	if got := parseWatchtowerMetrics(""); len(got.Present) != 0 {
		t.Fatalf("empty body produced %+v", got)
	}
	if got := formatMetric(m, "watchtower_containers_failed"); got != "1" {
		t.Fatalf("formatMetric = %q, want 1", got)
	}
	if got := formatMetric(m, "watchtower_scans_skipped"); got != "0" {
		t.Fatalf("formatMetric = %q, want 0", got)
	}
	if got := formatMetric(m, "not_a_metric"); got != "n/a" {
		t.Fatalf("formatMetric = %q, want n/a", got)
	}
}

func watchtowerTestTarget(ins *dockerInspectResponse, settings integrationSettings) integrationTarget {
	it := watchtowerIntegrationInstance()
	c := dockerContainer{ID: "0123456789abcdef", Names: []string{"/watchtower"}, Image: "containrrr/watchtower:1.7", State: "running"}
	return integrationTarget{
		Container: c,
		Inspect:   ins,
		Settings:  settings,
		Base:      baseIntegrationState(it, settings, &c),
	}
}

func TestWatchtowerCollectReadsMetrics(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		io.WriteString(w, watchtowerMetricsBody)
	}))
	defer srv.Close()

	ins := watchtowerTestInspect(t, watchtowerHealthyInspect)
	settings := integrationSettings{
		Enabled: true, Forced: true, URL: srv.URL, Token: "super-secret-token",
		Sources: map[string]string{"url": sourceConfig, "token": sourceConfig},
	}
	gate := &integrationGate{id: "watchtower", displayName: "Watchtower"}
	state := watchtowerIntegrationInstance().Collect(
		integrationRuntime{gate: gate, containers: []dockerContainer{{ID: "0123456789abcdef", Names: []string{"/watchtower"}, State: "running"}}},
		watchtowerTestTarget(ins, settings),
	)

	if gotPath != "/v1/metrics" {
		t.Fatalf("polled %q, want /v1/metrics", gotPath)
	}
	if gotAuth != "Bearer super-secret-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if state.Error != "" || !gate.passed {
		t.Fatalf("healthy poll failed: error=%q passed=%v", state.Error, gate.passed)
	}
	if !state.AuthConfigured || state.URL != srv.URL || state.URLSource != sourceConfig {
		t.Fatalf("endpoint metadata = %+v", state)
	}
	snap, ok := state.VendorData.(*watchtowerSnapshot)
	if !ok || snap.Metrics.ContainersFailed != 1 || !snap.Config.APIMetrics {
		t.Fatalf("vendor data = %#v", state.VendorData)
	}
	if len(state.Sections) == 0 || len(state.Stats) < 5 {
		t.Fatalf("configuration and stats must be rendered: %+v", state)
	}

	// The token must never reach a payload.
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if strings.Contains(string(payload), "super-secret-token") {
		t.Fatalf("token leaked into the state payload: %s", payload)
	}
}

func TestWatchtowerCollectUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ins := watchtowerTestInspect(t, watchtowerHealthyInspect)
	settings := integrationSettings{Enabled: true, Forced: true, URL: srv.URL, Sources: map[string]string{"url": sourceConfig}}
	gate := &integrationGate{id: "watchtower", displayName: "Watchtower"}
	state := watchtowerIntegrationInstance().Collect(integrationRuntime{gate: gate}, watchtowerTestTarget(ins, settings))

	if !strings.Contains(state.Error, "check the API token") {
		t.Fatalf("error = %q, want a token hint", state.Error)
	}
	if gate.attempts != 1 || gate.disabled {
		t.Fatalf("gate = %+v, want one attempt and not disabled", gate)
	}
}

func TestWatchtowerCollectWithoutAPIIsGuidance(t *testing.T) {
	ins := watchtowerTestInspect(t, watchtowerNoAPIInspect)
	settings := integrationSettings{Enabled: true, Sources: map[string]string{}}
	gate := &integrationGate{id: "watchtower", displayName: "Watchtower"}
	state := watchtowerIntegrationInstance().Collect(integrationRuntime{gate: gate}, watchtowerTestTarget(ins, settings))

	if state.Error != "" {
		t.Fatalf("a disabled HTTP API is guidance, not an error: %q", state.Error)
	}
	if gate.attempts != 0 {
		t.Fatalf("no poll must mean no attempt, got %d", gate.attempts)
	}
	if len(state.Warnings) == 0 || !strings.Contains(state.Warnings[0], "not enabled") {
		t.Fatalf("warnings = %v, want API guidance", state.Warnings)
	}
	if state.URL != "" {
		t.Fatalf("no endpoint should be reported, got %q", state.URL)
	}
}

func TestWatchtowerMonitoredContainers(t *testing.T) {
	containers := []dockerContainer{
		{ID: "1", Names: []string{"/watchtower"}, State: "running"},
		{ID: "2", Names: []string{"/app-a"}, State: "running", Labels: map[string]string{"com.centurylinklabs.watchtower.enable": "true", "com.centurylinklabs.watchtower.scope": "edge"}},
		{ID: "3", Names: []string{"/app-b"}, State: "running", Labels: map[string]string{"com.centurylinklabs.watchtower.enable": "true"}},
		{ID: "4", Names: []string{"/app-c"}, State: "exited", Labels: map[string]string{"com.centurylinklabs.watchtower.enable": "true"}},
	}
	count, allRunning, scoped := watchtowerMonitored(containers, watchtowerConfig{Scope: "edge"}, "watchtower")
	if !scoped || count != 1 || allRunning != 2 {
		t.Fatalf("scoped count = %d/%d scoped=%v, want 1/2 true", count, allRunning, scoped)
	}
	count, allRunning, scoped = watchtowerMonitored(containers, watchtowerConfig{}, "watchtower")
	if !scoped || count != 2 || allRunning != 2 {
		t.Fatalf("unscoped count = %d/%d scoped=%v, want 2/2 true", count, allRunning, scoped)
	}
	unlabelled := []dockerContainer{{ID: "1", Names: []string{"/watchtower"}, State: "running"}, {ID: "2", Names: []string{"/app"}, State: "running"}}
	count, allRunning, scoped = watchtowerMonitored(unlabelled, watchtowerConfig{}, "watchtower")
	if scoped || count != 0 || allRunning != 1 {
		t.Fatalf("unlabelled count = %d/%d scoped=%v", count, allRunning, scoped)
	}
}

func TestWatchtowerIssues(t *testing.T) {
	it := watchtowerIntegrationInstance()

	// A healthy state produces no rows.
	healthy := integrationState{ID: "watchtower", Enabled: true, Detected: true, AttemptLimit: integrationAttemptLimit}
	if rows, warnings, complete := it.Issues(healthy); len(rows) != 0 || !complete || len(warnings) != 0 {
		t.Fatalf("healthy issues = %v/%v/%v", rows, warnings, complete)
	}

	// Unreachable: one error row, coverage incomplete.
	broken := healthy
	broken.Error = "GET /v1/metrics: connection refused"
	rows, warnings, complete := it.Issues(broken)
	if len(rows) != 1 || rows[0].Category != categoryIntegrationAPI || rows[0].Severity != severityError {
		t.Fatalf("unreachable rows = %+v", rows)
	}
	if complete || len(warnings) == 0 {
		t.Fatalf("unreachable must report incomplete coverage, got complete=%v warnings=%v", complete, warnings)
	}
	if rows[0].ID != newIssueID(categoryIntegrationAPI, "watchtower", "api") {
		t.Fatalf("unstable issue id %q", rows[0].ID)
	}

	// Failed updates: one status warning row, coverage complete.
	failed := healthy
	failed.VendorData = &watchtowerSnapshot{
		Config:  watchtowerConfig{APIMetrics: true},
		Metrics: watchtowerMetrics{ContainersFailed: 2, Present: map[string]bool{"watchtower_containers_failed": true}},
	}
	rows, _, complete = it.Issues(failed)
	if len(rows) != 1 || rows[0].Category != categoryIntegrationStatus || rows[0].Severity != severityWarning || !complete {
		t.Fatalf("failed-update rows = %+v complete=%v", rows, complete)
	}

	// Disabled gate: exactly one row, complete coverage.
	disabled := healthy
	disabled.Disabled = true
	disabled.DisabledReason = "boom"
	rows, warnings, complete = it.Issues(disabled)
	if len(rows) != 1 || rows[0].Title != "Watchtower API unavailable (disabled until restart)" || !complete {
		t.Fatalf("disabled rows = %+v complete=%v", rows, complete)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "restart dumbdock") {
		t.Fatalf("disabled warnings = %v", warnings)
	}

	// Absent and switched-off integrations are silent.
	if rows, _, complete := it.Issues(integrationState{ID: "watchtower", Enabled: true}); len(rows) != 0 || !complete {
		t.Fatal("an absent integration must not report a finding")
	}
	if rows, _, complete := it.Issues(integrationState{ID: "watchtower", Enabled: false, Hidden: true}); len(rows) != 0 || !complete {
		t.Fatal("a hidden integration must not report a finding")
	}
}

func TestWatchtowerSettingsFallsBackToContainerToken(t *testing.T) {
	it := watchtowerIntegrationInstance()
	ins := watchtowerTestInspect(t, watchtowerHealthyInspect)

	s := it.Settings(ins, nil)
	if s.Token != "super-secret-token" || s.source("token") != sourceAuto {
		t.Fatalf("token = %q from %q, want the container env", s.Token, s.source("token"))
	}
	if s.URL != "" || s.Forced {
		t.Fatalf("a container-only setup must not look forced: %+v", s)
	}

	// The override wins over the container's own token.
	t.Setenv("DUMBDOCK_INTEGRATION_WATCHTOWER_TOKEN", "override-token")
	s = it.Settings(ins, nil)
	if s.Token != "override-token" || s.source("token") != sourceEnv {
		t.Fatalf("override token not applied: %q from %q", s.Token, s.source("token"))
	}
}

// TestWatchtowerUnfilteredStatIsHonest proves the card never labels every
// running container as "monitored" when label filtering is off.
func TestWatchtowerUnfilteredStatIsHonest(t *testing.T) {
	ins := watchtowerTestInspect(t, watchtowerNoAPIInspect)
	settings := integrationSettings{Enabled: true, Sources: map[string]string{}}
	containers := []dockerContainer{
		{ID: "0123456789abcdef", Names: []string{"/watchtower"}, State: "running"},
		{ID: "2", Names: []string{"/app"}, State: "running"},
	}
	state := watchtowerIntegrationInstance().Collect(
		integrationRuntime{gate: &integrationGate{id: "watchtower"}, containers: containers},
		watchtowerTestTarget(ins, settings),
	)
	var labels []string
	for _, s := range state.Stats {
		labels = append(labels, s.Label)
	}
	for _, l := range labels {
		if l == "Monitored containers" {
			t.Fatalf("unfiltered count must not be labelled monitored: %v", labels)
		}
	}
	if !strings.Contains(strings.Join(labels, ","), "Running containers") {
		t.Fatalf("unfiltered count missing: %v", labels)
	}
}
