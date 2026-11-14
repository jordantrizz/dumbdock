package main

import (
	"testing"
	"time"
)

func TestParseDockerEngineVersion(t *testing.T) {
	tests := []struct {
		in   string
		want dockerSemver
		ok   bool
	}{
		{in: "27.3.1", want: dockerSemver{Major: 27, Minor: 3, Patch: 1}, ok: true},
		{in: "v27.3.1", want: dockerSemver{Major: 27, Minor: 3, Patch: 1}, ok: true},
		{in: "V27.3.1", want: dockerSemver{Major: 27, Minor: 3, Patch: 1}, ok: true},
		{in: "27.3", want: dockerSemver{Major: 27, Minor: 3}, ok: true},
		{in: "20.10.24-ce", want: dockerSemver{Major: 20, Minor: 10, Patch: 24}, ok: true},
		{in: "24.0.5+dfsg1", want: dockerSemver{Major: 24, Minor: 0, Patch: 5}, ok: true},
		{in: "27.0.0-rc1", want: dockerSemver{Major: 27, Prerelease: "rc1"}, ok: true},
		{in: "27.0.0-beta.2", want: dockerSemver{Major: 27, Prerelease: "beta.2"}, ok: true},
		{in: " 26.1.4  ", want: dockerSemver{Major: 26, Minor: 1, Patch: 4}, ok: true},
		{in: "", ok: false},
		{in: "abc", ok: false},
		{in: "1.2.3.4", ok: false},
		{in: "1.x.3", ok: false},
		{in: "v", ok: false},
	}

	for _, tt := range tests {
		got, ok := parseDockerEngineVersion(tt.in)
		if ok != tt.ok {
			t.Errorf("parseDockerEngineVersion(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseDockerEngineVersion(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestCompareDockerVersions(t *testing.T) {
	must := func(s string) dockerSemver {
		t.Helper()
		v, ok := parseDockerEngineVersion(s)
		if !ok {
			t.Fatalf("parseDockerEngineVersion(%q) failed", s)
		}
		return v
	}

	tests := []struct {
		a, b string
		want int
	}{
		{"27.3.1", "27.3.1", 0},
		{"27.3.1", "v27.3.1", 0},
		{"27.3.1", "27.3.0", 1},
		{"27.2.9", "27.3.0", -1},
		{"26.0.0", "27.0.0", -1},
		{"27.0.0", "27.0.0-rc1", 1},
		{"27.0.0-rc1", "27.0.0", -1},
		{"27.0.0-rc2", "27.0.0-rc1", 1},
		{"20.10.24-ce", "20.10.24", 0},
		{"24.0.5+dfsg1", "24.0.5", 0},
	}
	for _, tt := range tests {
		if got := compareDockerVersions(must(tt.a), must(tt.b)); got != tt.want {
			t.Errorf("compareDockerVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDockerReleaseNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		lastSuccess time.Time
		lastAttempt time.Time
		fetching    bool
		want        bool
	}{
		{name: "never checked", want: true},
		{name: "fresh success", lastSuccess: now.Add(-time.Hour), lastAttempt: now.Add(-time.Hour), want: false},
		{name: "expired success", lastSuccess: now.Add(-dockerReleaseSuccessTTL), lastAttempt: now.Add(-dockerReleaseSuccessTTL), want: true},
		{name: "recent failure", lastAttempt: now.Add(-time.Minute), want: false},
		{name: "old failure", lastAttempt: now.Add(-dockerReleaseFailureTTL), want: true},
		{name: "fetch in flight", fetching: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &dockerReleaseChecker{now: func() time.Time { return now }}
			c.lastSuccess = tt.lastSuccess
			c.lastAttempt = tt.lastAttempt
			c.fetching = tt.fetching
			if got := c.needsRefresh(now); got != tt.want {
				t.Errorf("needsRefresh = %v, want %v", got, tt.want)
			}
		})
	}
}
