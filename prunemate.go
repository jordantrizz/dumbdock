package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PruneMate integration.
//
// PruneMate is a scheduled Docker cleanup helper with a Flask web UI. It
// publishes all-time cleanup statistics on /api/stats for Homepage widgets
// (camelCase) and the raw stats file on /stats (snake_case). Both are read-only
// and exempt from its login, so dumbdock never needs credentials — the Basic
// override exists only for a reverse proxy that adds auth in front.
//
// PruneMate keeps its schedule and per-resource prune flags inside its own
// /config volume. Reading those would need docker exec, which dumbdock does not
// do, so they are documented as a known limit rather than guessed.

const (
	prunemateRepository = "anoniemerd/prunemate"
	prunemateAPIPort    = 8080
	prunemateTimeout    = 8 * time.Second
	prunemateMaxBody    = 1 << 20 // 1 MiB
)

// prunemateStaleAfter is the only staleness rule: a monthly schedule plus
// grace. It is deliberately a constant, not a setting — one more knob buys
// nothing and every new setting has to be validated, logged, and documented.
const prunemateStaleAfter = 35 * 24 * time.Hour

// prunemateStats is the shared shape both payloads are mapped onto.
type prunemateStats struct {
	PruneRuns           int    `json:"pruneRuns"`
	ContainersDeleted   int    `json:"containersDeleted"`
	ImagesDeleted       int    `json:"imagesDeleted"`
	NetworksDeleted     int    `json:"networksDeleted"`
	VolumesDeleted      int    `json:"volumesDeleted"`
	BuildCacheDeleted   int    `json:"buildCacheDeleted"`
	SpaceReclaimed      int64  `json:"spaceReclaimed"`
	SpaceReclaimedHuman string `json:"spaceReclaimedHuman,omitempty"`
	FirstRun            string `json:"firstRun,omitempty"`
	LastRun             string `json:"lastRun,omitempty"`
	LastRunText         string `json:"lastRunText,omitempty"`
}

// prunemateSnapshot is the vendor payload carried in integrationState.
type prunemateSnapshot struct {
	Stats      prunemateStats
	Endpoint   string
	UseAuth    bool
	LastRun    time.Time
	HasLastRun bool
}

type prunemateIntegration struct{}

func prunemateIntegrationInstance() integration { return prunemateIntegration{} }

func (p prunemateIntegration) ID() string          { return "prunemate" }
func (p prunemateIntegration) DisplayName() string { return "PruneMate" }
func (p prunemateIntegration) Description() string {
	return "Reads PruneMate's public statistics: prune runs, reclaimed space, and the last cleanup."
}
func (p prunemateIntegration) MinPollInterval() time.Duration { return integrationMinPollInterval }

// Detect matches the PruneMate image repository exactly.
func (p prunemateIntegration) Detect(c dockerContainer) bool {
	return matchesImageRepository(c.Image, prunemateRepository)
}

// Settings is the generic override chain; PruneMate needs no legacy names.
func (p prunemateIntegration) Settings(inspect *dockerInspectResponse, cfg *overrideConfig) integrationSettings {
	var labels map[string]string
	if inspect != nil {
		labels = inspect.Config.Labels
	}
	return resolveIntegrationSettings(p.ID(), cfg, labels)
}

// prunemateAPIPayload decodes /api/stats (camelCase, Homepage shape).
type prunemateAPIPayload struct {
	PruneRuns           int    `json:"pruneRuns"`
	ContainersDeleted   int    `json:"containersDeleted"`
	ImagesDeleted       int    `json:"imagesDeleted"`
	NetworksDeleted     int    `json:"networksDeleted"`
	VolumesDeleted      int    `json:"volumesDeleted"`
	BuildCacheDeleted   int    `json:"buildCacheDeleted"`
	SpaceReclaimed      int64  `json:"spaceReclaimed"`
	SpaceReclaimedHuman string `json:"spaceReclaimedHuman"`
	FirstRun            string `json:"firstRun"`
	LastRun             string `json:"lastRun"`
	LastRunText         string `json:"lastRunText"`
}

// prunemateRawPayload decodes /stats (the raw stats.json, snake_case).
type prunemateRawPayload struct {
	PruneRuns           int    `json:"prune_runs"`
	ContainersDeleted   int    `json:"containers_deleted"`
	ImagesDeleted       int    `json:"images_deleted"`
	NetworksDeleted     int    `json:"networks_deleted"`
	VolumesDeleted      int    `json:"volumes_deleted"`
	BuildCacheDeleted   int    `json:"build_cache_deleted"`
	TotalSpaceReclaimed int64  `json:"total_space_reclaimed"`
	FirstRun            string `json:"first_run"`
	LastRun             string `json:"last_run"`
}

// fetchPruneMateStats reads the statistics, preferring the documented
// /api/stats endpoint and falling back once to /stats on older builds.
func fetchPruneMateStats(client *http.Client, baseURL, authHeader string) (prunemateStats, string, error) {
	if client == nil {
		client = &http.Client{Timeout: prunemateTimeout}
	}
	base := strings.TrimRight(baseURL, "/")

	body, status, err := prunemateGet(client, base+"/api/stats", authHeader)
	if err != nil {
		return prunemateStats{}, "/api/stats", err
	}
	if status == http.StatusOK {
		var payload prunemateAPIPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return prunemateStats{}, "/api/stats", fmt.Errorf("decode /api/stats: %w", err)
		}
		return prunemateStats{
			PruneRuns:           payload.PruneRuns,
			ContainersDeleted:   payload.ContainersDeleted,
			ImagesDeleted:       payload.ImagesDeleted,
			NetworksDeleted:     payload.NetworksDeleted,
			VolumesDeleted:      payload.VolumesDeleted,
			BuildCacheDeleted:   payload.BuildCacheDeleted,
			SpaceReclaimed:      payload.SpaceReclaimed,
			SpaceReclaimedHuman: payload.SpaceReclaimedHuman,
			FirstRun:            payload.FirstRun,
			LastRun:             payload.LastRun,
			LastRunText:         payload.LastRunText,
		}, "/api/stats", nil
	}
	if status != http.StatusNotFound {
		return prunemateStats{}, "/api/stats", prunemateStatusError("/api/stats", status)
	}

	// Older builds have no /api/stats; the raw file endpoint carries the same
	// counters under snake_case keys.
	body, status, err = prunemateGet(client, base+"/stats", authHeader)
	if err != nil {
		return prunemateStats{}, "/stats", err
	}
	if status != http.StatusOK {
		return prunemateStats{}, "/stats", prunemateStatusError("/stats", status)
	}
	var raw prunemateRawPayload
	if err := json.Unmarshal(body, &raw); err != nil {
		return prunemateStats{}, "/stats", fmt.Errorf("decode /stats: %w", err)
	}
	return prunemateStats{
		PruneRuns:         raw.PruneRuns,
		ContainersDeleted: raw.ContainersDeleted,
		ImagesDeleted:     raw.ImagesDeleted,
		NetworksDeleted:   raw.NetworksDeleted,
		VolumesDeleted:    raw.VolumesDeleted,
		BuildCacheDeleted: raw.BuildCacheDeleted,
		SpaceReclaimed:    raw.TotalSpaceReclaimed,
		FirstRun:          raw.FirstRun,
		LastRun:           raw.LastRun,
	}, "/stats", nil
}

// prunemateGet performs one bounded GET.
func prunemateGet(client *http.Client, url, authHeader string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, prunemateMaxBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read %s: %w", url, err)
	}
	return body, resp.StatusCode, nil
}

// prunemateStatusError turns a status code into an operator-facing message.
func prunemateStatusError(path string, status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("GET %s returned status %d (check the credentials)", path, status)
	default:
		return fmt.Errorf("GET %s returned status %d", path, status)
	}
}

// parsePruneMateLastRun reads the ISO-8601 timestamp PruneMate writes. A
// missing or malformed value is reported as "no timestamp", never a failed poll.
func parsePruneMateLastRun(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// humanBytes renders a reclaimed-byte count when PruneMate did not supply its
// own human string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Collect reads the statistics endpoint and renders the card.
func (p prunemateIntegration) Collect(rt integrationRuntime, target integrationTarget) integrationState {
	st := target.Base
	gate := rt.gate
	if gate == nil {
		gate = &integrationGate{id: p.ID(), displayName: p.DisplayName()}
	}

	apiURL := target.Settings.URL
	urlSource := target.Settings.source("url")
	if apiURL == "" {
		apiURL = containerAPIURL(target.Inspect, prunemateAPIPort)
		urlSource = sourceAuto
	}
	if apiURL == "" {
		recordPollFailure(gate, &st, fmt.Sprintf("resolve API URL: no override and no reachable address for %s on port %d", prunemateRepository, prunemateAPIPort))
		return st
	}
	st.URL = apiURL
	st.URLSource = urlSource

	authHeader := integrationAuthHeader(target.Settings)
	st.AuthConfigured = authHeader != ""

	stats, endpoint, err := fetchPruneMateStats(&http.Client{Timeout: prunemateTimeout}, apiURL, authHeader)
	if err != nil {
		recordPollFailure(gate, &st, err.Error())
		return st
	}
	gate.pass()

	lastRun, hasLastRun := parsePruneMateLastRun(stats.LastRun)
	snap := &prunemateSnapshot{
		Stats:      stats,
		Endpoint:   endpoint,
		UseAuth:    authHeader != "",
		LastRun:    lastRun,
		HasLastRun: hasLastRun,
	}
	st.VendorData = snap

	space := stats.SpaceReclaimedHuman
	if space == "" {
		space = humanBytes(stats.SpaceReclaimed)
	}
	lastRunText := stats.LastRunText
	if lastRunText == "" {
		if hasLastRun {
			lastRunText = relativeAge(rt.now, lastRun)
		} else {
			lastRunText = "never"
		}
	}

	st.Stats = []integrationStat{
		{Label: "Prune runs", Value: strconv.Itoa(stats.PruneRuns)},
		{Label: "Space reclaimed", Value: space},
		{Label: "Last run", Value: lastRunText},
		{Label: "Containers deleted", Value: strconv.Itoa(stats.ContainersDeleted)},
		{Label: "Images deleted", Value: strconv.Itoa(stats.ImagesDeleted)},
		{Label: "Volumes deleted", Value: strconv.Itoa(stats.VolumesDeleted)},
		{Label: "Networks deleted", Value: strconv.Itoa(stats.NetworksDeleted)},
		{Label: "Build cache", Value: strconv.Itoa(stats.BuildCacheDeleted)},
	}
	st.Sections = append(st.Sections, integrationSection{
		Title:   "Read endpoint",
		Columns: []string{"Field", "Value"},
		Rows: [][]string{
			{"Endpoint", endpoint},
			{"Credentials", boolYesNo(authHeader != "")},
			{"First run", valueOrUnknown(stats.FirstRun)},
			{"Last run", valueOrUnknown(stats.LastRun)},
		},
	})
	return st
}

// relativeAge renders a coarse age for a timestamp.
func relativeAge(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func valueOrUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "unknown"
	}
	return v
}

// Issues reports PruneMate's findings: an unreachable API, a cleanup that has
// never run, and a cleanup older than the staleness window.
func (p prunemateIntegration) Issues(st integrationState) ([]hostIssue, []string, bool) {
	if !st.Enabled {
		return nil, nil, true
	}
	if st.Disabled {
		limit := st.AttemptLimit
		if limit <= 0 {
			limit = integrationAttemptLimit
		}
		detail := strings.TrimSpace(st.DisabledReason)
		if detail == "" {
			detail = fmt.Sprintf("PruneMate API disabled after %d attempts", limit)
		}
		return []hostIssue{{
				ID:            newIssueID(categoryIntegrationAPI, "prunemate", "api"),
				Category:      categoryIntegrationAPI,
				Severity:      severityError,
				Title:         "PruneMate API unavailable (disabled until restart)",
				Detail:        truncateForLog(detail, 300),
				Source:        "prunemate",
				ContainerName: st.ContainerName,
			}},
			[]string{fmt.Sprintf("prunemate: API disabled after %d attempts (restart dumbdock to retry)", limit)},
			true
	}
	if !st.Detected && !st.Forced {
		return nil, nil, true
	}

	if strings.TrimSpace(st.Error) != "" {
		return []hostIssue{{
				ID:            newIssueID(categoryIntegrationAPI, "prunemate", "api"),
				Category:      categoryIntegrationAPI,
				Severity:      severityError,
				Title:         "PruneMate API is unreachable",
				Detail:        truncateForLog(st.Error, 300),
				Source:        "prunemate",
				ContainerName: st.ContainerName,
			}},
			[]string{"prunemate: status unavailable"},
			false
	}

	snap, _ := st.VendorData.(*prunemateSnapshot)
	if snap == nil {
		return nil, nil, true
	}
	if snap.Stats.PruneRuns == 0 {
		return []hostIssue{{
			ID:            newIssueID(categoryIntegrationStatus, "prunemate", "never-run"),
			Category:      categoryIntegrationStatus,
			Severity:      severityWarning,
			Title:         "PruneMate has never completed a prune",
			Detail:        "PruneMate reports zero completed cleanup runs. Check its schedule in the PruneMate UI.",
			Source:        "prunemate",
			ContainerName: st.ContainerName,
		}}, nil, true
	}
	if snap.HasLastRun && !st.LastChecked.IsZero() {
		if age := st.LastChecked.Sub(snap.LastRun); age > prunemateStaleAfter {
			return []hostIssue{{
				ID:            newIssueID(categoryIntegrationStatus, "prunemate", "stale"),
				Category:      categoryIntegrationStatus,
				Severity:      severityWarning,
				Title:         "PruneMate has not pruned recently",
				Detail:        fmt.Sprintf("The last completed prune was %d day(s) ago (threshold %d days).", int(age.Hours()/24), int(prunemateStaleAfter.Hours()/24)),
				Source:        "prunemate",
				ContainerName: st.ContainerName,
			}}, nil, true
		}
	}
	return nil, nil, true
}

// registerPruneMate wires the integration into the registry at init time.
func init() {
	registerIntegration(prunemateIntegrationInstance())
}
