package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Evidence collection
// ---------------------------------------------------------------------------

const (
	backupRequestHeader = "X-Dumbdock-Request"
	backupRequestValue  = "backups"

	maxBackupReportBytes = 16 << 10 // 16 KiB
	maxBackupDetailBytes = 200
	maxArtifactNameBytes = 128
	maxBackupReportAge   = 90 * 24 * time.Hour
	reportStateFileName  = "backup-reports.json"
	reportStateVersion   = 1
	reportStateFileMode  = 0o600
	reportPruneAge       = 30 * 24 * time.Hour
)

// backup artifact suffixes accepted as evidence. Contents are never read.
var backupArtifactSuffixes = []string{
	".sql", ".sql.gz", ".sql.zst",
	".dump", ".dump.gz", ".dump.zst",
	".tar.gz", ".tar.zst",
	".sqlite", ".sqlite.gz", ".sqlite3", ".db.gz", ".bak",
}

// isBackupArtifact reports whether a file name is a recognized backup artifact.
func isBackupArtifact(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || strings.HasPrefix(n, ".") {
		return false
	}
	for _, suffix := range backupArtifactSuffixes {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// scanBackupDir returns the newest recognized artifact in one backups
// directory. A missing directory is "no evidence", not an error.
func scanBackupDir(dir string, deadline time.Time) (backupEvidence, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return backupEvidence{}, false, nil
		}
		return backupEvidence{}, false, err
	}

	var newest *backupEvidence
	seen := 0
	for _, entry := range entries {
		if seen >= maxEvidenceEntries || time.Now().After(deadline) {
			break
		}
		seen++
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !isBackupArtifact(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		ev := backupEvidence{
			Provider: "filesystem",
			Status:   "ok",
			At:       info.ModTime(),
			Artifact: truncateForLog(entry.Name(), maxArtifactNameBytes),
		}
		if newest == nil || ev.At.After(newest.At) {
			copy := ev
			newest = &copy
		}
	}
	if newest == nil {
		return backupEvidence{}, false, nil
	}
	return *newest, true, nil
}

// scanSQLiteFiles looks for sqlite database files under each service data
// directory. The scan is depth- and entry-bounded and refuses symlinks.
func scanSQLiteFiles(root string, containers []dockerContainer, deadline time.Time) (map[string][]string, string, bool) {
	out := map[string][]string{}
	if _, ok := normalizeHostPath(root); !ok {
		return out, "invalid data root", false
	}
	scanned := 0
	services := 0
	for _, c := range containers {
		project := safePathSegment(c.composeProject())
		service := safePathSegment(c.composeService())
		if project == "" || service == "" {
			continue
		}
		if services >= 50 || time.Now().After(deadline) {
			break
		}
		services++
		dir := filepath.Join(root, project, service)
		if !pathWithin(root, dir) {
			continue
		}
		found, err := walkSQLite(dir, 1, &scanned, deadline)
		if err != nil {
			// A missing per-service directory is normal.
			continue
		}
		if len(found) > 0 {
			out[project+"/"+service] = found
		}
	}
	return out, "", true
}

// walkSQLite collects sqlite-looking files under dir, bounded by depth and the
// shared entry budget.
func walkSQLite(dir string, depth int, scanned *int, deadline time.Time) ([]string, error) {
	if depth > maxSQLiteScanDepth || time.Now().After(deadline) || *scanned >= maxSQLiteScanEntries {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if *scanned >= maxSQLiteScanEntries || time.Now().After(deadline) {
			break
		}
		*scanned++
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := strings.ToLower(entry.Name())
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") {
				continue
			}
			sub, err := walkSQLite(filepath.Join(dir, entry.Name()), depth+1, scanned, deadline)
			if err == nil {
				out = append(out, sub...)
			}
			continue
		}
		switch {
		case strings.HasSuffix(name, ".db"), strings.HasSuffix(name, ".sqlite"), strings.HasSuffix(name, ".sqlite3"):
			out = append(out, filepath.Join(dir, entry.Name()))
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Push reports
// ---------------------------------------------------------------------------

// backupReport is one report accepted from a backup job.
type backupReport struct {
	Target     string    `json:"target"`
	Engine     string    `json:"engine,omitempty"`
	OK         bool      `json:"ok"`
	At         time.Time `json:"at"`
	Artifact   string    `json:"artifact,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type backupReportFile struct {
	Version int            `json:"version"`
	Reports []backupReport `json:"reports"`
}

// reportStore owns the persisted push reports. It never stores dump contents,
// only the target, a bounded artifact name, and the reported time.
type reportStore struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	reports map[string]backupReport
}

func newReportStore(stateDir string) *reportStore {
	path := ""
	if strings.TrimSpace(stateDir) != "" {
		path = filepath.Join(stateDir, reportStateFileName)
	}
	return &reportStore{
		path:    path,
		now:     time.Now,
		reports: map[string]backupReport{},
	}
}

// load reads the persisted reports. A malformed or oversized file is a
// warning, not a crash.
func (s *reportStore) load() []configWarning {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []configWarning{{Field: "backup reports", Message: fmt.Sprintf("read %s: %v", s.path, err)}}
	}
	if len(data) > maxIssueStateBytes {
		return []configWarning{{Field: "backup reports", Message: fmt.Sprintf("%s exceeds %d bytes; starting with no reports", s.path, maxIssueStateBytes)}}
	}
	var file backupReportFile
	if err := json.Unmarshal(data, &file); err != nil {
		return []configWarning{{Field: "backup reports", Message: fmt.Sprintf("parse %s: %v; starting with no reports", s.path, err)}}
	}
	loaded := map[string]backupReport{}
	for _, r := range file.Reports {
		if r.Target == "" || r.At.IsZero() {
			continue
		}
		loaded[r.Target] = r
	}
	s.mu.Lock()
	s.reports = loaded
	s.mu.Unlock()
	return nil
}

// persistLocked writes the reports atomically. The caller must hold s.mu.
func (s *reportStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	keys := make([]string, 0, len(s.reports))
	for k := range s.reports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	file := backupReportFile{Version: reportStateVersion, Reports: make([]backupReport, 0, len(keys))}
	for _, k := range keys {
		file.Reports = append(file.Reports, s.reports[k])
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode backup reports: %w", err)
	}
	data = append(data, '\n')
	return writeStateFile(s.path, data, reportStateFileMode)
}

// record stores the latest report for a target.
func (s *reportStore) record(r backupReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.reports[r.Target]
	s.reports[r.Target] = r
	if err := s.persistLocked(); err != nil {
		if had {
			s.reports[r.Target] = prev
		} else {
			delete(s.reports, r.Target)
		}
		return err
	}
	return nil
}

// latest returns the newest report for a target.
func (s *reportStore) latest(target string) (backupReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[target]
	return r, ok
}

// len returns the number of stored reports.
func (s *reportStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

// prune drops reports for datasets that no longer exist once they are older
// than the retention window. Reports for live datasets are always kept.
func (s *reportStore) prune(valid map[string]bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := false
	for target, r := range s.reports {
		if valid[target] {
			continue
		}
		if now.Sub(r.ReceivedAt) > reportPruneAge {
			delete(s.reports, target)
			pruned = true
		}
	}
	if !pruned {
		return nil
	}
	return s.persistLocked()
}

// writeStateFile writes data to path through a same-directory temporary file
// with the requested mode, then renames it into place.
func writeStateFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, issueStateDirMode); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".dumbdock-state-*")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	name := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(name)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace state file: %w", err)
	}
	syncDir(dir)
	return nil
}

// ---------------------------------------------------------------------------
// Evidence engine
// ---------------------------------------------------------------------------

// evidenceEngine collects evidence from the three providers and caches nothing
// itself; the storage collector owns the TTL.
type evidenceEngine struct {
	cfg         storageConfig
	docker      *http.Client
	reports     *reportStore
	pushEnabled bool
}

type evidenceOutcome struct {
	evidence   map[string][]backupEvidence
	providers  []storageProviderCoverage
	configured bool
	complete   bool
	warnings   []string
}

// collect gathers evidence for the given datasets.
func (e *evidenceEngine) collect(containers []dockerContainer, datasets []storageDataset, now time.Time) evidenceOutcome {
	out := evidenceOutcome{
		evidence:  map[string][]backupEvidence{},
		providers: []storageProviderCoverage{},
		complete:  true,
	}
	deadline := time.Now().Add(3 * time.Second)

	// ---- filesystem provider ----
	fsProvider := storageProviderCoverage{Name: "filesystem", Enabled: false, Configured: false}
	root, rootOK := normalizeHostPath(e.cfg.BackupRoot)
	if rootOK {
		info, err := os.Stat(root)
		switch {
		case err == nil && info.IsDir():
			fsProvider.Enabled = true
			fsProvider.Configured = true
			out.configured = true
			checked := now
			fsProvider.LastCheckedAt = &checked
		case err == nil:
			fsProvider.Error = fmt.Sprintf("%s is not a directory", root)
			out.complete = false
			out.warnings = append(out.warnings, "backup evidence: "+truncateForLog(fsProvider.Error, 160))
		case errors.Is(err, os.ErrNotExist):
			fsProvider.Error = fmt.Sprintf("%s is not mounted", root)
		default:
			fsProvider.Error = truncateForLog(err.Error(), 160)
			out.complete = false
			out.warnings = append(out.warnings, "backup evidence: "+fsProvider.Error)
		}
	} else {
		fsProvider.Error = "invalid backup root"
	}

	if fsProvider.Enabled {
		for _, ds := range datasets {
			if time.Now().After(deadline) {
				break
			}
			ev, found, err := scanBackupDir(ds.BackupDir, deadline)
			if err != nil {
				fsProvider.Error = truncateForLog(err.Error(), 160)
				out.complete = false
				out.warnings = append(out.warnings, "backup evidence: "+fsProvider.Error)
				continue
			}
			if found {
				out.evidence[ds.Key] = append(out.evidence[ds.Key], ev)
			}
		}
	}
	out.providers = append(out.providers, fsProvider)

	// ---- backup container provider ----
	containerProvider := storageProviderCoverage{Name: "backup-containers", Enabled: false, Configured: false}
	for _, c := range containers {
		target := strings.TrimSpace(c.Labels["dumbdock.backup-for"])
		if target == "" {
			continue
		}
		containerProvider.Configured = true
		out.configured = true
		if containerProvider.LastCheckedAt == nil {
			checked := now
			containerProvider.LastCheckedAt = &checked
		}
		known := false
		for _, ds := range datasets {
			if ds.Key == target {
				known = true
				break
			}
		}
		if !known {
			continue
		}
		inspect, err := fetchContainerInspect(e.docker, c.ID)
		if err != nil {
			containerProvider.Error = truncateForLog(err.Error(), 160)
			out.complete = false
			continue
		}
		if inspect.State.Status == "exited" && inspect.State.ExitCode == 0 {
			at, err := time.Parse(time.RFC3339, inspect.State.FinishedAt)
			if err == nil && !at.IsZero() {
				out.evidence[target] = append(out.evidence[target], backupEvidence{
					Provider: "backup-containers",
					Status:   "ok",
					At:       at,
					Artifact: truncateForLog(containerName(c), maxArtifactNameBytes),
				})
			}
		}
	}
	if containerProvider.Configured {
		containerProvider.Enabled = true
	}
	out.providers = append(out.providers, containerProvider)

	// ---- push reports ----
	reportProvider := storageProviderCoverage{Name: "reports", Enabled: e.pushEnabled || e.reports.len() > 0, Configured: e.pushEnabled || e.reports.len() > 0}
	if reportProvider.Enabled {
		checked := now
		reportProvider.LastCheckedAt = &checked
		out.configured = true
		for _, ds := range datasets {
			r, ok := e.reports.latest(ds.Key)
			if !ok {
				continue
			}
			status := "failed"
			if r.OK {
				status = "ok"
			}
			out.evidence[ds.Key] = append(out.evidence[ds.Key], backupEvidence{
				Provider: "reports",
				Status:   status,
				At:       r.At,
				Artifact: truncateForLog(r.Artifact, maxArtifactNameBytes),
				Detail:   truncateForLog(r.Detail, maxBackupDetailBytes),
			})
		}
	} else {
		reportProvider.Error = "not configured (set DUMBDOCK_BACKUP_TOKEN)"
	}
	out.providers = append(out.providers, reportProvider)

	for key := range out.evidence {
		list := out.evidence[key]
		sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
		out.evidence[key] = list
	}
	return out
}

// ---------------------------------------------------------------------------
// Report endpoint
// ---------------------------------------------------------------------------

// backupReportRequest is the accepted request body.
type backupReportRequest struct {
	Target   string `json:"target"`
	Engine   string `json:"engine"`
	OK       bool   `json:"ok"`
	At       string `json:"at"`
	Artifact string `json:"artifact"`
	Detail   string `json:"detail"`
}

// isSafeArtifactName accepts a bare file name: no separators, no control
// characters, bounded length.
func isSafeArtifactName(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > maxArtifactNameBytes {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00\r\n\t") || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}

// sanitizeDetail removes control characters and bounds the length.
func sanitizeDetail(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return truncateForLog(strings.TrimSpace(b.String()), maxBackupDetailBytes)
}

// reportHandler accepts an authenticated backup report.
type reportHandler struct {
	reports *reportStore
	storage *storageStore
	now     func() time.Time
}

// ServeHTTP validates and stores one report. Unknown targets are refused so a
// report can never create a filesystem path or an untracked dataset.
func (h *reportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(backupRequestHeader) != backupRequestValue {
		writeIssueError(w, http.StatusForbidden, "missing "+backupRequestHeader+" header")
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBackupReportBytes)
	var req backupReportRequest
	dec := json.NewDecoder(body)
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeIssueError(w, http.StatusRequestEntityTooLarge, "report body too large")
			return
		}
		writeIssueError(w, http.StatusBadRequest, "invalid report body")
		return
	}

	target := strings.TrimSpace(req.Target)
	if target == "" || !h.storage.hasDataset(target) {
		writeIssueError(w, http.StatusNotFound, "unknown backup target")
		return
	}
	engine, _ := h.storage.datasetEngine(target)
	if want := engineFromLabel(req.Engine); strings.TrimSpace(req.Engine) != "" && want != engine {
		writeIssueError(w, http.StatusBadRequest, "engine does not match the target")
		return
	}
	if !isSafeArtifactName(req.Artifact) {
		writeIssueError(w, http.StatusBadRequest, "invalid artifact name")
		return
	}

	now := h.now()
	at := now
	if strings.TrimSpace(req.At) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(req.At))
		if err != nil {
			writeIssueError(w, http.StatusBadRequest, "invalid at timestamp")
			return
		}
		at = parsed
		if at.After(now.Add(5 * time.Minute)) {
			at = now
		}
		if at.Before(now.Add(-maxBackupReportAge)) {
			writeIssueError(w, http.StatusBadRequest, "at timestamp is too old")
			return
		}
	}

	report := backupReport{
		Target:     target,
		Engine:     string(engine),
		OK:         req.OK,
		At:         at.UTC(),
		Artifact:   req.Artifact,
		Detail:     sanitizeDetail(req.Detail),
		ReceivedAt: now.UTC(),
	}
	if err := h.reports.record(report); err != nil {
		writeIssueError(w, http.StatusInternalServerError, "could not persist report")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"target": target,
	})
}

// newReportHandler builds the report handler.
func newReportHandler(reports *reportStore, storage *storageStore) *reportHandler {
	return &reportHandler{reports: reports, storage: storage, now: time.Now}
}
