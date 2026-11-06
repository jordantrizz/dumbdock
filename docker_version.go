package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dockerReleaseURL        = "https://api.github.com/repos/moby/moby/releases/latest"
	dockerReleaseSuccessTTL = 24 * time.Hour
	dockerReleaseFailureTTL = 15 * time.Minute
	maxDockerReleaseBytes   = 1 << 20
	dockerReleaseTimeout    = 10 * time.Second
)

// dockerSemver is the numeric part of a Docker Engine version plus an optional
// pre-release marker.
type dockerSemver struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

// parseDockerEngineVersion normalizes a daemon or release version string. It
// tolerates a leading "v", a missing patch component, build metadata after
// "+", and vendor suffixes such as "-ce" or "-dfsg1" that do not describe a
// pre-release.
func parseDockerEngineVersion(raw string) (dockerSemver, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if s == "" {
		return dockerSemver{}, false
	}

	// Drop build metadata (e.g. "+dfsg1").
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	suffix := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		suffix = s[i+1:]
		s = s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return dockerSemver{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return dockerSemver{}, false
		}
		nums[i] = n
	}

	pre := ""
	lower := strings.ToLower(suffix)
	switch {
	case strings.HasPrefix(lower, "rc"):
		pre = lower
	case strings.HasPrefix(lower, "alpha"):
		pre = lower
	case strings.HasPrefix(lower, "beta"):
		pre = lower
	case strings.HasPrefix(lower, "preview"):
		pre = lower
	}

	return dockerSemver{Major: nums[0], Minor: nums[1], Patch: nums[2], Prerelease: pre}, true
}

// compareDockerVersions returns -1, 0, or 1 comparing a to b. A release sorts
// after any pre-release of the same numeric version.
func compareDockerVersions(a, b dockerSemver) int {
	if a.Major != b.Major {
		return sign(a.Major - b.Major)
	}
	if a.Minor != b.Minor {
		return sign(a.Minor - b.Minor)
	}
	if a.Patch != b.Patch {
		return sign(a.Patch - b.Patch)
	}
	switch {
	case a.Prerelease == b.Prerelease:
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	case a.Prerelease < b.Prerelease:
		return -1
	default:
		return 1
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// dockerReleaseChecker caches the latest stable Moby release version. Network
// I/O only happens from startRefresh, which the poll loop calls; readers see
// the last cached value and never block on the network.
type dockerReleaseChecker struct {
	client *http.Client
	url    string
	now    func() time.Time

	mu            sync.RWMutex
	version       string
	lastSuccess   time.Time
	lastAttempt   time.Time
	lastErr       string
	fetching      bool
}

func newDockerReleaseChecker() *dockerReleaseChecker {
	return &dockerReleaseChecker{
		client: &http.Client{Timeout: dockerReleaseTimeout},
		url:    dockerReleaseURL,
		now:    time.Now,
	}
}

// startRefresh triggers a background refresh when the cached value is stale.
// It never performs I/O on the calling goroutine.
func (c *dockerReleaseChecker) startRefresh() {
	c.mu.Lock()
	if !c.needsRefresh(c.now()) {
		c.mu.Unlock()
		return
	}
	c.fetching = true
	c.mu.Unlock()

	go func() {
		_ = c.refresh()
	}()
}

// needsRefresh reports whether the cache is due for a fetch. The caller must
// hold c.mu. Successful results live for 24 hours; failures are retried after
// a shorter, bounded interval.
func (c *dockerReleaseChecker) needsRefresh(now time.Time) bool {
	if c.fetching {
		return false
	}
	if !c.lastSuccess.IsZero() {
		return now.Sub(c.lastSuccess) >= dockerReleaseSuccessTTL
	}
	if !c.lastAttempt.IsZero() {
		return !now.Before(c.lastAttempt.Add(dockerReleaseFailureTTL))
	}
	return true
}

// refresh performs one fetch and updates the cache. It is safe to call
// synchronously (tests do) and is what startRefresh runs in the background.
func (c *dockerReleaseChecker) refresh() error {
	version, err := c.fetchOnce()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetching = false
	c.lastAttempt = c.now()
	if err != nil {
		// Keep the last successful version: an upstream blip must not erase a
		// known comparison point.
		c.lastErr = err.Error()
		return err
	}
	c.version = version
	c.lastSuccess = c.lastAttempt
	c.lastErr = ""
	return nil
}

// status returns the cached latest stable version, when it was last verified,
// and the last sanitized error (if any). It never performs I/O.
func (c *dockerReleaseChecker) status() (version string, lastSuccess time.Time, errText string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version, c.lastSuccess, c.lastErr
}

// fetchOnce performs one bounded request to the release feed.
func (c *dockerReleaseChecker) fetchOnce() (string, error) {
	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return "", fmt.Errorf("build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dumbdock/"+appVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch docker release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docker release feed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDockerReleaseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read docker release: %w", err)
	}
	if len(body) > maxDockerReleaseBytes {
		return "", fmt.Errorf("docker release response exceeds %d bytes", maxDockerReleaseBytes)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode docker release: %w", err)
	}
	tag := strings.TrimSpace(payload.TagName)
	if _, ok := parseDockerEngineVersion(tag); !ok {
		return "", fmt.Errorf("docker release feed returned an unusable tag %q", truncateForLog(tag, 32))
	}
	return tag, nil
}

// truncateForLog bounds an untrusted string before it reaches an error message.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
