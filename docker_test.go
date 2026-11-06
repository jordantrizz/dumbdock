package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDockerReleaseFetchOnce(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantTag    string
		wantErrSub string
	}{
		{name: "ok", status: http.StatusOK, body: `{"tag_name":"v27.3.1"}`, wantTag: "v27.3.1"},
		{name: "non-200", status: http.StatusForbidden, body: `{}`, wantErrSub: "status 403"},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, wantErrSub: "decode"},
		{name: "unusable tag", status: http.StatusOK, body: `{"tag_name":""}`, wantErrSub: "unusable tag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "dumbdock/") {
					t.Errorf("User-Agent = %q, want dumbdock/ prefix", got)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c := newDockerReleaseChecker()
			c.url = srv.URL
			got, err := c.fetchOnce()
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("fetchOnce error = %v, want substring %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchOnce: %v", err)
			}
			if got != tt.wantTag {
				t.Errorf("fetchOnce = %q, want %q", got, tt.wantTag)
			}
		})
	}
}

func TestDockerReleaseFetchRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"tag_name":"v1.2.3","padding":"`)
		for i := 0; i < maxDockerReleaseBytes; i++ {
			fmt.Fprint(w, "a")
		}
		fmt.Fprint(w, `"}`)
	}))
	defer srv.Close()

	c := newDockerReleaseChecker()
	c.url = srv.URL
	if _, err := c.fetchOnce(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("fetchOnce error = %v, want an exceeds-size error", err)
	}
}

func TestDockerReleaseRefreshRetainsLastSuccess(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"tag_name":"v27.3.1"}`)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	c := newDockerReleaseChecker()
	c.url = srv.URL
	c.now = func() time.Time { return now }

	if err := c.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if version, _, errText := c.status(); version != "v27.3.1" || errText != "" {
		t.Fatalf("status after success = (%q, %q)", version, errText)
	}

	fail = true
	now = now.Add(time.Hour)
	if err := c.refresh(); err == nil {
		t.Fatal("refresh succeeded against a failing server")
	}
	version, lastSuccess, errText := c.status()
	if version != "v27.3.1" {
		t.Errorf("last successful version was discarded: %q", version)
	}
	if !lastSuccess.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("lastSuccess = %s, want the original success time", lastSuccess)
	}
	if errText == "" {
		t.Error("status error text is empty after a failed refresh")
	}
}

// rewriteTransport redirects the hardcoded Docker API URL to an httptest
// server while keeping the request path and query intact.
type rewriteTransport struct {
	target string
}

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tgt, err := url.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	clone := r.Clone(r.Context())
	clone.URL.Scheme = tgt.Scheme
	clone.URL.Host = tgt.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestFetchDockerVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.45/version" {
			t.Errorf("path = %q, want /v1.45/version", r.URL.Path)
		}
		json.NewEncoder(w).Encode(dockerVersionResponse{
			Version:    "27.3.1",
			APIVersion: "1.45",
			Os:         "linux",
			Arch:       "amd64",
		})
	}))
	defer srv.Close()

	client := &http.Client{Transport: rewriteTransport{target: srv.URL}, Timeout: 5 * time.Second}
	got, err := fetchDockerVersion(client)
	if err != nil {
		t.Fatalf("fetchDockerVersion: %v", err)
	}
	if got.Version != "27.3.1" || got.APIVersion != "1.45" {
		t.Errorf("fetchDockerVersion = %+v", got)
	}
}

func TestFetchDockerVersionErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &http.Client{Transport: rewriteTransport{target: srv.URL}, Timeout: 5 * time.Second}
	if _, err := fetchDockerVersion(client); err == nil {
		t.Fatal("fetchDockerVersion returned no error for a 500 response")
	}
}

func TestFetchUnhealthyContainers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.45/containers/json" {
			t.Errorf("path = %q, want /v1.45/containers/json", r.URL.Path)
		}
		if got := r.URL.Query().Get("filters"); !strings.Contains(got, `"health"`) || !strings.Contains(got, "unhealthy") {
			t.Errorf("filters = %q, want an unhealthy health filter", got)
		}
		json.NewEncoder(w).Encode([]dockerContainer{
			{ID: "0123456789abcdef", Names: []string{"/api"}, State: "running", Image: "api:1"},
		})
	}))
	defer srv.Close()

	client := &http.Client{Transport: rewriteTransport{target: srv.URL}, Timeout: 5 * time.Second}
	got, err := fetchUnhealthyContainers(client)
	if err != nil {
		t.Fatalf("fetchUnhealthyContainers: %v", err)
	}
	if len(got) != 1 || got[0].State != "running" {
		t.Errorf("fetchUnhealthyContainers = %+v", got)
	}
}

func TestFetchUnhealthyContainersErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := &http.Client{Transport: rewriteTransport{target: srv.URL}, Timeout: 5 * time.Second}
	if _, err := fetchUnhealthyContainers(client); err == nil {
		t.Fatal("fetchUnhealthyContainers returned no error for a 502 response")
	}
}
