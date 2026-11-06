package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testIssue(category issueCategory, source, key, title string) hostIssue {
	return hostIssue{
		ID:       newIssueID(category, source, key),
		Category: category,
		Severity: severityWarning,
		Title:    title,
		Detail:   "detail for " + title,
		Source:   source,
	}
}

func completeCoverage() issueCoverage {
	return issueCoverage{CollectionComplete: true}
}

func TestNewIssueID(t *testing.T) {
	a := newIssueID(categoryUpdate, "container", "abc123")
	b := newIssueID(categoryUpdate, "container", "abc123")
	if a != b {
		t.Fatalf("newIssueID is not stable: %q != %q", a, b)
	}
	if !isIssueID(a) {
		t.Fatalf("newIssueID returned %q, which is not a valid issue ID", a)
	}
	if a == newIssueID(categoryUpdate, "container", "abc124") {
		t.Error("different object keys produced the same ID")
	}
	if a == newIssueID(categoryHealth, "container", "abc123") {
		t.Error("different categories produced the same ID")
	}
	if a == newIssueID(categoryUpdate, "other", "abc123") {
		t.Error("different sources produced the same ID")
	}
	for _, bad := range []string{"", "abc", strings.ToUpper(a), a + "0"} {
		if isIssueID(bad) {
			t.Errorf("isIssueID(%q) = true, want false", bad)
		}
	}
}

func TestIssueStoreApplySortsAndCounts(t *testing.T) {
	store := newIssueStore(t.TempDir())
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	errIssue := testIssue(categoryHealth, "container", "c1", "Zulu unhealthy")
	errIssue.Severity = severityError
	alpha := testIssue(categoryExposure, "container", "c2", "Alpha exposed")
	beta := testIssue(categoryUpdate, "container", "c2", "Beta update")

	if err := store.apply([]hostIssue{beta, alpha, errIssue}, completeCoverage(), now); err != nil {
		t.Fatalf("apply: %v", err)
	}

	snap := store.snapshotIssues()
	if snap.Count != 3 {
		t.Fatalf("Count = %d, want 3", snap.Count)
	}
	if len(snap.Items) != 3 || len(snap.Acknowledged) != 0 {
		t.Fatalf("unexpected snapshot lists: %d items, %d acknowledged", len(snap.Items), len(snap.Acknowledged))
	}
	if snap.Items[0].Severity != severityError {
		t.Errorf("first item severity = %q, want error", snap.Items[0].Severity)
	}
	if snap.Items[1].Title != "Alpha exposed" || snap.Items[2].Title != "Beta update" {
		t.Errorf("items are not deterministically ordered: %q, %q", snap.Items[1].Title, snap.Items[2].Title)
	}
	if !snap.Items[0].FirstObservedAt.Equal(now) || !snap.Items[0].LastObservedAt.Equal(now) {
		t.Errorf("timestamps not set: %+v", snap.Items[0])
	}
}

func TestIssueStoreFirstObservedSurvivesUpdates(t *testing.T) {
	store := newIssueStore(t.TempDir())
	first := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	second := first.Add(10 * time.Second)
	iss := testIssue(categoryUpdate, "container", "c1", "Update")

	if err := store.apply([]hostIssue{iss}, completeCoverage(), first); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := store.apply([]hostIssue{iss}, completeCoverage(), second); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := store.snapshotIssues().Items[0]
	if !got.FirstObservedAt.Equal(first) {
		t.Errorf("FirstObservedAt = %s, want %s", got.FirstObservedAt, first)
	}
	if !got.LastObservedAt.Equal(second) {
		t.Errorf("LastObservedAt = %s, want %s", got.LastObservedAt, second)
	}
}

func TestIssueStoreDeduplicates(t *testing.T) {
	store := newIssueStore(t.TempDir())
	iss := testIssue(categoryUpdate, "container", "c1", "Update")
	if err := store.apply([]hostIssue{iss, iss}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := store.snapshotIssues().Count; got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
}

func TestIssueStoreAcknowledgeAndUnacknowledge(t *testing.T) {
	dir := t.TempDir()
	store := newIssueStore(dir)
	fixed := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }
	iss := testIssue(categoryUpdate, "container", "c1", "Update")

	if err := store.apply([]hostIssue{iss}, completeCoverage(), fixed); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := store.acknowledge("deadbeef"); err != errIssueNotFound {
		t.Errorf("acknowledge(unknown) = %v, want errIssueNotFound", err)
	}
	if err := store.acknowledge(iss.ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	snap := store.snapshotIssues()
	if snap.Count != 0 || len(snap.Items) != 0 {
		t.Errorf("acknowledged issue still counted: %+v", snap)
	}
	if len(snap.Acknowledged) != 1 || !snap.Acknowledged[0].Acknowledged {
		t.Fatalf("acknowledged list = %+v, want one acknowledged issue", snap.Acknowledged)
	}
	if snap.Acknowledged[0].AcknowledgedAt == nil || !snap.Acknowledged[0].AcknowledgedAt.Equal(fixed) {
		t.Errorf("AcknowledgedAt = %v, want %s", snap.Acknowledged[0].AcknowledgedAt, fixed)
	}

	// Re-acknowledging is a no-op, not an error.
	if err := store.acknowledge(iss.ID); err != nil {
		t.Errorf("second acknowledge: %v", err)
	}

	if err := store.unacknowledge(iss.ID); err != nil {
		t.Fatalf("unacknowledge: %v", err)
	}
	snap = store.snapshotIssues()
	if snap.Count != 1 || len(snap.Acknowledged) != 0 {
		t.Errorf("unacknowledge did not restore the active issue: %+v", snap)
	}
	if err := store.unacknowledge("deadbeef"); err != errIssueNotFound {
		t.Errorf("unacknowledge(unknown) = %v, want errIssueNotFound", err)
	}
}

func TestIssueStorePersistenceRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	store := newIssueStore(dir)
	iss := testIssue(categoryHealth, "container", "c1", "Unhealthy")
	if err := store.apply([]hostIssue{iss}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := store.acknowledge(iss.ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	path := filepath.Join(dir, issueStateFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state file: %v", err)
	}
	if got := info.Mode().Perm(); got != issueStateFileMode {
		t.Errorf("state file mode = %o, want %o", got, issueStateFileMode)
	}

	reloaded := newIssueStore(dir)
	if warns := reloaded.load(); len(warns) != 0 {
		t.Fatalf("load warnings: %+v", warns)
	}
	if err := reloaded.apply([]hostIssue{iss}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply after reload: %v", err)
	}
	snap := reloaded.snapshotIssues()
	if snap.Count != 0 || len(snap.Acknowledged) != 1 {
		t.Errorf("acknowledgement did not survive reload: %+v", snap)
	}
}

func TestIssueStoreLoadRejectsBadState(t *testing.T) {
	tests := []struct {
		name    string
		write   func(t *testing.T, path string)
		wantSub string
	}{
		{
			name: "malformed JSON",
			write: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "starting with no acknowledgements",
		},
		{
			name: "oversized",
			write: func(t *testing.T, path string) {
				t.Helper()
				big := make([]byte, maxIssueStateBytes+16)
				for i := range big {
					big[i] = 'a'
				}
				if err := os.WriteFile(path, big, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSub: "exceeds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, issueStateFileName)
			tt.write(t, path)

			store := newIssueStore(dir)
			warns := store.load()
			if len(warns) == 0 {
				t.Fatal("load returned no warning for bad state")
			}
			if !strings.Contains(warns[0].Message, tt.wantSub) {
				t.Errorf("warning = %q, want substring %q", warns[0].Message, tt.wantSub)
			}
			if got := len(store.snapshotIssues().Acknowledged); got != 0 {
				t.Errorf("bad state produced %d acknowledgements, want 0", got)
			}
		})
	}
}

func TestIssueStoreLoadSkipsInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	good := newIssueID(categoryUpdate, "container", "c1")
	state := issueStateFile{
		Version: issueStateVersion,
		Acknowledged: []issueAcknowledgement{
			{IssueID: good, AcknowledgedAt: time.Now().UTC()},
			{IssueID: "not-an-id", AcknowledgedAt: time.Now().UTC()},
			{IssueID: newIssueID(categoryUpdate, "container", "c2")},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, issueStateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := newIssueStore(dir)
	if warns := store.load(); len(warns) != 0 {
		t.Fatalf("load warnings: %+v", warns)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.ack) != 1 {
		t.Errorf("loaded %d acknowledgements, want only the valid one", len(store.ack))
	}
}

func TestIssueStoreAtomicReplacementLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	store := newIssueStore(dir)
	iss := testIssue(categoryUpdate, "container", "c1", "Update")

	for i := 0; i < 3; i++ {
		if err := store.apply([]hostIssue{iss}, completeCoverage(), time.Now()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if i%2 == 0 {
			if err := store.acknowledge(iss.ID); err != nil {
				t.Fatalf("acknowledge: %v", err)
			}
		} else if err := store.unacknowledge(iss.ID); err != nil {
			t.Fatalf("unacknowledge: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != issueStateFileName {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, issueStateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state issueStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("persisted state is not valid JSON: %v", err)
	}
	if state.Version != issueStateVersion {
		t.Errorf("state version = %d, want %d", state.Version, issueStateVersion)
	}
}

func TestIssueStorePrunesResolvedAcknowledgements(t *testing.T) {
	store := newIssueStore(t.TempDir())
	a := testIssue(categoryUpdate, "container", "a", "A")
	b := testIssue(categoryUpdate, "container", "b", "B")
	if err := store.apply([]hostIssue{a, b}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := store.acknowledge(a.ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	// Incomplete collection must not prune: a is simply missing this round.
	if err := store.apply([]hostIssue{b}, issueCoverage{CollectionComplete: false}, time.Now()); err != nil {
		t.Fatalf("apply partial: %v", err)
	}
	store.mu.RLock()
	if _, ok := store.ack[a.ID]; !ok {
		t.Error("incomplete collection pruned an acknowledgement")
	}
	store.mu.RUnlock()

	// Complete collection proves the issue resolved.
	if err := store.apply([]hostIssue{b}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply complete: %v", err)
	}
	store.mu.RLock()
	if _, ok := store.ack[a.ID]; ok {
		t.Error("complete collection did not prune the resolved acknowledgement")
	}
	store.mu.RUnlock()

	// A later recurrence is active again.
	if err := store.apply([]hostIssue{a, b}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply recurrence: %v", err)
	}
	if got := store.snapshotIssues().Count; got != 2 {
		t.Errorf("Count after recurrence = %d, want 2", got)
	}
}

func TestValidateStateDir(t *testing.T) {
	dir := t.TempDir()
	if msg := validateStateDir(dir); msg != "" {
		t.Errorf("validateStateDir(%q) = %q, want empty", dir, msg)
	}

	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := validateStateDir(file); msg == "" {
		t.Error("validateStateDir reported no problem for a regular file")
	}

	if msg := validateStateDir(""); msg == "" {
		t.Error("validateStateDir(\"\") reported no problem")
	}
}

func TestStateDirDefaultAndOverride(t *testing.T) {
	t.Setenv("DUMBDOCK_STATE_DIR", "")
	if got := stateDir(); got != "/data" {
		t.Errorf("stateDir() = %q, want /data", got)
	}
	t.Setenv("DUMBDOCK_STATE_DIR", " /var/lib/dumbdock ")
	if got := stateDir(); got != "/var/lib/dumbdock" {
		t.Errorf("stateDir() = %q, want /var/lib/dumbdock", got)
	}
}

// ---------------------------------------------------------------------------
// Collector tests
// ---------------------------------------------------------------------------

func findIssue(issues []hostIssue, category issueCategory, key string) *hostIssue {
	want := newIssueID(category, "container", key)
	for i := range issues {
		if issues[i].ID == want {
			return &issues[i]
		}
	}
	return nil
}

func TestCollectContainerIssues(t *testing.T) {
	cards := []containerCard{
		{
			ContainerID:    "aaaaaaaaaaaa",
			ContainerName:  "web",
			Image:          "nginx:1",
			UpdateAvailable: true,
			HasPublicBinding: true,
			PublicBindingIPs: []string{"0.0.0.0", "203.0.113.9"},
		},
		{
			ContainerID:     "bbbbbbbbbbbb",
			ContainerName:   "db",
			Image:           "postgres:16",
			HasPrivateBinding: true,
			PrivateBindingIPs: []string{"10.0.0.5"},
		},
		{
			ContainerID:    "cccccccccccc",
			ContainerName:  "local",
			Image:          "redis:7",
			HasLocalBinding: true,
			LocalBindingIPs: []string{"127.0.0.1"},
		},
		{
			ContainerID:     "dddddddddddd",
			ContainerName:   "hidden",
			Image:           "secret:1",
			UpdateAvailable: true,
			HasPublicBinding: true,
			PublicBindingIPs: []string{"0.0.0.0"},
		},
	}
	// Duplicate card must not double-count.
	cards = append(cards, cards[0])

	unhealthy := []dockerContainer{
		{ID: "aaaaaaaaaaaaffff", Names: []string{"/web"}, Image: "nginx:1", State: "running"},
		{ID: "ffffffffffff0000", Names: []string{"/gone"}, Image: "gone:1", State: "exited"},
	}

	issues, warnings, complete := collectContainerIssues(cards, unhealthy, nil, []string{"hidden"})
	if !complete {
		t.Errorf("complete = false with a healthy health input")
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	// web: update + exposure + unhealthy = 3; db/local: none; hidden: excluded; gone: not shown.
	if len(issues) != 3 {
		t.Fatalf("got %d issues (%+v), want 3", len(issues), issues)
	}
	if iss := findIssue(issues, categoryUpdate, "aaaaaaaaaaaa"); iss == nil || iss.Severity != severityWarning {
		t.Errorf("missing update issue for web: %+v", iss)
	}
	if iss := findIssue(issues, categoryExposure, "aaaaaaaaaaaa"); iss == nil {
		t.Error("missing exposure issue for web")
	} else if !strings.Contains(iss.Detail, "0.0.0.0") || !strings.Contains(iss.Detail, "203.0.113.9") {
		t.Errorf("exposure detail does not list sorted IPs: %q", iss.Detail)
	}
	if iss := findIssue(issues, categoryHealth, "aaaaaaaaaaaa"); iss == nil || iss.Severity != severityError {
		t.Errorf("missing unhealthy issue for web: %+v", iss)
	}
	for _, id := range []string{"bbbbbbbbbbbb", "cccccccccccc", "dddddddddddd"} {
		for _, iss := range issues {
			if iss.ContainerID == id {
				t.Errorf("container %s should not produce an issue: %+v", id, iss)
			}
		}
	}
	for _, iss := range issues {
		if iss.ContainerID == "ffffffffffff" {
			t.Errorf("hidden/exited container produced an issue: %+v", iss)
		}
	}
}

func TestCollectContainerIssuesDeduplicates(t *testing.T) {
	card := containerCard{ContainerID: "aaaaaaaaaaaa", ContainerName: "web", Image: "nginx:1", UpdateAvailable: true}
	issues, _, _ := collectContainerIssues([]containerCard{card, card}, nil, nil, nil)
	if len(issues) != 1 {
		t.Errorf("got %d issues, want 1", len(issues))
	}
}

func TestCollectContainerIssuesHealthUnavailable(t *testing.T) {
	card := containerCard{ContainerID: "aaaaaaaaaaaa", ContainerName: "web", Image: "nginx:1", UpdateAvailable: true}
	issues, warnings, complete := collectContainerIssues([]containerCard{card}, nil, errTest("docker unavailable"), nil)
	if complete {
		t.Error("complete = true when the health list failed")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "container health") {
		t.Errorf("warnings = %v", warnings)
	}
	if len(issues) != 1 {
		t.Errorf("health failure suppressed the available update issue: %+v", issues)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestCollectTraefikIssuesNotDetected(t *testing.T) {
	issues, warnings, complete := collectTraefikIssues(false, nil, "")
	if len(issues) != 0 || len(warnings) != 0 || !complete {
		t.Errorf("no-Traefik state produced issues=%v warnings=%v complete=%v", issues, warnings, complete)
	}
}

func TestCollectTraefikIssuesUnreachable(t *testing.T) {
	data := &traefikDashboardData{EndpointErrors: map[string]string{}}
	for _, name := range traefikEndpointNames {
		data.EndpointErrors[name] = "connection refused"
	}
	issues, warnings, complete := collectTraefikIssues(true, data, "API unreachable at http://traefik:8080: connection refused")
	if complete {
		t.Error("complete = true for an unreachable API")
	}
	if len(issues) != 1 || issues[0].Severity != severityError || issues[0].Category != categoryTraefikAPI {
		t.Fatalf("issues = %+v, want one traefik-api error", issues)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestCollectTraefikIssuesPartialEndpointErrors(t *testing.T) {
	data := &traefikDashboardData{
		EndpointErrors: map[string]string{"tlsCerts": "timeout"},
	}
	issues, _, complete := collectTraefikIssues(true, data, "")
	if complete {
		t.Error("complete = true with a partial endpoint failure")
	}
	if len(issues) != 1 || issues[0].Severity != severityWarning {
		t.Fatalf("issues = %+v, want one warning", issues)
	}
}

func TestCollectTraefikIssuesNotConfiguredIsNotAFinding(t *testing.T) {
	data := &traefikDashboardData{NotConfigured: []string{"tlsCerts"}}
	issues, warnings, complete := collectTraefikIssues(true, data, "")
	if len(issues) != 0 || len(warnings) != 0 || !complete {
		t.Errorf("not-configured endpoint produced issues=%v warnings=%v complete=%v", issues, warnings, complete)
	}
}

func TestCollectTraefikIssuesUnsupportedVersion(t *testing.T) {
	data := &traefikDashboardData{
		Version:        &traefikVersion{Version: "v2.11.0"},
		MajorVersion:   2,
		VersionSupported: false,
	}
	issues, _, complete := collectTraefikIssues(true, data, "")
	if !complete {
		t.Error("complete = false for a supported-API host running an unsupported version")
	}
	if len(issues) != 1 || issues[0].Category != categoryTraefikVersion || issues[0].Severity != severityWarning {
		t.Fatalf("issues = %+v, want one traefik-version warning", issues)
	}
}

func TestCollectTraefikIssuesEntitiesAndBackends(t *testing.T) {
	data := &traefikDashboardData{
		Version:          &traefikVersion{Version: "v3.7.13"},
		VersionSupported: true,
		MajorVersion:     3,
		HTTPRouters: []traefikRouter{
			{Name: "enabled", Status: "enabled"},
			{Name: "off", Status: "disabled"},
			{Name: "broken", Status: "error"},
		},
		HTTPServices: []traefikService{
			{Name: "web", Status: "warning", ServerStatus: map[string]string{
				"http://10.0.0.2:80": "UP",
				"http://10.0.0.3:80": "DOWN",
			}},
		},
		Middlewares: []traefikMiddleware{{Name: "auth", Status: "disabled"}},
	}

	issues, warnings, complete := collectTraefikIssues(true, data, "")
	if !complete || len(warnings) != 0 {
		t.Errorf("healthy API produced warnings=%v complete=%v", warnings, complete)
	}
	// off, broken, web(warning), auth disabled, DOWN backend.
	if len(issues) != 5 {
		t.Fatalf("got %d issues (%+v), want 5", len(issues), issues)
	}

	byTitle := map[string]hostIssue{}
	for _, iss := range issues {
		byTitle[iss.Title] = iss
	}
	if got, ok := byTitle["Traefik HTTP router is disabled"]; !ok || got.Severity != severityWarning {
		t.Errorf("missing disabled router warning: %+v", got)
	}
	if got, ok := byTitle["Traefik HTTP router is error"]; !ok || got.Severity != severityError {
		t.Errorf("missing error router finding: %+v", got)
	}
	if got, ok := byTitle["Traefik middleware is disabled"]; !ok || got.Severity != severityWarning {
		t.Errorf("missing disabled middleware warning: %+v", got)
	}
	if got, ok := byTitle["Traefik backend is down"]; !ok || got.Severity != severityError {
		t.Errorf("missing backend-down finding: %+v", got)
	}
	enabledID := newIssueID(categoryTraefikEntity, "traefik", "http-router/enabled")
	for _, iss := range issues {
		if iss.ID == enabledID {
			t.Errorf("enabled entity produced a finding: %+v", iss)
		}
	}
}

func TestCollectDockerVersionIssue(t *testing.T) {
	checked := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	issues, coverage := collectDockerVersionIssue(dockerVersionResponse{Version: "20.10.24-ce"}, nil, "v27.3.1", checked, "")
	if len(issues) != 1 || issues[0].Category != categoryDockerVersion {
		t.Fatalf("issues = %+v, want one docker-version finding", issues)
	}
	if !coverage.Checked || coverage.LastCheckedAt == nil || !coverage.LastCheckedAt.Equal(checked) {
		t.Errorf("coverage = %+v", coverage)
	}
	if !strings.Contains(issues[0].Detail, "may intentionally lag upstream") {
		t.Errorf("detail lacks the distro caveat: %q", issues[0].Detail)
	}

	for _, tt := range []struct {
		name    string
		daemon  string
		latest  string
		want    int
	}{
		{name: "equal", daemon: "27.3.1", latest: "v27.3.1", want: 0},
		{name: "newer", daemon: "27.4.0", latest: "v27.3.1", want: 0},
		{name: "prerelease daemon", daemon: "27.3.1-rc1", latest: "v27.3.1", want: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := collectDockerVersionIssue(dockerVersionResponse{Version: tt.daemon}, nil, tt.latest, checked, "")
			if len(got) != tt.want {
				t.Errorf("got %d issues, want %d: %+v", len(got), tt.want, got)
			}
		})
	}

	got, cov := collectDockerVersionIssue(dockerVersionResponse{}, errTest("socket down"), "v27.3.1", checked, "")
	if len(got) != 0 || cov.Checked || cov.Error == "" {
		t.Errorf("daemon failure produced issues=%+v coverage=%+v", got, cov)
	}

	got, cov = collectDockerVersionIssue(dockerVersionResponse{Version: "27.3.1"}, nil, "", time.Time{}, "status 500")
	if len(got) != 0 || cov.Checked || !strings.Contains(cov.Error, "status 500") {
		t.Errorf("missing release data produced issues=%+v coverage=%+v", got, cov)
	}
}
