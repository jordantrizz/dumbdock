package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const prunemateInspectBody = `{
  "Id": "abcdef0123456789",
  "Name": "/prunemate",
  "State": {"Status": "running"},
  "Config": {"Image": "anoniemerd/prunemate:latest", "Labels": {}, "Env": [], "Cmd": []},
  "NetworkSettings": {"Networks": {"edge_default": {"IPAddress": "172.18.0.9"}}, "Ports": {}}
}`

const prunemateAPIBody = `{
  "pruneRuns": 7,
  "containersDeleted": 3,
  "imagesDeleted": 12,
  "networksDeleted": 1,
  "volumesDeleted": 2,
  "buildCacheDeleted": 4,
  "spaceReclaimed": 5368709120,
  "spaceReclaimedHuman": "5.0 GiB",
  "firstRun": "2026-09-01T02:00:00+00:00",
  "lastRun": "2026-11-10T02:00:00+00:00",
  "lastRunText": "4d ago",
  "lastRunTimestamp": 1762740000
}`

const prunemateRawBody = `{
  "prune_runs": 3,
  "containers_deleted": 1,
  "images_deleted": 5,
  "networks_deleted": 0,
  "volumes_deleted": 0,
  "build_cache_deleted": 2,
  "total_space_reclaimed": 1073741824,
  "first_run": "2026-10-01T02:00:00+00:00",
  "last_run": "2026-11-12T02:00:00+00:00"
}`

func prunemateTestTarget(t *testing.T, settings integrationSettings) integrationTarget {
	t.Helper()
	it := prunemateIntegrationInstance()
	ins := watchtowerTestInspect(t, prunemateInspectBody)
	c := dockerContainer{ID: "abcdef0123456789", Names: []string{"/prunemate"}, Image: "anoniemerd/prunemate:latest", State: "running"}
	return integrationTarget{Container: c, Inspect: ins, Settings: settings, Base: baseIntegrationState(it, settings, &c)}
}

func TestPruneMateDetection(t *testing.T) {
	it := prunemateIntegrationInstance()
	for _, image := range []string{"anoniemerd/prunemate", "anoniemerd/prunemate:latest", "ghcr.io/anoniemerd/prunemate:1.3.3", "docker.io/anoniemerd/prunemate@sha256:abc"} {
		if !it.Detect(dockerContainer{Image: image, State: "running"}) {
			t.Errorf("Detect(%q) = false, want true", image)
		}
	}
	for _, image := range []string{"prunemate-copy:latest", "anoniemerd/prunemate-helper", "watchtower", "traefik:v3"} {
		if it.Detect(dockerContainer{Image: image, State: "running"}) {
			t.Errorf("Detect(%q) = true, want false", image)
		}
	}
	if it.ID() != "prunemate" || it.DisplayName() != "PruneMate" || it.Description() == "" {
		t.Fatalf("identity = %q/%q", it.ID(), it.DisplayName())
	}
	if it.MinPollInterval() != integrationMinPollInterval {
		t.Fatalf("min poll interval = %v", it.MinPollInterval())
	}
}

func TestPruneMateFetchAPIPayload(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(w, prunemateAPIBody)
	}))
	defer srv.Close()

	stats, endpoint, err := fetchPruneMateStats(srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if path != "/api/stats" || endpoint != "/api/stats" {
		t.Fatalf("path = %q endpoint = %q", path, endpoint)
	}
	if stats.PruneRuns != 7 || stats.ImagesDeleted != 12 || stats.SpaceReclaimed != 5368709120 || stats.SpaceReclaimedHuman != "5.0 GiB" {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.LastRun != "2026-11-10T02:00:00+00:00" {
		t.Fatalf("last run = %q", stats.LastRun)
	}
}

func TestPruneMateFetchFallsBackToRawStats(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/stats" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, prunemateRawBody)
	}))
	defer srv.Close()

	stats, endpoint, err := fetchPruneMateStats(srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if endpoint != "/stats" || len(paths) != 2 || paths[1] != "/stats" {
		t.Fatalf("fallback paths = %v endpoint = %q", paths, endpoint)
	}
	if stats.PruneRuns != 3 || stats.ImagesDeleted != 5 || stats.SpaceReclaimed != 1073741824 {
		t.Fatalf("raw stats = %+v", stats)
	}
}

func TestPruneMateBasicAuthAndStatusErrors(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, prunemateAPIBody)
	}))
	defer srv.Close()

	settings := integrationSettings{Enabled: true, Forced: true, URL: srv.URL, Username: "admin", Password: "hunter2", Sources: map[string]string{"url": sourceConfig}}
	state := prunemateIntegrationInstance().Collect(integrationRuntime{gate: &integrationGate{id: "prunemate", displayName: "PruneMate"}}, prunemateTestTarget(t, settings))
	if state.Error != "" {
		t.Fatalf("collect error: %s", state.Error)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") || !state.AuthConfigured {
		t.Fatalf("basic auth not sent: %q authConfigured=%v", gotAuth, state.AuthConfigured)
	}

	// The password must never reach a payload.
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(payload), "hunter2") {
		t.Fatalf("password leaked into the state payload: %s", payload)
	}

	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	_, _, err = fetchPruneMateStats(unauthorized.Client(), unauthorized.URL, "")
	if err == nil || !strings.Contains(err.Error(), "check the credentials") {
		t.Fatalf("401 error = %v", err)
	}
}

func TestPruneMateCollectRendersStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, prunemateAPIBody)
	}))
	defer srv.Close()

	settings := integrationSettings{Enabled: true, Forced: true, URL: srv.URL, Sources: map[string]string{"url": sourceConfig}}
	gate := &integrationGate{id: "prunemate", displayName: "PruneMate"}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	state := prunemateIntegrationInstance().Collect(
		integrationRuntime{gate: gate, now: now},
		prunemateTestTarget(t, settings),
	)
	if !gate.passed || state.Error != "" {
		t.Fatalf("healthy collect failed: %q", state.Error)
	}
	snap, ok := state.VendorData.(*prunemateSnapshot)
	if !ok || !snap.HasLastRun || snap.Endpoint != "/api/stats" {
		t.Fatalf("vendor data = %#v", state.VendorData)
	}
	if len(state.Stats) < 6 || len(state.Sections) != 1 {
		t.Fatalf("card content = %+v", state)
	}
	for _, st := range state.Stats {
		if st.Label == "Space reclaimed" && st.Value != "5.0 GiB" {
			t.Fatalf("space stat = %q", st.Value)
		}
		if st.Label == "Last run" && st.Value != "4d ago" {
			t.Fatalf("last-run stat = %q", st.Value)
		}
	}

	// Without PruneMate's own human string, dumbdock renders the bytes.
	if got := humanBytes(5368709120); got != "5.0 GiB" {
		t.Fatalf("humanBytes = %q", got)
	}
}

func TestParsePruneMateLastRun(t *testing.T) {
	if _, ok := parsePruneMateLastRun(""); ok {
		t.Fatal("an empty timestamp must not parse")
	}
	if _, ok := parsePruneMateLastRun("not-a-time"); ok {
		t.Fatal("a malformed timestamp must not parse")
	}
	got, ok := parsePruneMateLastRun("2026-11-10T02:00:00+00:00")
	if !ok || got.UTC().Year() != 2026 || got.UTC().Month() != time.November {
		t.Fatalf("parsed = %v ok=%v", got, ok)
	}
	naive, ok := parsePruneMateLastRun("2026-11-10T02:00:00")
	if !ok || naive.IsZero() {
		t.Fatal("a naive timestamp must parse")
	}
}

func TestPruneMateIssues(t *testing.T) {
	it := prunemateIntegrationInstance()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	base := integrationState{ID: "prunemate", Enabled: true, Detected: true, AttemptLimit: integrationAttemptLimit, LastChecked: now}

	// Never run.
	never := base
	never.VendorData = &prunemateSnapshot{Stats: prunemateStats{PruneRuns: 0}}
	rows, _, complete := it.Issues(never)
	if len(rows) != 1 || rows[0].ID != newIssueID(categoryIntegrationStatus, "prunemate", "never-run") || !complete {
		t.Fatalf("never-run rows = %+v", rows)
	}

	// Stale.
	stale := base
	stale.VendorData = &prunemateSnapshot{Stats: prunemateStats{PruneRuns: 4}, HasLastRun: true, LastRun: now.Add(-40 * 24 * time.Hour)}
	rows, _, complete = it.Issues(stale)
	if len(rows) != 1 || rows[0].ID != newIssueID(categoryIntegrationStatus, "prunemate", "stale") || !complete {
		t.Fatalf("stale rows = %+v", rows)
	}
	if !strings.Contains(rows[0].Detail, "40 day") {
		t.Fatalf("stale detail = %q", rows[0].Detail)
	}

	// Recent.
	fresh := base
	fresh.VendorData = &prunemateSnapshot{Stats: prunemateStats{PruneRuns: 4}, HasLastRun: true, LastRun: now.Add(-2 * 24 * time.Hour)}
	if rows, _, complete := it.Issues(fresh); len(rows) != 0 || !complete {
		t.Fatalf("a recent prune must be healthy: %+v", rows)
	}

	// Unreachable.
	broken := base
	broken.Error = "GET /api/stats: connection refused"
	rows, warnings, complete := it.Issues(broken)
	if len(rows) != 1 || rows[0].Category != categoryIntegrationAPI || complete || len(warnings) == 0 {
		t.Fatalf("unreachable rows = %+v complete=%v warnings=%v", rows, complete, warnings)
	}

	// Disabled gate.
	disabled := base
	disabled.Disabled = true
	disabled.DisabledReason = "boom"
	rows, warnings, complete = it.Issues(disabled)
	if len(rows) != 1 || rows[0].Title != "PruneMate API unavailable (disabled until restart)" || !complete || len(warnings) != 1 {
		t.Fatalf("disabled rows = %+v complete=%v", rows, complete)
	}

	// Absent and hidden integrations stay silent.
	if rows, _, complete := it.Issues(integrationState{ID: "prunemate", Enabled: true}); len(rows) != 0 || !complete {
		t.Fatal("an absent integration must not report a finding")
	}
	if rows, _, complete := it.Issues(integrationState{ID: "prunemate", Enabled: false}); len(rows) != 0 || !complete {
		t.Fatal("a hidden integration must not report a finding")
	}
}
