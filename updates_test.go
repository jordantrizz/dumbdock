package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in       string
		ok       bool
		registry string
		repo     string
		tag      string
		pinned   bool
	}{
		{in: "nginx", ok: true, registry: dockerHubHost, repo: "library/nginx", tag: "latest"},
		{in: "nginx:1.25", ok: true, registry: dockerHubHost, repo: "library/nginx", tag: "1.25"},
		{in: "library/nginx:latest", ok: true, registry: dockerHubHost, repo: "library/nginx", tag: "latest"},
		{in: "docker.io/nginx:latest", ok: true, registry: dockerHubHost, repo: "library/nginx", tag: "latest"},
		{in: "index.docker.io/library/redis:7", ok: true, registry: dockerHubHost, repo: "library/redis", tag: "7"},
		{in: "registry-1.docker.io/library/redis:7", ok: true, registry: dockerHubHost, repo: "library/redis", tag: "7"},
		{in: "portainer/portainer-ce:latest", ok: true, registry: dockerHubHost, repo: "portainer/portainer-ce", tag: "latest"},
		{in: "ghcr.io/owner/repo:v1", ok: true, registry: ghcrHost, repo: "owner/repo", tag: "v1"},
		{in: "ghcr.io/Owner/Repo", ok: true, registry: ghcrHost, repo: "owner/repo", tag: "latest"},
		{in: "nginx@sha256:deadbeef", ok: true, registry: dockerHubHost, repo: "library/nginx", pinned: true},
		{in: "ghcr.io/owner/repo@sha256:deadbeef", ok: true, registry: ghcrHost, repo: "owner/repo", pinned: true},
		{in: "lscr.io/linuxserver/foo:latest", ok: true, registry: "lscr.io", repo: "linuxserver/foo", tag: "latest"},
		{in: "localhost:5000/nginx:latest", ok: true, registry: "localhost:5000", repo: "nginx", tag: "latest"},
		{in: "", ok: false},
		{in: "   ", ok: false},
	}

	for _, tc := range cases {
		ref, ok := parseImageRef(tc.in)
		if ok != tc.ok {
			t.Fatalf("parseImageRef(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if ref.Registry != tc.registry || ref.Repo != tc.repo || ref.Tag != tc.tag || ref.Pinned != tc.pinned {
			t.Errorf("parseImageRef(%q) = {%s %s %s pinned=%v}, want {%s %s %s pinned=%v}",
				tc.in, ref.Registry, ref.Repo, ref.Tag, ref.Pinned, tc.registry, tc.repo, tc.tag, tc.pinned)
		}
	}
}

func TestImageRefSupportedAndKey(t *testing.T) {
	hub, _ := parseImageRef("nginx:latest")
	if !hub.supported() {
		t.Errorf("docker hub ref should be supported")
	}
	if got, want := hub.key(), dockerHubHost+"/library/nginx:latest"; got != want {
		t.Errorf("key() = %q, want %q", got, want)
	}

	ghcr, _ := parseImageRef("ghcr.io/owner/repo:v1")
	if !ghcr.supported() {
		t.Errorf("ghcr ref should be supported")
	}
	if got, want := ghcr.key(), "ghcr.io/owner/repo:v1"; got != want {
		t.Errorf("key() = %q, want %q", got, want)
	}

	other, _ := parseImageRef("lscr.io/linuxserver/foo:latest")
	if other.supported() {
		t.Errorf("lscr.io ref should not be supported")
	}

	pinned, _ := parseImageRef("nginx@sha256:abc")
	if got, want := pinned.key(), dockerHubHost+"/library/nginx@pinned"; got != want {
		t.Errorf("pinned key() = %q, want %q", got, want)
	}
}

func TestTokenURL(t *testing.T) {
	hub, _ := parseImageRef("nginx:latest")
	got, ok := tokenURL(hub)
	if !ok {
		t.Fatalf("tokenURL(hub) ok = false")
	}
	want := "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library%2Fnginx:pull"
	if got != want {
		t.Errorf("tokenURL(hub) = %q, want %q", got, want)
	}

	ghcr, _ := parseImageRef("ghcr.io/owner/repo:v1")
	got, ok = tokenURL(ghcr)
	if !ok {
		t.Fatalf("tokenURL(ghcr) ok = false")
	}
	want = "https://ghcr.io/token?service=ghcr.io&scope=repository:owner%2Frepo:pull"
	if got != want {
		t.Errorf("tokenURL(ghcr) = %q, want %q", got, want)
	}

	other, _ := parseImageRef("lscr.io/linuxserver/foo:latest")
	if _, ok := tokenURL(other); ok {
		t.Errorf("tokenURL(other) ok = true, want false")
	}
}

func TestManifestURL(t *testing.T) {
	hub, _ := parseImageRef("nginx:latest")
	if got, want := manifestURL(hub), "https://registry-1.docker.io/v2/library/nginx/manifests/latest"; got != want {
		t.Errorf("manifestURL = %q, want %q", got, want)
	}
	ghcr, _ := parseImageRef("ghcr.io/owner/repo:v1")
	if got, want := manifestURL(ghcr), "https://ghcr.io/v2/owner/repo/manifests/v1"; got != want {
		t.Errorf("manifestURL = %q, want %q", got, want)
	}
}

func TestFetchManifestDigestHeader(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Docker-Content-Digest", "sha256:abc123")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	digest, err := fetchManifestDigest(context.Background(), srv.Client(), srv.URL, "tok")
	if err != nil {
		t.Fatalf("fetchManifestDigest: %v", err)
	}
	if digest != "sha256:abc123" {
		t.Errorf("digest = %q, want sha256:abc123", digest)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", gotAuth)
	}
	if !strings.Contains(gotAccept, "manifest.list.v2+json") {
		t.Errorf("Accept = %q, missing manifest list type", gotAccept)
	}
}

func TestFetchManifestDigestBodyFallback(t *testing.T) {
	body := []byte(`{"schemaVersion":2}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer srv.Close()

	digest, err := fetchManifestDigest(context.Background(), srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatalf("fetchManifestDigest: %v", err)
	}
	sum := sha256.Sum256(body)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if digest != want {
		t.Errorf("digest = %q, want %q", digest, want)
	}
}

func TestFetchManifestDigestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := fetchManifestDigest(context.Background(), srv.Client(), srv.URL, ""); err == nil {
		t.Errorf("expected error for 404 response")
	}
}

func TestFetchAnonymousToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"abc","expires_in":300}`))
	}))
	defer srv.Close()

	token, expiry, err := fetchAnonymousToken(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchAnonymousToken: %v", err)
	}
	if token != "abc" {
		t.Errorf("token = %q, want abc", token)
	}
	if !expiry.After(time.Now()) {
		t.Errorf("expiry %v should be in the future", expiry)
	}
}

func TestFetchAnonymousTokenAccessTokenFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"xyz"}`))
	}))
	defer srv.Close()

	token, _, err := fetchAnonymousToken(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchAnonymousToken: %v", err)
	}
	if token != "xyz" {
		t.Errorf("token = %q, want xyz", token)
	}
}

func TestFetchAnonymousTokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, _, err := fetchAnonymousToken(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Errorf("expected error for 401 response")
	}
}

func TestLocalDigestForRepo(t *testing.T) {
	digests := []string{
		"nginx@sha256:aaa",
		"ghcr.io/owner/repo@sha256:bbb",
	}
	hub, _ := parseImageRef("nginx:latest")
	if got := localDigestForRepo(digests, hub); got != "sha256:aaa" {
		t.Errorf("hub digest = %q, want sha256:aaa", got)
	}
	ghcr, _ := parseImageRef("ghcr.io/owner/repo:v1")
	if got := localDigestForRepo(digests, ghcr); got != "sha256:bbb" {
		t.Errorf("ghcr digest = %q, want sha256:bbb", got)
	}
	missing, _ := parseImageRef("redis:7")
	if got := localDigestForRepo(digests, missing); got != "" {
		t.Errorf("missing digest = %q, want empty", got)
	}
}

func TestDigestsEqual(t *testing.T) {
	if !digestsEqual("sha256:abc", "sha256:abc") {
		t.Errorf("identical digests should compare equal")
	}
	if digestsEqual("", "") {
		t.Errorf("empty digests should not compare equal")
	}
	if digestsEqual("sha256:abc", "sha256:def") {
		t.Errorf("different digests should not compare equal")
	}
}

func TestStaleResult(t *testing.T) {
	now := time.Now()
	if !staleResult(nil, now) {
		t.Errorf("nil result should be stale")
	}
	if staleResult(&updateResult{CheckedAt: now, TTL: time.Hour}, now) {
		t.Errorf("fresh result should not be stale")
	}
	if !staleResult(&updateResult{CheckedAt: now.Add(-2 * time.Hour), TTL: time.Hour}, now) {
		t.Errorf("expired result should be stale")
	}
}

func TestUpdateCheckerInfoFromCache(t *testing.T) {
	u := newUpdateChecker(nil, time.Hour, "test")
	ref, _ := parseImageRef("nginx:latest")
	u.store(ref.key(), updateResult{
		Status:        updateStatusAvailable,
		CurrentDigest: "sha256:old",
		LatestDigest:  "sha256:new",
		CheckedAt:     time.Now(),
		TTL:           time.Hour,
	})

	info := u.info("nginx:latest")
	if info.Status != updateStatusAvailable || !info.Available {
		t.Errorf("info = %+v, want update-available/true", info)
	}
	if info.CurrentDigest != "sha256:old" || info.LatestDigest != "sha256:new" {
		t.Errorf("digests = %q/%q", info.CurrentDigest, info.LatestDigest)
	}
	if got := u.info("redis:7"); got.Status != "" || got.Available {
		t.Errorf("uncached info = %+v, want zero value", got)
	}
}

func TestUpdateCheckerStoreDefaultsTTL(t *testing.T) {
	u := newUpdateChecker(nil, 2*time.Hour, "test")
	ref, _ := parseImageRef("nginx:latest")
	u.store(ref.key(), updateResult{Status: updateStatusUpToDate, CheckedAt: time.Now()})

	u.mu.Lock()
	res := u.cache[ref.key()]
	u.mu.Unlock()
	if res == nil {
		t.Fatalf("result not stored")
	}
	if res.TTL != 2*time.Hour {
		t.Errorf("stored TTL = %v, want 2h", res.TTL)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestUpdateCheckEnabled(t *testing.T) {
	cfgTrue := &overrideConfig{UpdateCheck: boolPtr(true)}
	cfgFalse := &overrideConfig{UpdateCheck: boolPtr(false)}

	t.Setenv("DUMBDOCK_UPDATE_CHECK", "false")
	if updateCheckEnabled(cfgTrue) {
		t.Errorf("env false should override config true")
	}
	t.Setenv("DUMBDOCK_UPDATE_CHECK", "true")
	if !updateCheckEnabled(cfgFalse) {
		t.Errorf("env true should override config false")
	}
	t.Setenv("DUMBDOCK_UPDATE_CHECK", "")
	if !updateCheckEnabled(nil) {
		t.Errorf("default should be enabled")
	}
	if !updateCheckEnabled(cfgTrue) {
		t.Errorf("config true should enable")
	}
	if updateCheckEnabled(cfgFalse) {
		t.Errorf("config false should disable")
	}
}

func TestUpdateCheckInterval(t *testing.T) {
	t.Setenv("DUMBDOCK_UPDATE_INTERVAL", "2h")
	if got := updateCheckInterval(nil); got != 2*time.Hour {
		t.Errorf("interval = %v, want 2h", got)
	}
	t.Setenv("DUMBDOCK_UPDATE_INTERVAL", "bogus")
	if got := updateCheckInterval(nil); got != 6*time.Hour {
		t.Errorf("invalid interval = %v, want 6h", got)
	}
	t.Setenv("DUMBDOCK_UPDATE_INTERVAL", "-1h")
	if got := updateCheckInterval(nil); got != 6*time.Hour {
		t.Errorf("non-positive interval = %v, want 6h", got)
	}
}
