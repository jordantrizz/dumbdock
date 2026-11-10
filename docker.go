package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dockerContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []dockerPort      `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
	Mounts  []dockerMount     `json:"Mounts"`
	Created int64             `json:"Created"`
}

// dockerMount is one entry of a container's mount list. Docker reports the
// absolute host source for binds and the volume name for volumes; the compose
// project labels are the only reliable way to tell "data beside the compose
// file" apart from an intended bind, so both are collected together.
type dockerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Driver      string `json:"Driver"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

// dockerInspect is the subset of GET /containers/{id}/json dumbdock reads. It
// carries environment values for database datadirs and the last exit state for
// labeled backup containers; no other field is decoded.
type dockerInspect struct {
	ID     string `json:"Id"`
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status     string `json:"Status"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	Mounts []dockerMount `json:"Mounts"`
}

// Compose label helpers. Callers never index the label map directly so a label
// rename stays a one-file change.
func (c dockerContainer) composeProject() string { return c.Labels["com.docker.compose.project"] }
func (c dockerContainer) composeService() string { return c.Labels["com.docker.compose.service"] }
func (c dockerContainer) composeWorkingDir() string {
	return c.Labels["com.docker.compose.project.working_dir"]
}
func (c dockerContainer) composeConfigFiles() []string {
	raw := strings.TrimSpace(c.Labels["com.docker.compose.project.config_files"])
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// fetchContainerInspect reads one container's detail. The response is bounded
// by the same limit as every other Docker API body.
func fetchContainerInspect(client *http.Client, id string) (dockerInspect, error) {
	var out dockerInspect
	resp, err := client.Get("http://localhost/v1.45/containers/" + url.PathEscape(id) + "/json")
	if err != nil {
		return out, fmt.Errorf("inspect container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("inspect container returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(&out); err != nil {
		return out, fmt.Errorf("decode container inspect: %w", err)
	}
	return out, nil
}

// envValue returns the value of key from a Docker env slice ("KEY=value").
// Secrets are never requested by name; callers ask only for datadir settings.
func envValue(env []string, key string) string {
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			return strings.TrimPrefix(entry, key+"=")
		}
	}
	return ""
}

type dockerImage struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
}

type dockerPort struct {
	IP          string `json:"IP"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort"`
	Type        string `json:"Type"`
}

// dockerVersionResponse is the subset of GET /version dumbdock reports on.
type dockerVersionResponse struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
}

func newDockerClient(socketPath string) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 30 * time.Second,
	}
}

func fetchContainers(client *http.Client, all bool) ([]dockerContainer, error) {
	url := "http://localhost/v1.45/containers/json?all=false"
	if all {
		url = "http://localhost/v1.45/containers/json?all=true"
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}
	return containers, nil
}

func fetchImages(client *http.Client) ([]dockerImage, error) {
	resp, err := client.Get("http://localhost/v1.45/images/json")
	if err != nil {
		return nil, fmt.Errorf("fetch images: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var images []dockerImage
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, fmt.Errorf("decode images: %w", err)
	}
	return images, nil
}

// fetchDockerVersion reads the daemon version from the Docker API.
func fetchDockerVersion(client *http.Client) (dockerVersionResponse, error) {
	var out dockerVersionResponse
	resp, err := client.Get("http://localhost/v1.45/version")
	if err != nil {
		return out, fmt.Errorf("fetch docker version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("docker version returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(&out); err != nil {
		return out, fmt.Errorf("decode docker version: %w", err)
	}
	return out, nil
}

// fetchUnhealthyContainers lists containers whose Docker health status is
// "unhealthy". The filter is evaluated by the daemon; dumbdock never infers
// health from the human-readable Status text.
func fetchUnhealthyContainers(client *http.Client) ([]dockerContainer, error) {
	filters := url.QueryEscape(`{"health":["unhealthy"]}`)
	resp, err := client.Get("http://localhost/v1.45/containers/json?all=true&filters=" + filters)
	if err != nil {
		return nil, fmt.Errorf("fetch unhealthy containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var containers []dockerContainer
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBodyBytes)).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode unhealthy containers: %w", err)
	}
	return containers, nil
}

// maxDockerAPIBodyBytes bounds every Docker API response dumbdock decodes.
const maxDockerAPIBodyBytes = 8 << 20

func formatPorts(ports []dockerPort) string {
	if len(ports) == 0 {
		return ""
	}
	result := ""
	for i, p := range ports {
		if i > 0 {
			result += ", "
		}
		if p.PublicPort > 0 {
			result += fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		} else {
			result += fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		}
	}
	return result
}

func containerName(c dockerContainer) string {
	if len(c.Names) > 0 {
		name := c.Names[0]
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
		return name
	}
	return c.ID[:12]
}
