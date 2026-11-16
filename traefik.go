package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Docker container inspection (used to resolve the Traefik API URL)
// ---------------------------------------------------------------------------

type dockerInspectResponse struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
		Cmd    []string          `json:"Cmd"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// ---------------------------------------------------------------------------
// Traefik API response types
// ---------------------------------------------------------------------------

// traefikDashboardData is the top-level response our /api/traefik endpoint
// returns.  Each field holds the raw decoded data from the corresponding
// Traefik API endpoint.  Endpoints that returned a non-200 or were skipped
// will have a nil slice/pointer and a non-empty error string.
type traefikDashboardData struct {
	APIURL         string                  `json:"apiUrl"`
	AuthConfigured bool                    `json:"authConfigured"`
	Version        *traefikVersion         `json:"version,omitempty"`
	Overview       *traefikOverview        `json:"overview,omitempty"`
	Entrypoints    []traefikEntrypoint     `json:"entrypoints,omitempty"`
	HTTPRouters    []traefikRouter         `json:"httpRouters,omitempty"`
	HTTPServices   []traefikService        `json:"httpServices,omitempty"`
	Middlewares    []traefikMiddleware     `json:"middlewares,omitempty"`
	TCPRouters     []traefikRouter         `json:"tcpRouters,omitempty"`
	TCPServices    []traefikService        `json:"tcpServices,omitempty"`
	TLSCerts       []traefikTLSCertificate `json:"tlsCerts,omitempty"`
	EndpointErrors map[string]string       `json:"endpointErrors,omitempty"`
	// NotConfigured lists optional endpoints that returned 404 (e.g. no TLS
	// certificates configured). These are informational, not failures.
	NotConfigured []string `json:"notConfigured,omitempty"`
	// MajorVersion is the parsed Traefik major version (0 when unavailable),
	// and VersionSupported reports whether it is a version dumbdock supports
	// (currently only major version 3).
	MajorVersion     int  `json:"majorVersion,omitempty"`
	VersionSupported bool `json:"versionSupported"`
}

type traefikVersion struct {
	Version   string `json:"Version"`
	Codename  string `json:"Codename"`
	StartDate string `json:"StartDate"`
	Goversion string `json:"Goversion"`
}

// traefikSupportedMajorVersion is the only Traefik major version whose API
// shape dumbdock fully supports.
const traefikSupportedMajorVersion = 3

// parseTraefikMajorVersion extracts the major version from a Traefik version
// string. It tolerates a leading "v"/"V" (e.g. "v3.7.13") and returns
// (0, false) when no leading integer is present.
func parseTraefikMajorVersion(s string) (int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

// traefikOverviewCount is a single value from /api/overview. Traefik v2
// returned a plain integer for each count, while v3 returns an object such as
// {"total": N, "warnings": 0, "errors": 0, ...}. This type accepts both and
// exposes the meaningful total.
type traefikOverviewCount int

func (c *traefikOverviewCount) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*c = 0
		return nil
	}
	// v2 (and some v3 builds): a bare number.
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*c = traefikOverviewCount(n)
		return nil
	}
	// v3: an object with a "total" field (fall back to 0 when absent).
	var obj struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*c = traefikOverviewCount(obj.Total)
	return nil
}

func (c traefikOverviewCount) Int() int { return int(c) }

type traefikOverview struct {
	HTTP struct {
		Routers     traefikOverviewCount `json:"routers"`
		Services    traefikOverviewCount `json:"services"`
		Middlewares traefikOverviewCount `json:"middlewares"`
	} `json:"http"`
	TCP struct {
		Routers     traefikOverviewCount `json:"routers"`
		Services    traefikOverviewCount `json:"services"`
		Middlewares traefikOverviewCount `json:"middlewares"`
	} `json:"tcp"`
}

type traefikEntrypoint struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type traefikRouter struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Rule        string   `json:"rule"`
	Service     string   `json:"service"`
	EntryPoints []string `json:"entryPoints"`
	Middlewares []string `json:"middlewares,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	// TLS is a free-form object that we pass through as-is.
	TLS json.RawMessage `json:"tls,omitempty"`
}

type traefikService struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Type         string            `json:"type"`
	ServerStatus map[string]string `json:"serverStatus,omitempty"`
	// loadBalancer and other fields are passed through as-is.
	LoadBalancer json.RawMessage `json:"loadBalancer,omitempty"`
}

type traefikMiddleware struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type"`
}

type traefikTLSCertificate struct {
	Name    string `json:"name"`
	Domains []struct {
		Main string   `json:"main"`
		SANS []string `json:"sans,omitempty"`
	} `json:"domains,omitempty"`
	NotAfter string `json:"notAfter"`
	Subject  struct {
		CommonName string `json:"commonName"`
	} `json:"subject,omitempty"`
	Stores []string `json:"stores,omitempty"`
}

// ---------------------------------------------------------------------------
// Traefik integration (the state itself lives in the integration store)
// ---------------------------------------------------------------------------

// traefikGate is the shared per-integration gate under its Traefik name, so the
// documented policy and the existing tests keep reading one type.
type traefikGate = integrationGate

// Post-boot log dedup. Collect runs from the refresh goroutine only; the mutex
// keeps direct test calls race-free.
var (
	traefikLogMu         sync.Mutex
	traefikLastErrSig    string
	traefikLastReachable bool
)

// resetTraefikState clears the published state, the log-dedup state, and the
// boot gate. It exists for tests so each case starts from a fresh
// process-like state; it is never called from production code.
func resetTraefikState() {
	integrationStoreState.reset("traefik")
	traefikLogMu.Lock()
	traefikLastErrSig = ""
	traefikLastReachable = false
	traefikLogMu.Unlock()
}

// traefikEndpointNames is the fixed set of Traefik API endpoints fetched by
// fetchTraefikDashboard.  It is used to detect when every endpoint failed.
var traefikEndpointNames = []string{
	"version",
	"overview",
	"entrypoints",
	"httpRouters",
	"httpServices",
	"middlewares",
	"tcpRouters",
	"tcpServices",
	"tlsCerts",
}

// traefikErrorSignature returns a stable signature of the endpoint errors in
// data (empty when there are none) plus the first error message in sorted
// endpoint order.  The signature is deterministic across map iteration order
// so it can be compared between refreshes for log deduplication.
func traefikErrorSignature(data *traefikDashboardData) (string, string) {
	if data == nil || len(data.EndpointErrors) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(data.EndpointErrors))
	for n := range data.EndpointErrors {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	firstErr := ""
	for i, n := range names {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(n)
		b.WriteByte('=')
		b.WriteString(data.EndpointErrors[n])
	}
	if len(names) > 0 {
		firstErr = data.EndpointErrors[names[0]]
	}
	return b.String(), firstErr
}

// allEndpointFetchesFailed reports whether every Traefik API endpoint failed,
// i.e. the API is entirely unreachable rather than just missing a few
// endpoints (e.g. no TLS certificates configured).
func allEndpointFetchesFailed(data *traefikDashboardData) bool {
	if data == nil {
		return false
	}
	return len(data.EndpointErrors) >= len(traefikEndpointNames)
}

// traefikLogSignature combines the endpoint-error signature with the detected
// version state so that log deduplication treats an unsupported version as a
// distinct, once-per-change event. It is empty when there is nothing to log
// (all healthy, supported version).
func traefikLogSignature(data *traefikDashboardData, allFailed bool) string {
	errSig, _ := traefikErrorSignature(data)
	if allFailed || data == nil || data.Version == nil {
		return errSig
	}
	var marker string
	if data.VersionSupported {
		marker = fmt.Sprintf("version=major%d(supported)", data.MajorVersion)
	} else {
		marker = fmt.Sprintf("version=%q(unsupported)", data.Version.Version)
	}
	if errSig == "" {
		return marker
	}
	return marker + "|" + errSig
}

// traefikSnapshot is the consistent view of the Traefik state that the HTTP
// handler and the issues collector consume. It is the only shape callers see,
// so no caller reads the store directly.
type traefikSnapshot struct {
	Found          bool
	APIURL         string
	Data           *traefikDashboardData
	Err            string
	Disabled       bool
	DisabledReason string
	Attempts       int
	AttemptLimit   int
}

// snapshotFromIntegrationState converts the published integration state into
// the Traefik-shaped snapshot.
func snapshotFromIntegrationState(st integrationState) traefikSnapshot {
	snap := traefikSnapshot{
		Found:          st.Detected,
		APIURL:         st.URL,
		Err:            st.Error,
		Disabled:       st.Disabled,
		DisabledReason: st.DisabledReason,
		Attempts:       st.Attempts,
		AttemptLimit:   st.AttemptLimit,
	}
	if data, ok := st.VendorData.(*traefikDashboardData); ok {
		snap.Data = data
	}
	return snap
}

// getTraefikState returns a snapshot of the current Traefik state.
func getTraefikState() traefikSnapshot {
	st, ok := integrationStoreState.get("traefik")
	if !ok {
		return traefikSnapshot{AttemptLimit: integrationAttemptLimit}
	}
	return snapshotFromIntegrationState(st)
}

// traefikAPIResponse builds the JSON payload of GET /api/traefik from a
// snapshot. It lives here so the handler stays a cache read and the payload
// shape is testable without an HTTP server.
func traefikAPIResponse(snap traefikSnapshot) map[string]interface{} {
	return map[string]interface{}{
		"found":          snap.Found,
		"apiUrl":         snap.APIURL,
		"data":           snap.Data,
		"error":          snap.Err,
		"disabled":       snap.Disabled,
		"disabledReason": snap.DisabledReason,
		"attempts":       snap.Attempts,
		"attemptLimit":   snap.AttemptLimit,
	}
}

// ---------------------------------------------------------------------------
// Detection
// ---------------------------------------------------------------------------

// findTraefikContainer scans the given container list for the running Traefik
// container using the shared exact-repository matcher. A substring match would
// adopt traefik-forward-auth, which is why the matcher is exact.
func findTraefikContainer(containers []dockerContainer) *dockerContainer {
	return detectIntegrationContainer(traefikIntegrationInstance(), containers, integrationSettings{Enabled: true})
}

// ---------------------------------------------------------------------------
// API URL resolution (auto-detection only; overrides are resolved first)
// ---------------------------------------------------------------------------

// resolveTraefikAPIURL resolves the Traefik API base URL from the container
// itself, after every override layer has been consulted:
//
//  1. The container's IP address on its first Docker network, plus the API port.
//  2. The first published port (preferring the API port) on 127.0.0.1.
//
// The dumbdock.traefik.api label and TRAEFIK_API_URL are handled by the
// override chain (Settings), so they are deliberately absent here.
func resolveTraefikAPIURL(inspect *dockerInspectResponse) (string, error) {
	if inspect == nil {
		return "", fmt.Errorf("no container inspect data")
	}

	// 1. Container IP on a Docker network.
	var containerIP string
	// Prefer a "traefik" named network, otherwise pick the first network with an IP.
	for netName, net := range inspect.NetworkSettings.Networks {
		if net.IPAddress != "" {
			containerIP = net.IPAddress
			if strings.Contains(strings.ToLower(netName), "traefik") {
				break
			}
		}
	}
	if containerIP != "" {
		port := findTraefikAPIPort(inspect.Config.Cmd)
		return fmt.Sprintf("http://%s:%d", containerIP, port), nil
	}

	// 2. Published port on localhost.
	port := findPublishedAPIPort(inspect.NetworkSettings.Ports)
	if port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	}

	return "", fmt.Errorf("unable to resolve Traefik API URL: no override, no network IP, and no published port found")
}

// findTraefikAPIPort scans the container's command line for an entrypoint
// called "traefik" and extracts its port.  Defaults to 8080.
func findTraefikAPIPort(cmd []string) int {
	for _, arg := range cmd {
		// Look for --entrypoints.traefik.address=:PORT or similar.
		if strings.Contains(arg, "entrypoints.traefik.address") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				addr := strings.TrimSpace(parts[1])
				// addr could be ":8080" or "0.0.0.0:8080"
				if idx := strings.LastIndex(addr, ":"); idx >= 0 {
					portStr := addr[idx+1:]
					var port int
					if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil && port > 0 {
						return port
					}
				}
			}
		}
		// Also check --api.port=PORT (deprecated but still used in v2).
		if strings.HasPrefix(arg, "--api.port=") {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				var port int
				if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &port); err == nil && port > 0 {
					return port
				}
			}
		}
	}
	return 8080
}

// findPublishedAPIPort looks at the container's published port bindings for
// port 8080/tcp, or falls back to the first TCP published port.
func findPublishedAPIPort(ports map[string][]struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}) int {
	// Prefer 8080/tcp.
	if entries, ok := ports["8080/tcp"]; ok && len(entries) > 0 {
		var port int
		if _, err := fmt.Sscanf(entries[0].HostPort, "%d", &port); err == nil && port > 0 {
			return port
		}
	}
	// Fall back to the first published TCP port.
	for proto, entries := range ports {
		if !strings.HasSuffix(proto, "/tcp") {
			continue
		}
		if len(entries) > 0 && entries[0].HostPort != "" {
			var port int
			if _, err := fmt.Sscanf(entries[0].HostPort, "%d", &port); err == nil && port > 0 {
				return port
			}
		}
	}
	return 0
}

// traefikCandidateURLs returns the ordered, de-duplicated list of URLs to try
// when the primary URL is unreachable.  The primary URL is always first; it is
// followed by the published API host port, the container IP on port 8081 (the
// default insecure API entrypoint), and any other published TCP port.
func traefikCandidateURLs(inspect *dockerInspectResponse, primary string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}

	add(primary)

	if inspect == nil {
		return out
	}

	// Published 8080/tcp host port on localhost.
	if entries, ok := inspect.NetworkSettings.Ports["8080/tcp"]; ok && len(entries) > 0 {
		if entries[0].HostPort != "" {
			add("http://127.0.0.1:" + entries[0].HostPort)
		}
	}

	// Container IP on the default insecure API port 8081.
	var containerIP string
	for netName, net := range inspect.NetworkSettings.Networks {
		if net.IPAddress != "" {
			containerIP = net.IPAddress
			if strings.Contains(strings.ToLower(netName), "traefik") {
				break
			}
		}
	}
	if containerIP != "" {
		add(fmt.Sprintf("http://%s:8081", containerIP))
	}

	// Any other published TCP port (sorted for determinism).
	protos := make([]string, 0, len(inspect.NetworkSettings.Ports))
	for proto := range inspect.NetworkSettings.Ports {
		protos = append(protos, proto)
	}
	sort.Strings(protos)
	for _, proto := range protos {
		if proto == "8080/tcp" || !strings.HasSuffix(proto, "/tcp") {
			continue
		}
		entries := inspect.NetworkSettings.Ports[proto]
		if len(entries) > 0 && entries[0].HostPort != "" {
			add("http://127.0.0.1:" + entries[0].HostPort)
		}
	}

	return out
}

// probeTraefikAPIURL reports whether baseURL is reachable by making a short
// GET request to /api/version.  Any HTTP response (including 401/403) counts
// as reachable — only transport errors (connection refused/timeout) are
// treated as unreachable.  No credentials are sent during probing.
func probeTraefikAPIURL(client *http.Client, baseURL string) bool {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Get(baseURL + "/api/version")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// ---------------------------------------------------------------------------
// Fetching data from the Traefik API
// ---------------------------------------------------------------------------

// fetchTraefikEndpoint is a generic helper that makes an authenticated GET
// request to a Traefik API endpoint and decodes the JSON response into the
// given target pointer (which should be a *[]T, *T, etc.).
func fetchTraefikEndpoint(httpClient *http.Client, apiURL, path, authHeader string, target interface{}) error {
	req, err := http.NewRequest("GET", apiURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// A 404 is a normal "not configured" signal for optional endpoints
		// (e.g. no TLS certificates yet), not a real failure. Return a
		// sentinel-wrapped error the caller can classify.
		return fmt.Errorf("%w: not configured (404)", errTraefikNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// errTraefikNotFound marks an endpoint that returned 404 (feature not
// configured), which should not be treated as a connectivity failure.
var errTraefikNotFound = errors.New("traefik endpoint not found")

// fetchTraefikDashboard fetches all major Traefik API endpoints and returns
// a populated traefikDashboardData.  Individual endpoint errors are recorded
// in EndpointErrors rather than aborting the whole fetch.
func fetchTraefikDashboard(httpClient *http.Client, apiURL, authHeader string) *traefikDashboardData {
	d := &traefikDashboardData{
		APIURL:         apiURL,
		AuthConfigured: authHeader != "",
		EndpointErrors: make(map[string]string),
	}

	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// We fetch endpoints concurrently for speed.
	type endpointJob struct {
		name   string
		path   string
		target interface{}
	}
	var jobs []endpointJob

	var version traefikVersion
	jobs = append(jobs, endpointJob{"version", "/api/version", &version})

	var overview traefikOverview
	jobs = append(jobs, endpointJob{"overview", "/api/overview", &overview})

	var entrypoints []traefikEntrypoint
	jobs = append(jobs, endpointJob{"entrypoints", "/api/entrypoints", &entrypoints})

	var httpRouters []traefikRouter
	jobs = append(jobs, endpointJob{"httpRouters", "/api/http/routers", &httpRouters})

	var httpServices []traefikService
	jobs = append(jobs, endpointJob{"httpServices", "/api/http/services", &httpServices})

	var middlewares []traefikMiddleware
	jobs = append(jobs, endpointJob{"middlewares", "/api/http/middlewares", &middlewares})

	var tcpRouters []traefikRouter
	jobs = append(jobs, endpointJob{"tcpRouters", "/api/tcp/routers", &tcpRouters})

	var tcpServices []traefikService
	jobs = append(jobs, endpointJob{"tcpServices", "/api/tcp/services", &tcpServices})

	var tlsCerts []traefikTLSCertificate
	jobs = append(jobs, endpointJob{"tlsCerts", "/api/tls/certificates", &tlsCerts})

	// Run all jobs concurrently.
	type jobResult struct {
		name          string
		err           string
		notConfigured bool
	}
	resultCh := make(chan jobResult, len(jobs))

	for _, j := range jobs {
		j := j // capture
		go func() {
			err := fetchTraefikEndpoint(httpClient, apiURL, j.path, authHeader, j.target)
			switch {
			case err == nil:
				resultCh <- jobResult{name: j.name}
			case errors.Is(err, errTraefikNotFound):
				resultCh <- jobResult{name: j.name, err: err.Error(), notConfigured: true}
			default:
				resultCh <- jobResult{name: j.name, err: err.Error()}
			}
		}()
	}

	for range jobs {
		r := <-resultCh
		if r.err == "" {
			continue
		}
		if r.notConfigured {
			d.NotConfigured = append(d.NotConfigured, r.name)
			continue
		}
		d.EndpointErrors[r.name] = r.err
	}
	sort.Strings(d.NotConfigured)

	// Assign results that succeeded.
	errs := d.EndpointErrors
	if _, ok := errs["version"]; !ok {
		d.Version = &version
		if major, ok := parseTraefikMajorVersion(version.Version); ok {
			d.MajorVersion = major
			d.VersionSupported = major == traefikSupportedMajorVersion
		}
	}
	if _, ok := errs["overview"]; !ok {
		d.Overview = &overview
	}
	if _, ok := errs["entrypoints"]; !ok {
		d.Entrypoints = entrypoints
	}
	if _, ok := errs["httpRouters"]; !ok {
		d.HTTPRouters = httpRouters
	}
	if _, ok := errs["httpServices"]; !ok {
		d.HTTPServices = httpServices
	}
	if _, ok := errs["middlewares"]; !ok {
		d.Middlewares = middlewares
	}
	if _, ok := errs["tcpRouters"]; !ok {
		d.TCPRouters = tcpRouters
	}
	if _, ok := errs["tcpServices"]; !ok {
		d.TCPServices = tcpServices
	}
	if _, ok := errs["tlsCerts"]; !ok {
		d.TLSCerts = tlsCerts
	}

	return d
}

// ---------------------------------------------------------------------------
// The integration adapter
// ---------------------------------------------------------------------------

// traefikIntegration adapts the Traefik collector to the integration
// framework. It declares no minimum poll interval: Traefik keeps its existing
// every-cycle behaviour.
type traefikIntegration struct{}

func traefikIntegrationInstance() integration { return traefikIntegration{} }

func (t traefikIntegration) ID() string          { return "traefik" }
func (t traefikIntegration) DisplayName() string { return "Traefik" }
func (t traefikIntegration) Description() string {
	return "Reads the Traefik API: routers, services, middlewares, entrypoints, and TLS certificates."
}
func (t traefikIntegration) MinPollInterval() time.Duration { return 0 }

// Detect matches the Traefik image repository exactly.
func (t traefikIntegration) Detect(c dockerContainer) bool {
	return matchesImageRepository(c.Image, "traefik")
}

// Settings resolves the generic override layers, then folds in the legacy
// Traefik names where the generic layers left a gap. Label still beats env,
// as documented before the framework existed.
func (t traefikIntegration) Settings(inspect *dockerInspectResponse, cfg *overrideConfig) integrationSettings {
	var labels map[string]string
	if inspect != nil {
		labels = inspect.Config.Labels
	}
	s := resolveIntegrationSettings(t.ID(), cfg, labels)

	if s.URL == "" {
		if v, ok := labels["dumbdock.traefik.api"]; ok && strings.TrimSpace(v) != "" {
			s.URL = strings.TrimSpace(v)
			s.Sources["url"] = sourceLabel
			s.Forced = true
		} else if v := strings.TrimSpace(os.Getenv("TRAEFIK_API_URL")); v != "" {
			s.URL = v
			s.Sources["url"] = sourceEnv
			s.Forced = true
		}
	}
	if s.Token == "" {
		// Config beats env, as the documented precedence says; the legacy names
		// are folded into the matching layer.
		if cfg != nil && cfg.TraefikAPIToken != "" {
			s.Token = cfg.TraefikAPIToken
			s.Sources["token"] = sourceConfig
		} else if v := strings.TrimSpace(os.Getenv("TRAEFIK_API_TOKEN")); v != "" {
			s.Token = v
			s.Sources["token"] = sourceEnv
		}
	}
	if s.Username == "" {
		if v := os.Getenv("TRAEFIK_API_USER"); v != "" {
			s.Username = v
			s.Sources["username"] = sourceEnv
		}
	}
	if s.Password == "" {
		if v := os.Getenv("TRAEFIK_API_PASS"); v != "" {
			s.Password = v
			s.Sources["password"] = sourceEnv
		}
	}
	return s
}

// Collect resolves the endpoint, fetches every endpoint, runs the bounded
// candidate probe when nothing answered, and records log-dedup state. The
// framework owns detection, inspection, and the gate's fields.
func (t traefikIntegration) Collect(rt integrationRuntime, target integrationTarget) integrationState {
	st := target.Base
	gate := rt.gate
	if gate == nil {
		gate = &integrationGate{id: t.ID(), displayName: t.DisplayName()}
	}

	apiURL := target.Settings.URL
	urlSource := target.Settings.source("url")
	if apiURL == "" {
		resolved, err := resolveTraefikAPIURL(target.Inspect)
		if err != nil {
			reason := fmt.Sprintf("resolve API URL: %v", err)
			st.Error = reason
			if !gate.passed {
				gate.recordFailure(reason)
			} else {
				log.Printf("traefik: resolve URL for %s: %v", st.ContainerName, err)
			}
			return st
		}
		apiURL = resolved
		urlSource = sourceAuto
	}
	st.URL = apiURL
	st.URLSource = urlSource

	authHeader := integrationAuthHeader(target.Settings)
	st.AuthConfigured = authHeader != ""

	httpClient := rt.client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	data := fetchTraefikDashboard(httpClient, apiURL, authHeader)

	// If every endpoint failed, the primary (container-IP) URL is likely
	// wrong: probe fallback candidates (published ports, alternate API port)
	// and adopt the first reachable one before reporting unreachable.
	if allEndpointFetchesFailed(data) {
		for _, candidate := range traefikCandidateURLs(target.Inspect, apiURL) {
			if candidate == apiURL {
				continue
			}
			probeClient := &http.Client{Timeout: 2 * time.Second}
			if !probeTraefikAPIURL(probeClient, candidate) {
				continue
			}
			retry := fetchTraefikDashboard(httpClient, candidate, authHeader)
			if !allEndpointFetchesFailed(retry) {
				apiURL = candidate
				data = retry
				break
			}
		}
	}

	st.URL = apiURL
	st.VendorData = data

	errSig, firstErr := traefikErrorSignature(data)
	allFailed := allEndpointFetchesFailed(data)
	versionUnsupported := data.Version != nil && !data.VersionSupported
	combinedSig := traefikLogSignature(data, allFailed)

	if allFailed {
		st.Error = fmt.Sprintf("API unreachable at %s: %s", apiURL, firstErr)
	} else {
		st.Error = ""
	}

	traefikLogMu.Lock()
	gateWasPassed := gate.passed
	lastErrSig := traefikLastErrSig
	lastReachable := traefikLastReachable
	// Boot gate: while it is still armed, a failed fetch spends one attempt and
	// logs exactly one line. The per-endpoint lines below stay for the
	// post-boot path, where a recovery is still worth reporting.
	if allFailed && !gateWasPassed {
		gate.recordFailure(fmt.Sprintf("API unreachable at %s: %s", apiURL, firstErr))
	} else if combinedSig != lastErrSig {
		if errSig == "" {
			if lastErrSig != "" {
				log.Printf("traefik: API recovered at %s", apiURL)
			}
		} else {
			names := make([]string, 0, len(data.EndpointErrors))
			for n := range data.EndpointErrors {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				log.Printf("traefik: %s endpoint error: %s", n, data.EndpointErrors[n])
			}
			if allFailed {
				log.Printf("traefik: API unreachable at %s: %s", apiURL, firstErr)
			}
		}
		if versionUnsupported {
			log.Printf("traefik: unsupported version %q (only v%d is supported) at %s",
				data.Version.Version, traefikSupportedMajorVersion, apiURL)
		}
	}
	traefikLastErrSig = combinedSig

	// The first fetch that answered at least one endpoint disarms the boot gate
	// permanently.
	if !allFailed {
		gate.pass()
	}

	// Log a success line only when the API becomes reachable (first success or
	// recovery), never on every poll and never when all endpoints failed.
	if !allFailed && !lastReachable {
		version := "unknown"
		if data.Version != nil {
			version = data.Version.Version
		}
		log.Printf("traefik: detected %s at %s (version: %s)", st.ContainerName, apiURL, version)
	}
	traefikLastReachable = !allFailed
	traefikLogMu.Unlock()

	if versionUnsupported {
		st.Warnings = append(st.Warnings, fmt.Sprintf("unsupported Traefik version %q (major %d); dumbdock supports major version %d",
			data.Version.Version, data.MajorVersion, traefikSupportedMajorVersion))
	}
	for _, name := range data.NotConfigured {
		st.Warnings = append(st.Warnings, fmt.Sprintf("%s endpoint not configured", name))
	}

	if data.Overview != nil {
		st.Stats = []integrationStat{
			{Label: "HTTP routers", Value: strconv.Itoa(data.Overview.HTTP.Routers.Int())},
			{Label: "HTTP services", Value: strconv.Itoa(data.Overview.HTTP.Services.Int())},
			{Label: "Middlewares", Value: strconv.Itoa(data.Overview.HTTP.Middlewares.Int())},
			{Label: "TCP routers", Value: strconv.Itoa(data.Overview.TCP.Routers.Int())},
			{Label: "TCP services", Value: strconv.Itoa(data.Overview.TCP.Services.Int())},
			{Label: "Entrypoints", Value: strconv.Itoa(len(data.Entrypoints))},
		}
	}
	if data.Version != nil {
		st.Sections = append(st.Sections, integrationSection{
			Title:   "Version",
			Columns: []string{"Field", "Value"},
			Rows: [][]string{
				{"Version", data.Version.Version},
				{"Codename", data.Version.Codename},
				{"Go", data.Version.Goversion},
				{"Started", data.Version.StartDate},
			},
		})
	}
	return st
}

// Issues reports the Traefik findings from the published state.
func (t traefikIntegration) Issues(st integrationState) ([]hostIssue, []string, bool) {
	return collectTraefikIssues(snapshotFromIntegrationState(st))
}

// ---------------------------------------------------------------------------
// Refresh entry point
// ---------------------------------------------------------------------------

// refreshTraefik is the Traefik-shaped entry point used by the tests. In
// production refreshIntegrations runs the whole registry.
func refreshTraefik(dockerClient *http.Client, containers []dockerContainer, cfg *overrideConfig) {
	refreshIntegrations(dockerClient, containers, cfg)
}

// registerTraefik wires the integration into the registry at init time.
func init() {
	registerIntegration(traefikIntegrationInstance())
}
