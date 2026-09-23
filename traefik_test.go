package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllEndpointFetchesFailed(t *testing.T) {
	if allEndpointFetchesFailed(nil) {
		t.Fatal("nil data must not report all endpoints failed")
	}
	if allEndpointFetchesFailed(&traefikDashboardData{}) {
		t.Fatal("empty endpoint errors must not report all endpoints failed")
	}

	partial := &traefikDashboardData{EndpointErrors: map[string]string{
		"tlsCerts": "not found (404)",
	}}
	if allEndpointFetchesFailed(partial) {
		t.Fatal("partial endpoint errors must not report all endpoints failed")
	}

	all := &traefikDashboardData{EndpointErrors: make(map[string]string)}
	for _, n := range traefikEndpointNames {
		all.EndpointErrors[n] = "connection refused"
	}
	if !allEndpointFetchesFailed(all) {
		t.Fatal("all endpoint errors must report all endpoints failed")
	}
}

func TestTraefikErrorSignatureDeterministic(t *testing.T) {
	a := &traefikDashboardData{EndpointErrors: map[string]string{
		"version":     "connection refused",
		"overview":    "connection refused",
		"entrypoints": "connection refused",
	}}
	b := &traefikDashboardData{EndpointErrors: map[string]string{
		"entrypoints": "connection refused",
		"version":     "connection refused",
		"overview":    "connection refused",
	}}

	sigA, firstA := traefikErrorSignature(a)
	sigB, firstB := traefikErrorSignature(b)

	if sigA != sigB {
		t.Fatalf("signature not stable across map order: %q != %q", sigA, sigB)
	}
	// Sorted endpoint order puts "entrypoints" first.
	if firstA != "connection refused" || firstB != "connection refused" {
		t.Fatalf("unexpected first error: %q / %q", firstA, firstB)
	}
	if !strings.HasPrefix(sigA, "entrypoints=") {
		t.Fatalf("signature should start with the first sorted endpoint, got %q", sigA)
	}
}

func TestTraefikErrorSignatureEmpty(t *testing.T) {
	if sig, first := traefikErrorSignature(nil); sig != "" || first != "" {
		t.Fatalf("nil data should have empty signature, got %q / %q", sig, first)
	}
	if sig, first := traefikErrorSignature(&traefikDashboardData{}); sig != "" || first != "" {
		t.Fatalf("empty errors should have empty signature, got %q / %q", sig, first)
	}
}

func TestParseTraefikMajorVersion(t *testing.T) {
	tests := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"v3.7.13", 3, true},
		{"3.7.13", 3, true},
		{"v2.2", 2, true},
		{"2.11.0", 2, true},
		{"V1.7", 1, true},
		{"v3", 3, true},
		{"  v3.1.0  ", 3, true},
		{"", 0, false},
		{"garbage", 0, false},
		{"v", 0, false},
		{"vX.Y", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseTraefikMajorVersion(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseTraefikMajorVersion(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestFetchTraefikDashboardVersionSupport(t *testing.T) {
	makeServer := func(version string) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"Version":"` + version + `"}`))
		})
		return httptest.NewServer(mux)
	}

	v3srv := makeServer("v3.7.13")
	defer v3srv.Close()
	d3 := fetchTraefikDashboard(v3srv.Client(), v3srv.URL, "")
	if !d3.VersionSupported || d3.MajorVersion != 3 {
		t.Fatalf("v3 should be supported, got major=%d supported=%v", d3.MajorVersion, d3.VersionSupported)
	}

	v2srv := makeServer("v2.2.0")
	defer v2srv.Close()
	d2 := fetchTraefikDashboard(v2srv.Client(), v2srv.URL, "")
	if d2.VersionSupported || d2.MajorVersion != 2 {
		t.Fatalf("v2 should not be supported, got major=%d supported=%v", d2.MajorVersion, d2.VersionSupported)
	}
}

func TestFetchTraefikDashboardVersionSupportUnparseable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Version":"dev"}`))
	}))
	defer srv.Close()
	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	if d.VersionSupported || d.MajorVersion != 0 {
		t.Fatalf("unparseable version should be unsupported with major 0, got major=%d supported=%v", d.MajorVersion, d.VersionSupported)
	}
}

func TestFetchTraefikDashboardVersionSupportWhenVersionFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	if d.VersionSupported || d.MajorVersion != 0 {
		t.Fatalf("missing version should be unsupported with major 0, got major=%d supported=%v", d.MajorVersion, d.VersionSupported)
	}
}

func TestTraefikLogSignature(t *testing.T) {
	v2 := &traefikDashboardData{Version: &traefikVersion{Version: "v2.2.0"}, MajorVersion: 2, VersionSupported: false}
	v3 := &traefikDashboardData{Version: &traefikVersion{Version: "v3.7.13"}, MajorVersion: 3, VersionSupported: true}

	sigV2 := traefikLogSignature(v2, false)
	sigV3 := traefikLogSignature(v3, false)
	if sigV2 == sigV3 {
		t.Fatalf("supported and unsupported signatures must differ, both %q", sigV2)
	}
	if sigV3 == "" {
		t.Fatal("supported v3 signature should be non-empty (marker)")
	}
	if !strings.Contains(sigV2, "unsupported") {
		t.Fatalf("v2 signature should mark unsupported, got %q", sigV2)
	}

	// Stable across repeated calls.
	if traefikLogSignature(v2, false) != sigV2 {
		t.Fatal("signature must be stable across calls")
	}

	// When all endpoints failed there is no version to report, so the signature
	// falls back to the error signature only.
	allFailedV2 := &traefikDashboardData{
		Version:          v2.Version,
		VersionSupported: false,
		EndpointErrors:   map[string]string{"version": "connection refused"},
	}
	if sig := traefikLogSignature(allFailedV2, true); strings.Contains(sig, "unsupported") {
		t.Fatalf("all-failed signature must not include the version marker, got %q", sig)
	}
}

func TestTraefikCandidateURLs(t *testing.T) {
	inspect := &dockerInspectResponse{}
	inspect.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
	}{
		"traefik": {IPAddress: "172.18.0.4"},
	}
	inspect.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"80/tcp":   {{HostPort: "80"}},
		"8080/tcp": {{HostPort: "8081"}},
	}

	primary := "http://172.18.0.4:8080"
	got := traefikCandidateURLs(inspect, primary)

	want := []string{
		"http://172.18.0.4:8080",
		"http://127.0.0.1:8081",
		"http://172.18.0.4:8081",
		"http://127.0.0.1:80",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}

	// No duplicates when the primary already equals a candidate.
	if got := traefikCandidateURLs(nil, ""); len(got) != 0 {
		t.Fatalf("nil inspect should yield no candidates, got %v", got)
	}
}

func TestProbeTraefikAPIURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !probeTraefikAPIURL(nil, srv.URL) {
		t.Fatal("reachable server should probe true")
	}
	if probeTraefikAPIURL(nil, "http://127.0.0.1:1") {
		t.Fatal("closed port should probe false")
	}
}

func TestTraefikOverviewUnmarshal(t *testing.T) {
	// Traefik v2 shape: bare integers.
	v2 := []byte(`{"http":{"routers":5,"services":4,"middlewares":2},"tcp":{"routers":0,"services":0,"middlewares":0}}`)
	var ov2 traefikOverview
	if err := json.Unmarshal(v2, &ov2); err != nil {
		t.Fatalf("v2 overview unmarshal: %v", err)
	}
	if ov2.HTTP.Routers.Int() != 5 || ov2.HTTP.Services.Int() != 4 || ov2.HTTP.Middlewares.Int() != 2 {
		t.Fatalf("v2 counts wrong: %d/%d/%d", ov2.HTTP.Routers, ov2.HTTP.Services, ov2.HTTP.Middlewares)
	}

	// Traefik v3 shape: objects with a "total" field.
	v3 := []byte(`{"http":{"routers":{"total":7,"warnings":0,"errors":0},"services":{"total":6},"middlewares":{"total":3}},"tcp":{"routers":{"total":1},"services":{"total":1},"middlewares":{"total":0}}}`)
	var ov3 traefikOverview
	if err := json.Unmarshal(v3, &ov3); err != nil {
		t.Fatalf("v3 overview unmarshal: %v", err)
	}
	if ov3.HTTP.Routers.Int() != 7 || ov3.HTTP.Services.Int() != 6 || ov3.HTTP.Middlewares.Int() != 3 {
		t.Fatalf("v3 counts wrong: %d/%d/%d", ov3.HTTP.Routers, ov3.HTTP.Services, ov3.HTTP.Middlewares)
	}
	if ov3.TCP.Routers.Int() != 1 {
		t.Fatalf("v3 tcp routers wrong: %d", ov3.TCP.Routers)
	}

	// A missing "total" falls back to zero rather than failing.
	var ovEmpty traefikOverview
	if err := json.Unmarshal([]byte(`{"http":{"routers":{}}}`), &ovEmpty); err != nil {
		t.Fatalf("empty object overview unmarshal: %v", err)
	}
	if ovEmpty.HTTP.Routers.Int() != 0 {
		t.Fatalf("expected 0 for missing total, got %d", ovEmpty.HTTP.Routers.Int())
	}
}

func TestFetchTraefikDashboardClassifies404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Version":"v3.7.13"}`))
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"http":{"routers":{"total":2}}}`))
	})
	mux.HandleFunc("/api/tls/certificates", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := fetchTraefikDashboard(srv.Client(), srv.URL, "")
	found := false
	for _, n := range d.NotConfigured {
		if n == "tlsCerts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tlsCerts 404 should be in NotConfigured, got %v", d.NotConfigured)
	}
	if len(d.EndpointErrors) != 0 {
		t.Fatalf("all-404 endpoints must not be endpoint errors, got %v", d.EndpointErrors)
	}
	if allEndpointFetchesFailed(d) {
		t.Fatal("a reachable API with 404s must not report all endpoints failed")
	}
	if d.Version == nil || d.Version.Version != "v3.7.13" {
		t.Fatalf("version should decode, got %+v", d.Version)
	}
}
