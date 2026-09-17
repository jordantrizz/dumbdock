package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Update status values exposed on containerCard.updateStatus.
const (
	updateStatusUpToDate  = "up-to-date"
	updateStatusAvailable = "update-available"
	updateStatusUnknown   = "unknown"
	updateStatusError     = "error"
	updateStatusPinned    = "pinned"
)

// Supported registry hosts and their canonical Docker Hub identity.
const (
	dockerHubHost = "registry-1.docker.io"
	dockerHubName = "docker.io"
	ghcrHost      = "ghcr.io"

	// manifestAccept requests the multi-arch index first, then the platform
	// manifest types, so the registry's Docker-Content-Digest is the same
	// digest Docker stores in the local image's RepoDigests.
	manifestAccept = "application/vnd.oci.image.index.v1+json, " +
		"application/vnd.docker.distribution.manifest.list.v2+json, " +
		"application/vnd.docker.distribution.manifest.v2+json, " +
		"application/vnd.oci.image.manifest.v1+json"

	maxTokenBytes    = 1 << 20 // 1 MiB
	maxManifestBytes = 4 << 20 // 4 MiB
)

// imageRef is a parsed Docker image reference.
type imageRef struct {
	Registry string // canonical registry host, e.g. registry-1.docker.io or ghcr.io
	Repo     string // repository path with namespace, e.g. library/nginx
	Tag      string // tag, e.g. latest (empty when pinned by digest)
	Pinned   bool   // true when the reference uses @sha256:...
	Raw      string // original reference
}

// key returns a stable cache key for the reference.
func (r imageRef) key() string {
	if r.Pinned {
		return r.Registry + "/" + r.Repo + "@pinned"
	}
	return r.Registry + "/" + r.Repo + ":" + r.Tag
}

// supported reports whether the reference targets a registry dumbdock knows
// how to query anonymously (Docker Hub or GHCR).
func (r imageRef) supported() bool {
	return r.Registry == dockerHubHost || r.Registry == ghcrHost
}

// parseImageRef parses a Docker image reference into its registry, repository,
// and tag components. Docker Hub references (bare names plus the docker.io and
// index.docker.io aliases) are normalized to registry-1.docker.io with the
// library/ namespace added for single-component names. References pinned by
// digest are flagged Pinned with an empty Tag. ok is false only when the
// reference is empty or malformed.
func parseImageRef(image string) (ref imageRef, ok bool) {
	raw := strings.TrimSpace(image)
	if raw == "" {
		return imageRef{}, false
	}
	ref.Raw = raw

	name := raw
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
		ref.Pinned = true
	}

	registry := ""
	repo := name
	if slash := strings.Index(name, "/"); slash >= 0 {
		first := name[:slash]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry = first
			repo = name[slash+1:]
		}
	}

	tag := ""
	if !ref.Pinned {
		if colon := strings.LastIndex(repo, ":"); colon >= 0 {
			tag = repo[colon+1:]
			repo = repo[:colon]
		}
		if tag == "" {
			tag = "latest"
		}
	}

	repo = strings.Trim(repo, "/")
	if repo == "" || strings.ContainsAny(repo, " \t") || strings.ContainsAny(registry, " \t") {
		return imageRef{}, false
	}

	switch registry {
	case "", dockerHubName, dockerHubHost, "index.docker.io":
		ref.Registry = dockerHubHost
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	default:
		ref.Registry = registry
	}

	ref.Repo = strings.ToLower(repo)
	ref.Tag = tag
	return ref, true
}

// tokenURL returns the anonymous pull-token endpoint for a supported registry.
// ok is false when the reference's registry is not supported.
func tokenURL(ref imageRef) (string, bool) {
	switch ref.Registry {
	case dockerHubHost:
		return "https://auth.docker.io/token?service=registry.docker.io&scope=repository:" +
			url.QueryEscape(ref.Repo) + ":pull", true
	case ghcrHost:
		return "https://ghcr.io/token?service=ghcr.io&scope=repository:" +
			url.QueryEscape(ref.Repo) + ":pull", true
	}
	return "", false
}

// manifestURL returns the registry v2 manifest endpoint for the reference's tag.
func manifestURL(ref imageRef) string {
	return "https://" + ref.Registry + "/v2/" + ref.Repo + "/manifests/" + url.PathEscape(ref.Tag)
}

type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// fetchAnonymousToken requests an anonymous pull token from a registry token
// endpoint. It returns the bearer token and a conservative expiry time.
func fetchAnonymousToken(ctx context.Context, hc *http.Client, endpoint string) (string, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxTokenBytes))
		return "", time.Time{}, fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBytes))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("decode token: %w", err)
	}
	token := tr.Token
	if token == "" {
		token = tr.AccessToken
	}
	if token == "" {
		return "", time.Time{}, fmt.Errorf("token endpoint returned an empty token")
	}

	expiry := time.Now().Add(5 * time.Minute)
	if tr.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - time.Minute)
	}
	return token, expiry, nil
}

// fetchManifestDigest resolves the current manifest digest for a tag. It issues
// a HEAD request and reads Docker-Content-Digest; when a registry omits that
// header it falls back to GET and hashes the manifest body.
func fetchManifestDigest(ctx context.Context, hc *http.Client, endpoint, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", manifestAccept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("manifest request: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxTokenBytes))
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if d := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest")); d != "" {
			return d, nil
		}
	case http.StatusUnauthorized:
		return "", fmt.Errorf("registry returned 401 unauthorized")
	case http.StatusNotFound:
		return "", fmt.Errorf("tag not found in registry")
	default:
		return "", fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	// Fallback: some registries only expose the digest on GET.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	getReq.Header.Set("Accept", manifestAccept)
	if token != "" {
		getReq.Header.Set("Authorization", "Bearer "+token)
	}
	getResp, err := hc.Do(getReq)
	if err != nil {
		return "", fmt.Errorf("manifest request: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(getResp.Body, maxTokenBytes))
		return "", fmt.Errorf("registry returned status %d", getResp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(getResp.Body, maxManifestBytes+1))
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	if len(body) > maxManifestBytes {
		return "", fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalRepoRepo normalizes a RepoDigest repository (the part before "@")
// into a canonical registry host and repository path.
func canonicalRepoRepo(repo string) (registry, path string) {
	registry = ""
	path = repo
	if slash := strings.Index(repo, "/"); slash >= 0 {
		first := repo[:slash]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry = first
			path = repo[slash+1:]
		}
	}
	switch registry {
	case "", dockerHubName, dockerHubHost, "index.docker.io":
		if !strings.Contains(path, "/") {
			path = "library/" + path
		}
		return dockerHubHost, strings.ToLower(path)
	default:
		return registry, strings.ToLower(path)
	}
}

// localDigestForRepo finds the manifest digest for ref among a list of Docker
// RepoDigests (e.g. "nginx@sha256:..."). It returns "" when no entry matches.
func localDigestForRepo(repoDigests []string, ref imageRef) string {
	for _, rd := range repoDigests {
		at := strings.Index(rd, "@")
		if at < 0 {
			continue
		}
		registry, path := canonicalRepoRepo(rd[:at])
		if registry == ref.Registry && path == ref.Repo {
			return strings.TrimSpace(rd[at+1:])
		}
	}
	return ""
}

// digestsEqual reports whether two non-empty manifest digests match.
func digestsEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && a == b
}

const (
	// updateScanInterval is how often running containers are rescanned for
	// images whose cached result has expired.
	updateScanInterval = time.Minute
	// updateErrorTTL is how long a failed check is cached before retrying.
	updateErrorTTL = 15 * time.Minute
	// updateMaxConcurrency caps simultaneous registry requests.
	updateMaxConcurrency = 4
)

// updateInfo is the cached, UI-ready outcome for one image reference.
type updateInfo struct {
	Status        string
	Available     bool
	CurrentDigest string
	LatestDigest  string
}

// updateResult is a cached digest-check outcome for a single image tag.
type updateResult struct {
	Status        string
	CurrentDigest string
	LatestDigest  string
	CheckedAt     time.Time
	TTL           time.Duration
}

// updateTarget is one unique image tag to check with its local manifest digest.
type updateTarget struct {
	Ref         imageRef
	LocalDigest string
}

type tokenEntry struct {
	token  string
	expiry time.Time
}

// updateChecker resolves remote image digests in the background and caches the
// results so the HTTP API never blocks on registry I/O.
type updateChecker struct {
	dockerClient *http.Client
	httpClient   *http.Client
	ttl          time.Duration

	mu     sync.Mutex
	cache  map[string]*updateResult
	tokens map[string]tokenEntry
}

// userAgentTransport stamps a descriptive User-Agent on outgoing requests.
type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.ua != "" && req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.ua)
	}
	return t.base.RoundTrip(req)
}

// newUpdateChecker builds a checker that talks to the Docker socket and public
// registries. ttl controls how long a successful result is cached.
func newUpdateChecker(dockerClient *http.Client, ttl time.Duration, appVersion string) *updateChecker {
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return &updateChecker{
		dockerClient: dockerClient,
		httpClient: &http.Client{
			Timeout:   20 * time.Second,
			Transport: userAgentTransport{base: http.DefaultTransport, ua: "dumbdock/" + appVersion},
		},
		ttl:    ttl,
		cache:  make(map[string]*updateResult),
		tokens: make(map[string]tokenEntry),
	}
}

// info returns the cached update state for an image reference. An empty Status
// means the image has not been checked yet (or checking is disabled).
func (u *updateChecker) info(image string) updateInfo {
	ref, ok := parseImageRef(image)
	if !ok {
		return updateInfo{}
	}
	u.mu.Lock()
	res := u.cache[ref.key()]
	u.mu.Unlock()
	if res == nil {
		return updateInfo{}
	}
	return updateInfo{
		Status:        res.Status,
		Available:     res.Status == updateStatusAvailable,
		CurrentDigest: res.CurrentDigest,
		LatestDigest:  res.LatestDigest,
	}
}

// run scans immediately, then rescans on a fixed interval until ctx is done.
func (u *updateChecker) run(ctx context.Context) {
	u.refresh(ctx)
	ticker := time.NewTicker(updateScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.refresh(ctx)
		}
	}
}

// refresh prunes cache entries for images that are no longer running and checks
// every target whose cached result has expired.
func (u *updateChecker) refresh(ctx context.Context) {
	targets := u.scanTargets()
	if len(targets) == 0 {
		return
	}

	now := time.Now()
	u.mu.Lock()
	for key := range u.cache {
		if _, ok := targets[key]; !ok {
			delete(u.cache, key)
		}
	}
	var stale []updateTarget
	for key, t := range targets {
		if staleResult(u.cache[key], now) {
			stale = append(stale, t)
		}
	}
	u.mu.Unlock()

	if len(stale) == 0 {
		return
	}

	sem := make(chan struct{}, updateMaxConcurrency)
	var wg sync.WaitGroup
	for _, t := range stale {
		wg.Add(1)
		sem <- struct{}{}
		go func(t updateTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			u.check(ctx, t)
		}(t)
	}
	wg.Wait()
}

// staleResult reports whether a cached result is missing or past its TTL.
func staleResult(res *updateResult, now time.Time) bool {
	return res == nil || now.Sub(res.CheckedAt) >= res.TTL
}

// scanTargets lists the running containers' image tags alongside their local
// manifest digests.
func (u *updateChecker) scanTargets() map[string]updateTarget {
	containers, err := fetchContainers(u.dockerClient, false)
	if err != nil {
		log.Printf("update check: fetch containers: %v", err)
		return nil
	}
	images, err := fetchImages(u.dockerClient)
	if err != nil {
		log.Printf("update check: fetch images: %v", err)
	}

	byID := make(map[string]dockerImage, len(images))
	for _, im := range images {
		byID[im.ID] = im
	}

	targets := make(map[string]updateTarget)
	for _, c := range containers {
		ref, ok := parseImageRef(c.Image)
		if !ok || ref.Pinned {
			continue
		}
		local := ""
		if im, found := byID[c.ImageID]; found {
			local = localDigestForRepo(im.RepoDigests, ref)
		}
		targets[ref.key()] = updateTarget{Ref: ref, LocalDigest: local}
	}
	return targets
}

// check resolves the remote digest for one target and caches the outcome.
func (u *updateChecker) check(ctx context.Context, t updateTarget) {
	res := updateResult{
		Status:    updateStatusUnknown,
		CheckedAt: time.Now(),
		TTL:       u.ttl,
	}

	if !t.Ref.supported() {
		u.store(t.Ref.key(), res)
		return
	}
	if t.LocalDigest == "" {
		u.store(t.Ref.key(), res)
		return
	}

	token, err := u.bearerToken(ctx, t.Ref)
	if err != nil {
		log.Printf("update check: %s: %v", t.Ref.key(), err)
		res.Status = updateStatusError
		res.TTL = updateErrorTTL
		u.store(t.Ref.key(), res)
		return
	}

	digest, err := fetchManifestDigest(ctx, u.httpClient, manifestURL(t.Ref), token)
	if err != nil {
		log.Printf("update check: %s: %v", t.Ref.key(), err)
		res.Status = updateStatusError
		res.TTL = updateErrorTTL
		u.store(t.Ref.key(), res)
		return
	}

	res.CurrentDigest = t.LocalDigest
	res.LatestDigest = digest
	if digestsEqual(t.LocalDigest, digest) {
		res.Status = updateStatusUpToDate
	} else {
		res.Status = updateStatusAvailable
	}
	u.store(t.Ref.key(), res)
}

// bearerToken returns a cached (or freshly fetched) anonymous pull token.
func (u *updateChecker) bearerToken(ctx context.Context, ref imageRef) (string, error) {
	endpoint, ok := tokenURL(ref)
	if !ok {
		return "", fmt.Errorf("unsupported registry %q", ref.Registry)
	}
	key := ref.Registry + "/" + ref.Repo

	u.mu.Lock()
	entry, found := u.tokens[key]
	u.mu.Unlock()
	if found && time.Now().Before(entry.expiry) {
		return entry.token, nil
	}

	token, expiry, err := fetchAnonymousToken(ctx, u.httpClient, endpoint)
	if err != nil {
		return "", err
	}
	u.mu.Lock()
	u.tokens[key] = tokenEntry{token: token, expiry: expiry}
	u.mu.Unlock()
	return token, nil
}

// store caches a check result, defaulting to the checker TTL when unset.
func (u *updateChecker) store(key string, res updateResult) {
	if res.TTL <= 0 {
		res.TTL = u.ttl
	}
	u.mu.Lock()
	u.cache[key] = &res
	u.mu.Unlock()
}
