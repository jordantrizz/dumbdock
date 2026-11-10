package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeArtifact(t *testing.T, dir, name string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestIsBackupArtifact(t *testing.T) {
	tests := map[string]bool{
		"postgres-app-20261107.sql.gz": true,
		"mysql.dump":                   true,
		"db.sql":                       true,
		"notes.sqlite":                 true,
		"archive.tar.gz":               true,
		"latest.bak":                   true,
		"notes.txt":                    false,
		".postgres.sql":                false,
		"postgres.sql.tmp":             false,
		"":                             false,
	}
	for name, want := range tests {
		if got := isBackupArtifact(name); got != want {
			t.Errorf("isBackupArtifact(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScanBackupDirNewestWins(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	writeArtifact(t, dir, "old.sql.gz", now.Add(-48*time.Hour))
	writeArtifact(t, dir, "new.sql.gz", now.Add(-2*time.Hour))
	writeArtifact(t, dir, "notes.txt", now)
	writeArtifact(t, dir, ".hidden.sql", now)
	if err := os.MkdirAll(filepath.Join(dir, "nested.sql"), 0o755); err != nil {
		t.Fatal(err)
	}

	ev, found, err := scanBackupDir(dir, time.Now().Add(3*time.Second))
	if err != nil {
		t.Fatalf("scanBackupDir: %v", err)
	}
	if !found {
		t.Fatal("no evidence found")
	}
	if ev.Artifact != "new.sql.gz" {
		t.Errorf("Artifact = %q, want new.sql.gz", ev.Artifact)
	}
	if ev.Provider != "filesystem" || ev.Status != "ok" {
		t.Errorf("evidence = %+v", ev)
	}

	// Missing directory is not an error and yields no evidence.
	if _, found, err := scanBackupDir(filepath.Join(dir, "missing"), now); err != nil || found {
		t.Errorf("missing dir: found=%v err=%v", found, err)
	}
}

func TestScanBackupDirRefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	writeArtifact(t, outside, "secret.sql", time.Now())
	dir := filepath.Join(root, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.sql"), filepath.Join(dir, "linked.sql")); err != nil {
		t.Skip("symlinks unavailable")
	}

	if _, found, err := scanBackupDir(dir, time.Now().Add(time.Second)); err != nil || found {
		t.Errorf("symlink was accepted as evidence: found=%v err=%v", found, err)
	}
}

func TestScanSQLiteFiles(t *testing.T) {
	root := t.TempDir()
	service := filepath.Join(root, "notes", "app")
	writeArtifact(t, service, "notes.db", time.Now())
	writeArtifact(t, filepath.Join(service, "cache"), "index.sqlite", time.Now())
	writeArtifact(t, service, "readme.txt", time.Now())

	containers := []dockerContainer{{
		Labels: map[string]string{
			"com.docker.compose.project": "notes",
			"com.docker.compose.service": "app",
		},
	}}
	got, errText, ok := scanSQLiteFiles(root, containers, time.Now().Add(5*time.Second))
	if !ok || errText != "" {
		t.Fatalf("scan failed: %q", errText)
	}
	list := got["notes/app"]
	if len(list) != 2 {
		t.Fatalf("sqlite files = %v, want 2", list)
	}
	for _, p := range list {
		if !strings.HasSuffix(p, ".db") && !strings.HasSuffix(p, ".sqlite") {
			t.Errorf("unexpected file %q", p)
		}
	}
}

func TestReportStorePersistenceAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	store := newReportStore(dir)

	if err := store.record(backupReport{Target: "mwp/postgres", OK: true, At: now, ReceivedAt: now, Artifact: "app.sql.gz"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, reportStateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}

	reloaded := newReportStore(dir)
	if warnings := reloaded.load(); len(warnings) != 0 {
		t.Fatalf("load warnings: %v", warnings)
	}
	if r, ok := reloaded.latest("mwp/postgres"); !ok || r.Artifact != "app.sql.gz" {
		t.Errorf("latest after reload = %+v", r)
	}

	// Reports for a live dataset survive pruning; a removed dataset's report is
	// kept until it ages out.
	if err := reloaded.prune(map[string]bool{"mwp/postgres": true}, now.Add(60*24*time.Hour)); err != nil {
		t.Fatalf("prune live: %v", err)
	}
	if _, ok := reloaded.latest("mwp/postgres"); !ok {
		t.Fatal("live report was pruned")
	}
	if err := reloaded.prune(map[string]bool{}, now.Add(reportPruneAge-time.Hour)); err != nil {
		t.Fatalf("prune young: %v", err)
	}
	if _, ok := reloaded.latest("mwp/postgres"); !ok {
		t.Fatal("report younger than the retention window was pruned")
	}
	if err := reloaded.prune(map[string]bool{}, now.Add(reportPruneAge+time.Hour)); err != nil {
		t.Fatalf("prune old: %v", err)
	}
	if _, ok := reloaded.latest("mwp/postgres"); ok {
		t.Fatal("old report for a removed dataset was not pruned")
	}
}

func newTestStorageWithDataset(key string) *storageStore {
	store := newStorageStore()
	store.apply(storageResponse{Datasets: []storageDataset{{Key: key, Engine: enginePostgres, Service: "postgres", Project: "mwp"}}}, nil, nil, true, true)
	return store
}

func TestReportHandler(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	newHandler := func(t *testing.T) (*reportHandler, *reportStore) {
		reports := newReportStore(t.TempDir())
		h := newReportHandler(reports, newTestStorageWithDataset("mwp/postgres"))
		h.now = func() time.Time { return now }
		return h, reports
	}

	post := func(t *testing.T, h *reportHandler, body string, header bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/backups/report", bytes.NewBufferString(body))
		if header {
			req.Header.Set(backupRequestHeader, backupRequestValue)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("missing header", func(t *testing.T) {
		h, _ := newHandler(t)
		if rec := post(t, h, `{"target":"mwp/postgres","ok":true}`, false); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("unknown target", func(t *testing.T) {
		h, _ := newHandler(t)
		if rec := post(t, h, `{"target":"other/db","ok":true}`, true); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
	t.Run("invalid artifact", func(t *testing.T) {
		h, _ := newHandler(t)
		if rec := post(t, h, `{"target":"mwp/postgres","ok":true,"artifact":"../../etc/passwd"}`, true); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("engine mismatch", func(t *testing.T) {
		h, _ := newHandler(t)
		if rec := post(t, h, `{"target":"mwp/postgres","engine":"mysql","ok":true}`, true); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("invalid at", func(t *testing.T) {
		h, _ := newHandler(t)
		if rec := post(t, h, `{"target":"mwp/postgres","ok":true,"at":"yesterday"}`, true); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("oversized body", func(t *testing.T) {
		h, _ := newHandler(t)
		big := `{"target":"mwp/postgres","ok":true,"detail":"` + strings.Repeat("a", maxBackupReportBytes) + `"}`
		if rec := post(t, h, big, true); rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d, want 413", rec.Code)
		}
	})
	t.Run("accepted and persisted", func(t *testing.T) {
		h, reports := newHandler(t)
		body, _ := json.Marshal(map[string]interface{}{
			"target":   "mwp/postgres",
			"ok":       true,
			"at":       now.Add(-time.Hour).Format(time.RFC3339),
			"artifact": "postgres-mwp-20261107.sql.gz",
			"detail":   "pg_dump ok\nnext line",
		})
		rec := post(t, h, string(body), true)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		r, ok := reports.latest("mwp/postgres")
		if !ok || r.Artifact != "postgres-mwp-20261107.sql.gz" {
			t.Fatalf("stored report = %+v", r)
		}
		if strings.Contains(r.Detail, "\n") {
			t.Errorf("detail was not sanitized: %q", r.Detail)
		}
	})
	t.Run("future timestamp is clamped", func(t *testing.T) {
		h, reports := newHandler(t)
		future := now.Add(24 * time.Hour).Format(time.RFC3339)
		body := `{"target":"mwp/postgres","ok":true,"at":"` + future + `"}`
		if rec := post(t, h, body, true); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		r, _ := reports.latest("mwp/postgres")
		if r.At.After(now) {
			t.Errorf("future report time was not clamped: %s", r.At)
		}
	})
}

func TestEvaluateDatasetVerdicts(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	base := storageDataset{Key: "mwp/postgres", Engine: enginePostgres, PolicyMaxAge: "26h0m0s", Required: true}

	ds := base
	ds.Evidence = []backupEvidence{{Provider: "filesystem", Status: "ok", At: now.Add(-2 * time.Hour)}}
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictOK {
		t.Errorf("fresh evidence verdict = %q", ds.Verdict)
	}

	ds = base
	ds.Evidence = []backupEvidence{{Provider: "filesystem", Status: "ok", At: now.Add(-48 * time.Hour)}}
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictStale {
		t.Errorf("stale evidence verdict = %q", ds.Verdict)
	}

	ds = base
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictMissing {
		t.Errorf("no evidence verdict = %q", ds.Verdict)
	}

	// No provider configured must never read as missing.
	ds = base
	evaluateDataset(&ds, false, now)
	if ds.Verdict != verdictUnverified {
		t.Errorf("unconfigured provider verdict = %q", ds.Verdict)
	}

	// Not required and no evidence stays unverified.
	ds = base
	ds.Required = false
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictUnverified {
		t.Errorf("not-required verdict = %q", ds.Verdict)
	}

	ds = base
	ds.Exempt = true
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictExempt {
		t.Errorf("exempt verdict = %q", ds.Verdict)
	}

	// A failed report is not evidence of a backup.
	ds = base
	ds.Evidence = []backupEvidence{{Provider: "reports", Status: "failed", At: now.Add(-time.Hour)}}
	evaluateDataset(&ds, true, now)
	if ds.Verdict != verdictMissing {
		t.Errorf("failed-report verdict = %q", ds.Verdict)
	}
}

func TestCollectBackupIssues(t *testing.T) {
	datasets := []storageDataset{
		{Key: "a/db", Engine: enginePostgres, Verdict: verdictOK, PolicyMaxAge: "26h0m0s", Required: true, BackupDir: "/srv/containers/a/db/backups"},
		{Key: "b/db", Engine: engineMySQL, Verdict: verdictMissing, PolicyMaxAge: "26h0m0s", Required: true, BackupDir: "/srv/containers/b/db/backups"},
		{Key: "c/db", Engine: engineMariaDB, Verdict: verdictStale, PolicyMaxAge: "26h0m0s", Required: true, BackupDir: "/srv/containers/c/db/backups"},
		{Key: "d/db", Engine: engineSQLite, Verdict: verdictUnverified, PolicyMaxAge: "26h0m0s", Required: true, BackupDir: "/srv/containers/d/db/backups"},
		{Key: "e/db", Engine: engineSQLite, Verdict: verdictExempt, PolicyMaxAge: "26h0m0s", Required: true, BackupDir: "/srv/containers/e/db/backups"},
	}
	issues := collectBackupIssues(datasets)
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2: %+v", len(issues), issues)
	}
	if issues[0].Severity != severityError || issues[0].ContainerName != "" {
		// Ordering is by dataset order; b/db comes before c/db.
	}
	byID := map[string]hostIssue{}
	for _, is := range issues {
		byID[is.ID] = is
	}
	if is, ok := byID[newIssueID(categoryBackup, "backup", "b/db")]; !ok || is.Severity != severityError {
		t.Errorf("missing-evidence issue = %+v", is)
	}
	if is, ok := byID[newIssueID(categoryBackup, "backup", "c/db")]; !ok || is.Severity != severityWarning {
		t.Errorf("stale issue = %+v", is)
	}
}

func TestEvidenceEngineFilesystemAndReports(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	cfg := storageConfig{
		ContainerRoot: "/opt/containers",
		DataRoot:      root,
		BackupRoot:    root,
		DefaultMaxAge: 26 * time.Hour,
	}
	dir := filepath.Join(root, "mwp", "postgres", "backups")
	writeArtifact(t, dir, "app.sql.gz", now.Add(-time.Hour))

	reports := newReportStore(t.TempDir())
	if err := reports.record(backupReport{Target: "mwp/postgres", OK: true, At: now.Add(-30 * time.Minute), ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}

	engine := &evidenceEngine{cfg: cfg, reports: reports, pushEnabled: true}
	datasets := []storageDataset{{Key: "mwp/postgres", Engine: enginePostgres, BackupDir: dir}}

	out := engine.collect(nil, datasets, now)
	if !out.configured {
		t.Fatal("providers should be configured")
	}
	list := out.evidence["mwp/postgres"]
	if len(list) != 2 {
		t.Fatalf("evidence = %+v, want filesystem and report", list)
	}
	if list[0].Provider != "reports" {
		t.Errorf("evidence not sorted newest-first: %+v", list)
	}
	if len(out.providers) != 3 {
		t.Errorf("providers = %+v", out.providers)
	}
}

func TestEvidenceEngineMissingRootIsNotConfigured(t *testing.T) {
	now := time.Now()
	cfg := storageConfig{DataRoot: "/nonexistent-data-root", BackupRoot: "/nonexistent-data-root"}
	engine := &evidenceEngine{cfg: cfg, reports: newReportStore(t.TempDir())}
	out := engine.collect(nil, []storageDataset{{Key: "mwp/postgres"}}, now)
	if out.configured {
		t.Error("missing root must not count as a configured provider")
	}
	if !out.complete {
		t.Error("an unmounted root is a coverage state, not an incomplete collection")
	}
}
