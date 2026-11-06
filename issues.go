package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// issueSeverity ranks a finding for display. Only two levels are used: an
// "error" is a broken or unsafe state that needs attention; a "warning" is a
// deviation worth knowing about.
type issueSeverity string

const (
	severityError   issueSeverity = "error"
	severityWarning issueSeverity = "warning"
)

// issueCategory groups findings by their source.
type issueCategory string

const (
	categoryExposure        issueCategory = "container-exposure"
	categoryUpdate          issueCategory = "container-update"
	categoryHealth          issueCategory = "container-health"
	categoryDockerVersion   issueCategory = "docker-version"
	categoryTraefikAPI      issueCategory = "traefik-api"
	categoryTraefikVersion  issueCategory = "traefik-version"
	categoryTraefikEntity   issueCategory = "traefik-entity"
	categoryTraefikBackend  issueCategory = "traefik-backend"
)

// hostIssue is one reported finding. It deliberately carries no Docker labels,
// registry tokens, Traefik credentials, or raw log text.
type hostIssue struct {
	ID            string        `json:"id"`
	Category      issueCategory `json:"category"`
	Severity      issueSeverity `json:"severity"`
	Title         string        `json:"title"`
	Detail        string        `json:"detail"`
	Source        string        `json:"source"`
	ContainerID   string        `json:"containerId,omitempty"`
	ContainerName string        `json:"containerName,omitempty"`
	FirstObservedAt time.Time   `json:"firstObservedAt"`
	LastObservedAt  time.Time   `json:"lastObservedAt"`
	Acknowledged    bool        `json:"acknowledged"`
	AcknowledgedAt  *time.Time  `json:"acknowledgedAt,omitempty"`
}

// issueCoverageSource reports whether one input to the issue collectors was
// available. Unavailable inputs never raise an issue; they are surfaced so an
// empty Issues page is not mistaken for a healthy host.
type issueCoverageSource struct {
	Checked       bool       `json:"checked"`
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// issueCoverage carries the collection-quality metadata for a snapshot.
type issueCoverage struct {
	CollectionComplete bool                  `json:"collectionComplete"`
	DockerVersion      issueCoverageSource   `json:"dockerVersion"`
	Warnings           []string              `json:"warnings,omitempty"`
}

// issuesResponse is the payload of GET /api/issues.
type issuesResponse struct {
	Count        int           `json:"count"`
	Items        []hostIssue   `json:"items"`
	Acknowledged []hostIssue   `json:"acknowledged"`
	Coverage     issueCoverage `json:"coverage"`
	CheckedAt    time.Time     `json:"checkedAt"`
}

const (
	issueStateFileName  = "issues-acknowledged.json"
	issueStateVersion   = 1
	maxIssueStateBytes  = 1 << 20 // 1 MiB
	issueStateFileMode  = 0o600
	issueStateDirMode   = 0o700
)

// errIssueNotFound is returned when an acknowledgement names an ID that is not
// in the current snapshot.
var errIssueNotFound = errors.New("issue not found")

// newIssueID derives a stable, opaque identifier from the finding's category,
// source, and canonical object key. Display text, timestamps, and user input
// are intentionally excluded so a re-worded title does not create a new
// acknowledgement.
func newIssueID(category issueCategory, source, objectKey string) string {
	sum := sha256.Sum256([]byte(string(category) + "\x00" + source + "\x00" + objectKey))
	return hex.EncodeToString(sum[:])
}

// isIssueID reports whether id has the shape produced by newIssueID.
func isIssueID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// issueAcknowledgement is one persisted acknowledgement record.
type issueAcknowledgement struct {
	IssueID        string    `json:"issueId"`
	AcknowledgedAt time.Time `json:"acknowledgedAt"`
}

// issueStateFile is the on-disk acknowledgement store.
type issueStateFile struct {
	Version      int                    `json:"version"`
	Acknowledged []issueAcknowledgement `json:"acknowledged"`
}

// issueStore owns the current issue snapshot and the acknowledgement map. All
// fields are guarded by mu; readers receive a published response value that is
// never mutated after publication.
type issueStore struct {
	mu        sync.RWMutex
	path      string
	now       func() time.Time
	ack       map[string]time.Time
	firstSeen map[string]time.Time
	known     map[string]hostIssue
	coverage  issueCoverage
	checkedAt time.Time

	// published is replaced wholesale under mu; readers may hold a copy of the
	// value after unlocking because its slices are never mutated in place.
	published issuesResponse
}

func newIssueStore(stateDir string) *issueStore {
	path := ""
	if strings.TrimSpace(stateDir) != "" {
		path = filepath.Join(stateDir, issueStateFileName)
	}
	return &issueStore{
		path:      path,
		now:       time.Now,
		ack:       make(map[string]time.Time),
		firstSeen: make(map[string]time.Time),
		known:     make(map[string]hostIssue),
		published: issuesResponse{Items: []hostIssue{}, Acknowledged: []hostIssue{}},
	}
}

// load reads the persisted acknowledgement file. A missing file is normal; a
// malformed, oversized, or unreadable file yields a warning and an empty
// acknowledgement map so the process keeps running.
func (s *issueStore) load() []configWarning {
	if s.path == "" {
		return nil
	}

	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []configWarning{{Field: "issues state", Message: fmt.Sprintf("read %s: %v", s.path, err)}}
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxIssueStateBytes+1))
	if err != nil {
		return []configWarning{{Field: "issues state", Message: fmt.Sprintf("read %s: %v", s.path, err)}}
	}
	if len(data) > maxIssueStateBytes {
		return []configWarning{{Field: "issues state", Message: fmt.Sprintf("%s exceeds %d bytes; starting with no acknowledgements", s.path, maxIssueStateBytes)}}
	}

	var state issueStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return []configWarning{{Field: "issues state", Message: fmt.Sprintf("parse %s: %v; starting with no acknowledgements", s.path, err)}}
	}

	loaded := make(map[string]time.Time, len(state.Acknowledged))
	for _, rec := range state.Acknowledged {
		if !isIssueID(rec.IssueID) || rec.AcknowledgedAt.IsZero() {
			continue
		}
		loaded[rec.IssueID] = rec.AcknowledgedAt
	}

	s.mu.Lock()
	s.ack = loaded
	s.mu.Unlock()
	return nil
}

// persistLocked writes the acknowledgement map atomically. The caller must
// hold s.mu for writing.
func (s *issueStore) persistLocked() error {
	if s.path == "" {
		return nil
	}

	ids := make([]string, 0, len(s.ack))
	for id := range s.ack {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	state := issueStateFile{Version: issueStateVersion, Acknowledged: make([]issueAcknowledgement, 0, len(ids))}
	for _, id := range ids {
		state.Acknowledged = append(state.Acknowledged, issueAcknowledgement{IssueID: id, AcknowledgedAt: s.ack[id]})
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode issues state: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, issueStateDirMode); err != nil {
		return fmt.Errorf("create issues state dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".issues-ack-*")
	if err != nil {
		return fmt.Errorf("create temp issues state: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if err := tmp.Chmod(issueStateFileMode); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp issues state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp issues state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp issues state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp issues state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace issues state: %w", err)
	}
	syncDir(dir)
	return nil
}

// syncDir flushes a directory entry after a rename. Failures are intentionally
// ignored: the rename already succeeded and not every platform supports it.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

// severityRank orders errors before warnings.
func severityRank(s issueSeverity) int {
	if s == severityError {
		return 0
	}
	return 1
}

// sortIssues orders a slice deterministically: severity, category, title, ID.
func sortIssues(list []hostIssue) {
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := severityRank(list[i].Severity), severityRank(list[j].Severity); ri != rj {
			return ri < rj
		}
		if list[i].Category != list[j].Category {
			return list[i].Category < list[j].Category
		}
		ti, tj := strings.ToLower(list[i].Title), strings.ToLower(list[j].Title)
		if ti != tj {
			return ti < tj
		}
		return list[i].ID < list[j].ID
	})
}

// rebuildLocked recomputes the published snapshot from the known issues and
// the acknowledgement map. The caller must hold s.mu.
func (s *issueStore) rebuildLocked() {
	active := make([]hostIssue, 0, len(s.known))
	acknowledged := make([]hostIssue, 0)
	for _, iss := range s.known {
		iss.Acknowledged = false
		iss.AcknowledgedAt = nil
		if at, ok := s.ack[iss.ID]; ok {
			t := at
			iss.Acknowledged = true
			iss.AcknowledgedAt = &t
			acknowledged = append(acknowledged, iss)
			continue
		}
		active = append(active, iss)
	}
	sortIssues(active)
	sortIssues(acknowledged)
	s.publishLocked(active, acknowledged)
}

// publishLocked stores the computed lists. The store keeps no other reference
// to the published response, so a caller holding the returned value never sees
// it mutate.
func (s *issueStore) publishLocked(active, acknowledged []hostIssue) {
	// Assign through the package-level holder to keep one published value.
	s.published = issuesResponse{
		Count:        len(active),
		Items:        active,
		Acknowledged: acknowledged,
		Coverage:     s.coverage,
		CheckedAt:    s.checkedAt,
	}
}

// snapshotIssues returns the current published response. The returned value is
// safe to read after the lock is released because publishLocked replaces it
// wholesale rather than mutating its slices.
func (s *issueStore) snapshotIssues() issuesResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.published
}

// apply replaces the known issue set, reconciles acknowledgements, and
// publishes a new snapshot. Acknowledgements are pruned only when the caller
// reports a complete collection and the issue is genuinely gone.
func (s *issueStore) apply(found []hostIssue, coverage issueCoverage, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	known := make(map[string]hostIssue, len(found))
	for _, iss := range found {
		if iss.ID == "" {
			continue
		}
		if _, dup := known[iss.ID]; dup {
			continue
		}
		first := now
		if t, ok := s.firstSeen[iss.ID]; ok {
			first = t
		}
		iss.FirstObservedAt = first
		iss.LastObservedAt = now
		iss.Acknowledged = false
		iss.AcknowledgedAt = nil
		known[iss.ID] = iss
		s.firstSeen[iss.ID] = first
	}

	pruned := false
	if coverage.CollectionComplete {
		for id := range s.firstSeen {
			if _, ok := known[id]; !ok {
				delete(s.firstSeen, id)
			}
		}
		for id := range s.ack {
			if _, ok := known[id]; !ok {
				delete(s.ack, id)
				pruned = true
			}
		}
	}

	s.known = known
	s.coverage = coverage
	if s.coverage.Warnings == nil {
		s.coverage.Warnings = []string{}
	}
	s.checkedAt = now
	s.rebuildLocked()

	if pruned {
		return s.persistLocked()
	}
	return nil
}

// hasIssueLocked reports whether id is present in the known set.
func (s *issueStore) hasIssueLocked(id string) bool {
	_, ok := s.known[id]
	return ok
}

// acknowledge marks an existing issue acknowledged. The state file is written
// before the published snapshot changes, so a failed write leaves the visible
// state untouched.
func (s *issueStore) acknowledge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasIssueLocked(id) {
		return errIssueNotFound
	}
	if _, already := s.ack[id]; already {
		return nil
	}

	now := s.now()
	s.ack[id] = now
	if err := s.persistLocked(); err != nil {
		delete(s.ack, id)
		return err
	}
	s.rebuildLocked()
	return nil
}

// unacknowledge removes an acknowledgment from an existing issue.
func (s *issueStore) unacknowledge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasIssueLocked(id) {
		return errIssueNotFound
	}
	prev, ok := s.ack[id]
	if !ok {
		return nil
	}

	delete(s.ack, id)
	if err := s.persistLocked(); err != nil {
		s.ack[id] = prev
		return err
	}
	s.rebuildLocked()
	return nil
}

// ---------------------------------------------------------------------------
// Collectors
// ---------------------------------------------------------------------------

// shortContainerID matches the 12-character ID dumbdock exposes on cards while
// tolerating IDs that are already truncated.
func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// collectContainerIssues turns the current container snapshot into findings.
// It deduplicates by container ID, skips denylisted containers, and reports
// whether the health input was available.
func collectContainerIssues(cards []containerCard, unhealthy []dockerContainer, unhealthyErr error, denylist []string) ([]hostIssue, []string, bool) {
	unique := make(map[string]containerCard, len(cards))
	for _, c := range cards {
		if c.ContainerID == "" {
			continue
		}
		if isInBlacklist(c.ContainerName, denylist) {
			continue
		}
		if _, ok := unique[c.ContainerID]; ok {
			continue
		}
		unique[c.ContainerID] = c
	}

	var issues []hostIssue
	for _, c := range unique {
		if c.UpdateAvailable {
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryUpdate, "container", c.ContainerID),
				Category:      categoryUpdate,
				Severity:      severityWarning,
				Title:         "Container image update available",
				Detail:        fmt.Sprintf("A newer digest is available for %s.", c.Image),
				Source:        "container",
				ContainerID:   c.ContainerID,
				ContainerName: c.ContainerName,
			})
		}
		if c.HasPublicBinding {
			ips := append([]string(nil), c.PublicBindingIPs...)
			sort.Strings(ips)
			detail := "Published on all interfaces."
			if len(ips) > 0 {
				detail = "Published on " + strings.Join(ips, ", ") + "."
			}
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryExposure, "container", c.ContainerID),
				Category:      categoryExposure,
				Severity:      severityWarning,
				Title:         "Container ports are publicly bound",
				Detail:        detail + " Bind published ports to 127.0.0.1 or a private address unless public access is intended.",
				Source:        "container",
				ContainerID:   c.ContainerID,
				ContainerName: c.ContainerName,
			})
		}
	}

	var warnings []string
	complete := true
	if unhealthyErr != nil {
		complete = false
		warnings = append(warnings, "container health: "+truncateForLog(unhealthyErr.Error(), 160))
	}

	for _, c := range unhealthy {
		name := containerName(c)
		if isInBlacklist(name, denylist) {
			continue
		}
		id := shortContainerID(c.ID)
		card, ok := unique[id]
		if !ok {
			// A stopped container can still carry a last-known health state;
			// only containers the dashboard currently shows are findings.
			continue
		}
		label := card.ContainerName
		if label == "" {
			label = name
		}
		issues = append(issues, hostIssue{
			ID:            newIssueID(categoryHealth, "container", id),
			Category:      categoryHealth,
			Severity:      severityError,
			Title:         "Container is unhealthy",
			Detail:        fmt.Sprintf("%s (%s) reports Docker health status \"unhealthy\".", label, card.Image),
			Source:        "container",
			ContainerID:   id,
			ContainerName: label,
		})
	}

	return issues, warnings, complete
}

// collectTraefikIssues turns the current Traefik state into findings. A missing
// Traefik container is guidance, not an issue; a 404/not-configured endpoint is
// informational, not a finding.
func collectTraefikIssues(found bool, data *traefikDashboardData, errStr string) ([]hostIssue, []string, bool) {
	if !found {
		return nil, nil, true
	}
	if data == nil {
		if strings.TrimSpace(errStr) == "" {
			return nil, nil, true
		}
		return []hostIssue{{
			ID:       newIssueID(categoryTraefikAPI, "traefik", "api"),
			Category: categoryTraefikAPI,
			Severity: severityError,
			Title:    "Traefik API is unreachable",
			Detail:   truncateForLog(errStr, 300),
			Source:   "traefik",
		}}, []string{"traefik: " + truncateForLog(errStr, 160)}, false
	}

	var issues []hostIssue
	var warnings []string
	complete := true

	if allEndpointFetchesFailed(data) {
		detail := strings.TrimSpace(errStr)
		if detail == "" {
			_, detail = traefikErrorSignature(data)
		}
		issues = append(issues, hostIssue{
			ID:       newIssueID(categoryTraefikAPI, "traefik", "api"),
			Category: categoryTraefikAPI,
			Severity: severityError,
			Title:    "Traefik API is unreachable",
			Detail:   truncateForLog(detail, 300),
			Source:   "traefik",
		})
		warnings = append(warnings, "traefik: API endpoint data unavailable")
		complete = false
	} else if len(data.EndpointErrors) > 0 {
		names := make([]string, 0, len(data.EndpointErrors))
		for name := range data.EndpointErrors {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			issues = append(issues, hostIssue{
				ID:       newIssueID(categoryTraefikAPI, "traefik", "endpoint/"+name),
				Category: categoryTraefikAPI,
				Severity: severityWarning,
				Title:    "Traefik API endpoint failed",
				Detail:   fmt.Sprintf("%s: %s", name, truncateForLog(data.EndpointErrors[name], 240)),
				Source:   "traefik",
			})
		}
		warnings = append(warnings, "traefik: some API endpoints returned errors")
		complete = false
	}

	if data.Version != nil && !data.VersionSupported {
		issues = append(issues, hostIssue{
			ID:       newIssueID(categoryTraefikVersion, "traefik", "version"),
			Category: categoryTraefikVersion,
			Severity: severityWarning,
			Title:    "Unsupported Traefik version",
			Detail: fmt.Sprintf("Detected Traefik %s (major %d); dumbdock supports major version %d.",
				truncateForLog(data.Version.Version, 40), data.MajorVersion, traefikSupportedMajorVersion),
			Source: "traefik",
		})
	}

	entities := []struct {
		kind   string
		key    string
		issues []traefikEntityStatus
	}{
		{"HTTP router", "http-router", routerStatuses(data.HTTPRouters)},
		{"TCP router", "tcp-router", routerStatuses(data.TCPRouters)},
		{"HTTP service", "http-service", serviceStatuses(data.HTTPServices)},
		{"TCP service", "tcp-service", serviceStatuses(data.TCPServices)},
		{"middleware", "middleware", middlewareStatuses(data.Middlewares)},
	}
	for _, group := range entities {
		for _, e := range group.issues {
			switch e.status {
			case "disabled", "warning", "error":
			default:
				continue
			}
			sev := severityWarning
			if e.status == "error" {
				sev = severityError
			}
			issues = append(issues, hostIssue{
				ID:       newIssueID(categoryTraefikEntity, "traefik", group.key+"/"+e.name),
				Category: categoryTraefikEntity,
				Severity: sev,
				Title:    fmt.Sprintf("Traefik %s is %s", group.kind, e.status),
				Detail:   fmt.Sprintf("%s %q reports status %q.", group.kind, truncateForLog(e.name, 120), e.status),
				Source:   "traefik",
			})
		}
	}

	for _, svc := range append(append([]traefikService{}, data.HTTPServices...), data.TCPServices...) {
		urls := make([]string, 0, len(svc.ServerStatus))
		for url := range svc.ServerStatus {
			urls = append(urls, url)
		}
		sort.Strings(urls)
		for _, url := range urls {
			if !strings.EqualFold(strings.TrimSpace(svc.ServerStatus[url]), "down") {
				continue
			}
			issues = append(issues, hostIssue{
				ID:       newIssueID(categoryTraefikBackend, "traefik", "service/"+svc.Name+"/"+url),
				Category: categoryTraefikBackend,
				Severity: severityError,
				Title:    "Traefik backend is down",
				Detail:   fmt.Sprintf("Service %q reports server %s as DOWN.", truncateForLog(svc.Name, 120), truncateForLog(url, 200)),
				Source:   "traefik",
			})
		}
	}

	return issues, warnings, complete
}

// traefikEntityStatus is the name/status pair extracted from one Traefik entity.
type traefikEntityStatus struct {
	name   string
	status string
}

func routerStatuses(list []traefikRouter) []traefikEntityStatus {
	out := make([]traefikEntityStatus, 0, len(list))
	for _, r := range list {
		out = append(out, traefikEntityStatus{name: r.Name, status: strings.ToLower(strings.TrimSpace(r.Status))})
	}
	return out
}

func serviceStatuses(list []traefikService) []traefikEntityStatus {
	out := make([]traefikEntityStatus, 0, len(list))
	for _, s := range list {
		out = append(out, traefikEntityStatus{name: s.Name, status: strings.ToLower(strings.TrimSpace(s.Status))})
	}
	return out
}

func middlewareStatuses(list []traefikMiddleware) []traefikEntityStatus {
	out := make([]traefikEntityStatus, 0, len(list))
	for _, m := range list {
		out = append(out, traefikEntityStatus{name: m.Name, status: strings.ToLower(strings.TrimSpace(m.Status))})
	}
	return out
}

// collectDockerVersionIssue compares the daemon version with the cached latest
// stable Moby release. An unavailable comparison is coverage metadata, never an
// active finding.
func collectDockerVersionIssue(daemon dockerVersionResponse, daemonErr error, latest string, lastChecked time.Time, checkErr string) ([]hostIssue, issueCoverageSource) {
	coverage := issueCoverageSource{}

	if daemonErr != nil {
		coverage.Error = "daemon version unavailable"
		return nil, coverage
	}
	if strings.TrimSpace(latest) == "" {
		coverage.Error = "release comparison unavailable"
		if strings.TrimSpace(checkErr) != "" {
			coverage.Error += ": " + truncateForLog(checkErr, 120)
		}
		return nil, coverage
	}
	coverage.Checked = true
	if !lastChecked.IsZero() {
		t := lastChecked
		coverage.LastCheckedAt = &t
	}

	daemonSemver, ok := parseDockerEngineVersion(daemon.Version)
	if !ok {
		coverage.Error = "daemon version unparseable"
		return nil, coverage
	}
	latestSemver, ok := parseDockerEngineVersion(latest)
	if !ok {
		coverage.Error = "latest release version unparseable"
		return nil, coverage
	}

	if compareDockerVersions(daemonSemver, latestSemver) >= 0 {
		return nil, coverage
	}

	return []hostIssue{{
		ID:       newIssueID(categoryDockerVersion, "docker", "engine"),
		Category: categoryDockerVersion,
		Severity: severityWarning,
		Title:    "Docker Engine may be out of date",
		Detail: fmt.Sprintf("Daemon version %s is older than the latest stable Moby release %s. Distribution packages may intentionally lag upstream; verify before upgrading.",
			truncateForLog(daemon.Version, 40), truncateForLog(latest, 40)),
		Source: "docker",
	}}, coverage
}
