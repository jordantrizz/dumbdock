package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type dockerContainer struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []dockerPort      `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
	Created int64             `json:"Created"`
}

type dockerPort struct {
	IP          string `json:"IP"`
	PrivatePort uint16 `json:"PrivatePort"`
	PublicPort  uint16 `json:"PublicPort"`
	Type        string `json:"Type"`
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

func fetchContainers(client *http.Client) ([]dockerContainer, error) {
	resp, err := client.Get("http://localhost/v1.45/containers/json?all=false")
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
