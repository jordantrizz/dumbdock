package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Watchtower integration.
//
// Watchtower exposes two read-only surfaces and one mutating one. dumbdock
// reads /v1/metrics only (Prometheus text format) and never calls
// /v1/update, so a dashboard visit can never trigger an image update. The
// metrics API is experimental, needs an API token, and reports counters since
// the process started — there is no last-scan timestamp to report.

const (
	watchtowerRepository = "containrrr/watchtower"
	watchtowerAPIPort    = 8080
	watchtowerTimeout    = 8 * time.Second
	watchtowerMaxBody    = 1 << 20 // 1 MiB
)

// watchtowerConfig is the runtime configuration read from the container's own
// environment and command line. The token is held here only to build the
// Authorization header.
type watchtowerConfig struct {
	APIUpdate    bool
	APIMetrics   bool
	PeriodicPoll bool
	// Token is held only to build the Authorization header. It must never be
	// serialised into a payload, log line, or issue detail.
	Token        string `json:"-"`
	Schedule     string
	PollInterval string
	LabelEnable  bool
	Scope        string
	Cleanup      bool
}

// watchtowerMetrics is the parsed Prometheus payload.
type watchtowerMetrics struct {
	ContainersScanned float64
	ContainersUpdated float64
	ContainersFailed  float64
	ScansTotal        float64
	ScansSkipped      float64
	Present           map[string]bool
}

// watchtowerSnapshot is the vendor payload carried in integrationState.
type watchtowerSnapshot struct {
	Config      watchtowerConfig
	Metrics     watchtowerMetrics
	Monitored   int
	AllRunning  int
	LabelScoped bool
}

type watchtowerIntegration struct{}

func watchtowerIntegrationInstance() integration { return watchtowerIntegration{} }

func (w watchtowerIntegration) ID() string          { return "watchtower" }
func (w watchtowerIntegration) DisplayName() string { return "Watchtower" }
func (w watchtowerIntegration) Description() string {
	return "Reads Watchtower's metrics API: scan counters, last-scan results, and monitored containers."
}
func (w watchtowerIntegration) MinPollInterval() time.Duration { return integrationMinPollInterval }

// Detect matches the Watchtower image repository exactly.
func (w watchtowerIntegration) Detect(c dockerContainer) bool {
	return matchesImageRepository(c.Image, watchtowerRepository)
}

// Settings resolves the generic override layers, then falls back to the token
// the running Watchtower container was started with.
func (w watchtowerIntegration) Settings(inspect *dockerInspectResponse, cfg *overrideConfig) integrationSettings {
	var labels map[string]string
	if inspect != nil {
		labels = inspect.Config.Labels
	}
	s := resolveIntegrationSettings(w.ID(), cfg, labels)
	if s.Token == "" && inspect != nil {
		if v := envValue(inspect.Config.Env, "WATCHTOWER_HTTP_API_TOKEN"); v != "" {
			s.Token = v
			s.Sources["token"] = sourceAuto
		}
	}
	return s
}

// watchtowerConfigFrom reads the flags Watchtower itself was started with.
func watchtowerConfigFrom(inspect *dockerInspectResponse) watchtowerConfig {
	var c watchtowerConfig
	if inspect == nil {
		return c
	}
	env := inspect.Config.Env
	b := func(key string) bool {
		v, ok := parseBoolSetting(envValue(env, key))
		return ok && v
	}
	c.APIUpdate = b("WATCHTOWER_HTTP_API_UPDATE")
	c.APIMetrics = b("WATCHTOWER_HTTP_API_METRICS")
	c.PeriodicPoll = b("WATCHTOWER_HTTP_API_PERIODIC_POLLS")
	c.LabelEnable = b("WATCHTOWER_LABEL_ENABLE")
	c.Cleanup = b("WATCHTOWER_CLEANUP")
	c.Token = envValue(env, "WATCHTOWER_HTTP_API_TOKEN")
	c.Schedule = strings.TrimSpace(envValue(env, "WATCHTOWER_SCHEDULE"))
	c.PollInterval = strings.TrimSpace(envValue(env, "WATCHTOWER_POLL_INTERVAL"))
	c.Scope = strings.TrimSpace(envValue(env, "WATCHTOWER_SCOPE"))

	for _, arg := range inspect.Config.Cmd {
		switch {
		case arg == "--http-api-update":
			c.APIUpdate = true
		case arg == "--http-api-metrics":
			c.APIMetrics = true
		case arg == "--http-api-periodic-polls":
			c.PeriodicPoll = true
		case arg == "--label-enable":
			c.LabelEnable = true
		case arg == "--cleanup":
			c.Cleanup = true
		case strings.HasPrefix(arg, "--schedule="):
			if v := strings.TrimSpace(strings.TrimPrefix(arg, "--schedule=")); v != "" {
				c.Schedule = v
			}
		case strings.HasPrefix(arg, "--interval="):
			if v := strings.TrimSpace(strings.TrimPrefix(arg, "--interval=")); v != "" {
				c.PollInterval = v
			}
		case strings.HasPrefix(arg, "--scope="):
			if v := strings.TrimSpace(strings.TrimPrefix(arg, "--scope=")); v != "" {
				c.Scope = v
			}
		}
	}
	return c
}

// watchtowerMonitored counts the containers Watchtower is expected to watch.
// Label-filtered mode is the only mode with an unambiguous answer, so the
// unlabelled result is reported as "all running" instead of a guess.
func watchtowerMonitored(containers []dockerContainer, cfg watchtowerConfig, self string) (count, allRunning int, labelScoped bool) {
	for _, c := range containers {
		if c.State != "running" || containerName(c) == self {
			continue
		}
		allRunning++
		if !watchtowerEnabledByLabel(c, cfg.Scope) {
			continue
		}
		labelScoped = true
		count++
	}
	if !labelScoped {
		return 0, allRunning, false
	}
	return count, allRunning, true
}

// watchtowerEnabledByLabel applies the opt-in and scope labels.
func watchtowerEnabledByLabel(c dockerContainer, scope string) bool {
	if !strings.EqualFold(strings.TrimSpace(c.Labels["com.centurylinklabs.watchtower.enable"]), "true") {
		return false
	}
	if scope != "" {
		return strings.TrimSpace(c.Labels["com.centurylinklabs.watchtower.scope"]) == scope
	}
	return true
}

// parseWatchtowerMetrics reads the Prometheus text exposition format that
// Watchtower serves on /v1/metrics. Unknown lines are ignored; a malformed
// value is skipped rather than failing the whole poll.
func parseWatchtowerMetrics(body string) watchtowerMetrics {
	m := watchtowerMetrics{Present: map[string]bool{}}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.Index(name, "{"); i >= 0 {
			name = name[:i]
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil || value != value { // NaN fails the self-comparison
			continue
		}
		switch name {
		case "watchtower_containers_scanned":
			m.ContainersScanned = value
		case "watchtower_containers_updated":
			m.ContainersUpdated = value
		case "watchtower_containers_failed":
			m.ContainersFailed = value
		case "watchtower_scans_total":
			m.ScansTotal = value
		case "watchtower_scans_skipped":
			m.ScansSkipped = value
		default:
			continue
		}
		m.Present[name] = true
	}
	return m
}

// fetchWatchtowerMetrics performs the bounded, authenticated metrics read.
func fetchWatchtowerMetrics(client *http.Client, baseURL, authHeader string) (watchtowerMetrics, error) {
	if client == nil {
		client = &http.Client{Timeout: watchtowerTimeout}
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/metrics", nil)
	if err != nil {
		return watchtowerMetrics{}, fmt.Errorf("create request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return watchtowerMetrics{}, fmt.Errorf("GET /v1/metrics: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return watchtowerMetrics{}, fmt.Errorf("GET /v1/metrics returned status %d (check the API token)", resp.StatusCode)
	case http.StatusNotFound:
		return watchtowerMetrics{}, fmt.Errorf("GET /v1/metrics returned 404 (start Watchtower with --http-api-metrics)")
	default:
		return watchtowerMetrics{}, fmt.Errorf("GET /v1/metrics returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, watchtowerMaxBody))
	if err != nil {
		return watchtowerMetrics{}, fmt.Errorf("read /v1/metrics: %w", err)
	}
	return parseWatchtowerMetrics(string(body)), nil
}

// Collect polls the metrics endpoint when it is enabled and reports the
// configuration either way. The mutating /v1/update endpoint is never called.
func (w watchtowerIntegration) Collect(rt integrationRuntime, target integrationTarget) integrationState {
	st := target.Base
	gate := rt.gate
	if gate == nil {
		gate = &integrationGate{id: w.ID(), displayName: w.DisplayName()}
	}

	cfg := watchtowerConfigFrom(target.Inspect)
	if cfg.Token == "" {
		cfg.Token = target.Settings.Token
	}
	monitored, allRunning, labelScoped := watchtowerMonitored(rt.containers, cfg, st.ContainerName)
	snap := &watchtowerSnapshot{Config: cfg, Monitored: monitored, AllRunning: allRunning, LabelScoped: labelScoped}
	st.VendorData = snap

	st.Sections = append(st.Sections, watchtowerConfigSection(cfg))
	if labelScoped {
		st.Stats = append(st.Stats, integrationStat{Label: "Monitored containers", Value: strconv.Itoa(monitored), Hint: "watchtower.enable=true"})
	} else {
		st.Stats = append(st.Stats, integrationStat{Label: "Running containers", Value: strconv.Itoa(allRunning), Hint: "label filtering is off"})
	}

	if !cfg.APIMetrics {
		if cfg.APIUpdate {
			st.Warnings = append(st.Warnings, "Watchtower has the update API but not the metrics API; add --http-api-metrics to read scan results")
		} else {
			st.Warnings = append(st.Warnings,
				"Watchtower HTTP API is not enabled; start it with --http-api-metrics (and WATCHTOWER_HTTP_API_TOKEN) to read scan results")
		}
		st.Stats = sortIntegrationStats(st.Stats)
		return st
	}

	apiURL := target.Settings.URL
	urlSource := target.Settings.source("url")
	if apiURL == "" {
		apiURL = containerAPIURL(target.Inspect, watchtowerAPIPort)
		urlSource = sourceAuto
	}
	if apiURL == "" {
		recordPollFailure(gate, &st, fmt.Sprintf("resolve API URL: no override and no reachable address for %s on port %d", watchtowerRepository, watchtowerAPIPort))
		st.Stats = sortIntegrationStats(st.Stats)
		return st
	}
	st.URL = apiURL
	st.URLSource = urlSource

	authHeader := integrationAuthHeader(target.Settings)
	st.AuthConfigured = authHeader != ""

	client := &http.Client{Timeout: watchtowerTimeout}
	metrics, err := fetchWatchtowerMetrics(client, apiURL, authHeader)
	if err != nil {
		recordPollFailure(gate, &st, err.Error())
		st.Stats = sortIntegrationStats(st.Stats)
		return st
	}
	gate.pass()
	snap.Metrics = metrics

	st.Stats = append(st.Stats,
		integrationStat{Label: "Scanned (last scan)", Value: formatMetric(metrics, "watchtower_containers_scanned")},
		integrationStat{Label: "Updated (last scan)", Value: formatMetric(metrics, "watchtower_containers_updated")},
		integrationStat{Label: "Failed (last scan)", Value: formatMetric(metrics, "watchtower_containers_failed")},
		integrationStat{Label: "Scans total", Value: formatMetric(metrics, "watchtower_scans_total")},
		integrationStat{Label: "Scans skipped", Value: formatMetric(metrics, "watchtower_scans_skipped")},
	)
	st.Stats = sortIntegrationStats(st.Stats)

	if metrics.ContainersFailed > 0 {
		st.Warnings = append(st.Warnings,
			fmt.Sprintf("%.0f container update(s) failed in the last Watchtower scan", metrics.ContainersFailed))
	}
	return st
}

// watchtowerConfigSection renders the configuration table. The token is
// reported as configured/unset and never echoed.
func watchtowerConfigSection(cfg watchtowerConfig) integrationSection {
	yesNo := boolYesNo
	token := "not configured"
	if cfg.Token != "" {
		token = "configured"
	}
	schedule := cfg.Schedule
	if schedule == "" {
		schedule = "not set"
	}
	interval := cfg.PollInterval
	if interval == "" {
		interval = "not set"
	}
	scope := cfg.Scope
	if scope == "" {
		scope = "none"
	}
	return integrationSection{
		Title:   "Configuration",
		Columns: []string{"Setting", "Value"},
		Rows: [][]string{
			{"HTTP API (update)", yesNo(cfg.APIUpdate)},
			{"HTTP API (metrics)", yesNo(cfg.APIMetrics)},
			{"Periodic polls", yesNo(cfg.PeriodicPoll)},
			{"Label filtering", yesNo(cfg.LabelEnable)},
			{"Cleanup old images", yesNo(cfg.Cleanup)},
			{"Schedule", schedule},
			{"Poll interval", interval},
			{"Scope", scope},
			{"API token", token},
		},
	}
}

// formatMetric renders one parsed gauge, or "n/a" when Watchtower did not
// report it.
func formatMetric(m watchtowerMetrics, name string) string {
	if !m.Present[name] {
		return "n/a"
	}
	switch name {
	case "watchtower_containers_scanned":
		return strconv.FormatFloat(m.ContainersScanned, 'f', -1, 64)
	case "watchtower_containers_updated":
		return strconv.FormatFloat(m.ContainersUpdated, 'f', -1, 64)
	case "watchtower_containers_failed":
		return strconv.FormatFloat(m.ContainersFailed, 'f', -1, 64)
	case "watchtower_scans_total":
		return strconv.FormatFloat(m.ScansTotal, 'f', -1, 64)
	case "watchtower_scans_skipped":
		return strconv.FormatFloat(m.ScansSkipped, 'f', -1, 64)
	}
	return "n/a"
}

// Issues reports Watchtower's findings. The mutating API is never involved.
func (w watchtowerIntegration) Issues(st integrationState) ([]hostIssue, []string, bool) {
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
			detail = fmt.Sprintf("Watchtower API disabled after %d attempts", limit)
		}
		return []hostIssue{{
				ID:            newIssueID(categoryIntegrationAPI, "watchtower", "api"),
				Category:      categoryIntegrationAPI,
				Severity:      severityError,
				Title:         "Watchtower API unavailable (disabled until restart)",
				Detail:        truncateForLog(detail, 300),
				Source:        "watchtower",
				ContainerName: st.ContainerName,
			}},
			[]string{fmt.Sprintf("watchtower: API disabled after %d attempts (restart dumbdock to retry)", limit)},
			true
	}
	if !st.Detected && !st.Forced {
		return nil, nil, true
	}

	var issues []hostIssue
	var warnings []string
	complete := true

	if strings.TrimSpace(st.Error) != "" {
		issues = append(issues, hostIssue{
			ID:            newIssueID(categoryIntegrationAPI, "watchtower", "api"),
			Category:      categoryIntegrationAPI,
			Severity:      severityError,
			Title:         "Watchtower API is unreachable",
			Detail:        truncateForLog(st.Error, 300),
			Source:        "watchtower",
			ContainerName: st.ContainerName,
		})
		warnings = append(warnings, "watchtower: status unavailable")
		complete = false
	}

	snap, _ := st.VendorData.(*watchtowerSnapshot)
	if snap != nil {
		if snap.Config.APIMetrics && snap.Metrics.Present["watchtower_containers_failed"] && snap.Metrics.ContainersFailed > 0 {
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryIntegrationStatus, "watchtower", "failed-updates"),
				Category:      categoryIntegrationStatus,
				Severity:      severityWarning,
				Title:         "Watchtower update failures",
				Detail:        fmt.Sprintf("%.0f container update(s) failed in the last scan.", snap.Metrics.ContainersFailed),
				Source:        "watchtower",
				ContainerName: st.ContainerName,
			})
		}
		if !snap.Config.APIMetrics {
			warnings = append(warnings, "watchtower: HTTP API is not enabled (start it with --http-api-metrics to read scan results)")
		}
	}

	return issues, warnings, complete
}

// registerWatchtower wires the integration into the registry at init time.
func init() {
	registerIntegration(watchtowerIntegrationInstance())
}
