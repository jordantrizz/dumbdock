package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	Created int64             `json:"Created"`
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
