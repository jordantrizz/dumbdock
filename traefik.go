package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Docker container inspection (used to resolve the Traefik API URL)
// ---------------------------------------------------------------------------

type dockerInspectResponse struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	State  struct {
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
			IPAddress  string `json:"IPAddress"`
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
	APIURL        string                 `json:"apiUrl"`
	AuthConfigured bool                  `json:"authConfigured"`
	Version       *traefikVersion        `json:"version,omitempty"`
	Overview      *traefikOverview       `json:"overview,omitempty"`
	Entrypoints   []traefikEntrypoint    `json:"entrypoints,omitempty"`
	HTTPRouters   []traefikRouter        `json:"httpRouters,omitempty"`
	HTTPServices  []traefikService       `json:"httpServices,omitempty"`
	Middlewares   []traefikMiddleware    `json:"middlewares,omitempty"`
	TCPRouters    []traefikRouter        `json:"tcpRouters,omitempty"`
	TCPServices   []traefikService       `json:"tcpServices,omitempty"`
	TLSCerts      []traefikTLSCertificate `json:"tlsCerts,omitempty"`
	EndpointErrors map[string]string     `json:"endpointErrors,omitempty"`
}

type traefikVersion struct {
	Version   string `json:"Version"`
	Codename  string `json:"Codename"`
	StartDate string `json:"StartDate"`
	Goversion string `json:"Goversion"`
}

type traefikOverview struct {
	HTTP struct {
		Routers     int `json:"routers"`
		Services    int `json:"services"`
		Middlewares int `json:"middlewares"`
	} `json:"http"`
	TCP struct {
		Routers     int `json:"routers"`
		Services    int `json:"services"`
		Middlewares int `json:"middlewares"`
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
	Name         string              `json:"name"`
	Status       string              `json:"status"`
	Type         string              `json:"type"`
	ServerStatus map[string]string   `json:"serverStatus,omitempty"`
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
// Global state (updated by the refresh goroutine, read by the HTTP handler)
// ---------------------------------------------------------------------------

var (
	traefikMu            sync.RWMutex
	traefikContainerFound bool
	traefikAPIURL         string
	traefikData           *traefikDashboardData
	traefikDataErr        string
)

// getTraefikState returns a snapshot of the current Traefik state.
func getTraefikState() (found bool, url string, data *traefikDashboardData, errStr string) {
	traefikMu.RLock()
	defer traefikMu.RUnlock()
	return traefikContainerFound, traefikAPIURL, traefikData, traefikDataErr
}

// ---------------------------------------------------------------------------
// Detection: find a running Traefik container
// ---------------------------------------------------------------------------

// findTraefikContainer scans the given container list for a running container
// whose image contains "traefik" (case-insensitive).  Returns the first
// match, or nil.
func findTraefikContainer(containers []dockerContainer) *dockerContainer {
	for i := range containers {
		if containers[i].State != "running" {
			continue
		}
		if strings.Contains(strings.ToLower(containers[i].Image), "traefik") {
			return &containers[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Docker inspect
// ---------------------------------------------------------------------------

// inspectTraefikContainer calls the Docker API to get full container
// metadata (networks, ports, labels, env, cmd).
func inspectTraefikContainer(dockerClient *http.Client, containerID string) (*dockerInspectResponse, error) {
	url := fmt.Sprintf("http://localhost/v1.45/containers/%s/json", containerID)
	resp, err := dockerClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("container inspect returned status %d", resp.StatusCode)
	}

	var inspect dockerInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return nil, fmt.Errorf("decode inspect response: %w", err)
	}
	return &inspect, nil
}

// ---------------------------------------------------------------------------
// API URL resolution (fallback chain)
// ---------------------------------------------------------------------------

// resolveTraefikAPIURL uses the following fallback chain to determine the
// Traefik API base URL:
//
//  1. The dumbdock.traefik.api label on the container.
//  2. The TRAEFIK_API_URL environment variable.
//  3. The container's IP address on its first Docker network, plus port 8080.
//  4. The first published port (preferring 8080) on 127.0.0.1.
func resolveTraefikAPIURL(inspect *dockerInspectResponse) (string, error) {
	// 1. Container label dumbdock.traefik.api
	if u, ok := inspect.Config.Labels["dumbdock.traefik.api"]; ok && u != "" {
		return u, nil
	}

	// 2. Environment variable TRAEFIK_API_URL
	if u := os.Getenv("TRAEFIK_API_URL"); u != "" {
		return u, nil
	}

	// 3. Container IP on a Docker network + port 8080
	var containerIP string
	// Prefer "traefik" named network, otherwise pick the first network with an IP.
	for netName, net := range inspect.NetworkSettings.Networks {
		if net.IPAddress != "" {
			containerIP = net.IPAddress
			if strings.Contains(strings.ToLower(netName), "traefik") {
				break
			}
		}
	}
	if containerIP != "" {
		// Determine the API port: check the running command for
		// --entrypoints.traefik.address=:PORT, default to 8080.
		port := findTraefikAPIPort(inspect.Config.Cmd)
		return fmt.Sprintf("http://%s:%d", containerIP, port), nil
	}

	// 4. Published port on localhost
	port := findPublishedAPIPort(inspect.NetworkSettings.Ports)
	if port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d", port), nil
	}

	return "", fmt.Errorf("unable to resolve Traefik API URL: no label, no env var, no network IP, and no published port found")
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

// ---------------------------------------------------------------------------
// API authentication
// ---------------------------------------------------------------------------

// getTraefikAuthHeader returns an HTTP Authorization header value, or empty
// string if no auth is configured.  It checks (in order):
//  1. TRAEFIK_API_TOKEN env var → Bearer
//  2. cfg.TraefikAPIToken → Bearer
//  3. TRAEFIK_API_USER + TRAEFIK_API_PASS env vars → Basic
func getTraefikAuthHeader(cfg *overrideConfig) string {
	if token := os.Getenv("TRAEFIK_API_TOKEN"); token != "" {
		return "Bearer " + token
	}
	if cfg != nil && cfg.TraefikAPIToken != "" {
		return "Bearer " + cfg.TraefikAPIToken
	}
	user := os.Getenv("TRAEFIK_API_USER")
	pass := os.Getenv("TRAEFIK_API_PASS")
	if user != "" || pass != "" {
		raw := user + ":" + pass
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	return ""
}

// ---------------------------------------------------------------------------
// Fetching data from Traefik API
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
		return fmt.Errorf("not found (404)")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

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
		name string
		err  string
	}
	resultCh := make(chan jobResult, len(jobs))

	for _, j := range jobs {
		j := j // capture
		go func() {
			if err := fetchTraefikEndpoint(httpClient, apiURL, j.path, authHeader, j.target); err != nil {
				resultCh <- jobResult{j.name, err.Error()}
			} else {
				resultCh <- jobResult{j.name, ""}
			}
		}()
	}

	for range jobs {
		r := <-resultCh
		if r.err != "" {
			d.EndpointErrors[r.name] = r.err
		}
	}

	// Assign results that succeeded.
	errs := d.EndpointErrors
	if _, ok := errs["version"]; !ok {
		d.Version = &version
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
// Top-level refresh function (called by the refresh goroutine)
// ---------------------------------------------------------------------------

// refreshTraefik is called from main.go's refresh() to update the global
// Traefik state.  It is safe for concurrent access.
func refreshTraefik(dockerClient *http.Client, containers []dockerContainer, cfg *overrideConfig) {
	container := findTraefikContainer(containers)

	traefikMu.Lock()
	defer traefikMu.Unlock()

	if container == nil {
		traefikContainerFound = false
		traefikAPIURL = ""
		traefikData = nil
		traefikDataErr = ""
		return
	}

	// Inspect the container.
	inspect, err := inspectTraefikContainer(dockerClient, container.ID)
	if err != nil {
		traefikContainerFound = true
		traefikAPIURL = ""
		traefikData = nil
		traefikDataErr = fmt.Sprintf("inspection failed: %v", err)
		log.Printf("traefik: inspect %s (%s): %v", containerName(*container), container.ID[:12], err)
		return
	}

	// Resolve API URL.
	apiURL, err := resolveTraefikAPIURL(inspect)
	if err != nil {
		traefikContainerFound = true
		traefikAPIURL = ""
		traefikData = nil
		traefikDataErr = fmt.Sprintf("resolve API URL: %v", err)
		log.Printf("traefik: resolve URL for %s: %v", containerName(*container), err)
		return
	}
	traefikAPIURL = apiURL

	// Determine auth.
	authHeader := getTraefikAuthHeader(cfg)

	// Fetch dashboard data.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	data := fetchTraefikDashboard(httpClient, apiURL, authHeader)

	traefikContainerFound = true
	traefikData = data

	// Log any endpoint errors.
	if len(data.EndpointErrors) > 0 {
		names := make([]string, 0, len(data.EndpointErrors))
		for n := range data.EndpointErrors {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			log.Printf("traefik: %s endpoint error: %s", n, data.EndpointErrors[n])
		}
	}

	log.Printf("traefik: detected %s at %s (version: %v)",
		containerName(*container), apiURL,
		func() string {
			if data.Version != nil {
				return data.Version.Version
			}
			return "unknown"
		}())
}
