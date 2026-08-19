package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// dockerNetwork mirrors the Docker API's network object (GET /v1.45/networks).
type dockerNetwork struct {
	Name       string                     `json:"Name"`
	ID         string                     `json:"Id"`
	Driver     string                     `json:"Driver"`
	Scope      string                     `json:"Scope"`
	Containers map[string]dockerNetMember `json:"Containers"`
}

// dockerNetMember is a single container attachment within a network.
type dockerNetMember struct {
	Name        string `json:"Name"`
	IPv4Address string `json:"IPv4Address"`
	IPv6Address string `json:"IPv6Address"`
}

// networkPort is a single port mapping for a container in the networking view.
// Internal is the container-side port (PrivatePort); External is the published
// host port (PublicPort, 0 when not published).
type networkPort struct {
	Internal uint16 `json:"internal"`
	External uint16 `json:"external"`
	Type     string `json:"type"`
	HostIP   string `json:"hostIP,omitempty"`
}

// networkContainer is a container as shown in the networking view. It carries
// no Docker labels (AGENTS.md M-01) — only identity, state, network IP, and ports.
type networkContainer struct {
	Name  string        `json:"name"`
	ID    string        `json:"id"`
	Image string        `json:"image"`
	State string        `json:"state"`
	IP    string        `json:"ip,omitempty"`
	Ports []networkPort `json:"ports"`
}

// networkGroup is a network with the running containers attached to it.
type networkGroup struct {
	Name       string             `json:"name"`
	Driver     string             `json:"driver"`
	Containers []networkContainer `json:"containers"`
}

// networkingResponse is the payload of GET /api/networking.
type networkingResponse struct {
	Running []networkGroup     `json:"running"`
	Stopped []networkContainer `json:"stopped"`
}

// fetchNetworks returns the list of Docker networks from the Docker API.
func fetchNetworks(client *http.Client) ([]dockerNetwork, error) {
	resp, err := client.Get("http://localhost/v1.45/networks")
	if err != nil {
		return nil, fmt.Errorf("fetch networks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var networks []dockerNetwork
	if err := json.NewDecoder(resp.Body).Decode(&networks); err != nil {
		return nil, fmt.Errorf("decode networks: %w", err)
	}
	return networks, nil
}

// buildNetworkingData groups running containers under each network they are
// attached to (using the network's Containers map for IPs and the container's
// Ports for port mappings) and collects non-running containers into a flat
// Stopped list. Running containers not attached to any listed network
// (network_mode: none) are grouped under a "none" group.
func buildNetworkingData(containers []dockerContainer, networks []dockerNetwork) networkingResponse {
	// Index running containers by ID; collect the rest as stopped.
	byID := make(map[string]dockerContainer)
	var stopped []networkContainer
	for _, c := range containers {
		if c.State == "running" {
			byID[c.ID] = c
		} else {
			stopped = append(stopped, networkContainer{
				Name:  containerName(c),
				ID:    c.ID[:12],
				Image: c.Image,
				State: c.State,
				Ports: []networkPort{},
			})
		}
	}

	// Group running containers under each network they are attached to.
	seen := make(map[string]bool)
	var running []networkGroup
	for _, n := range networks {
		if len(n.Containers) == 0 {
			continue
		}
		group := networkGroup{Name: n.Name, Driver: n.Driver}
		for cid, member := range n.Containers {
			c, ok := byID[cid]
			if !ok {
				continue
			}
			seen[cid] = true
			group.Containers = append(group.Containers, networkContainer{
				Name:  containerName(c),
				ID:    c.ID[:12],
				Image: c.Image,
				State: c.State,
				IP:    cleanIP(member.IPv4Address),
				Ports: toNetworkPorts(c.Ports),
			})
		}
		if len(group.Containers) > 0 {
			sort.Slice(group.Containers, func(i, j int) bool {
				return strings.ToLower(group.Containers[i].Name) < strings.ToLower(group.Containers[j].Name)
			})
			running = append(running, group)
		}
	}

	// Running containers not attached to any listed network (network_mode: none).
	var none []networkContainer
	for cid, c := range byID {
		if !seen[cid] {
			none = append(none, networkContainer{
				Name:  containerName(c),
				ID:    c.ID[:12],
				Image: c.Image,
				State: c.State,
				Ports: toNetworkPorts(c.Ports),
			})
		}
	}
	if len(none) > 0 {
		sort.Slice(none, func(i, j int) bool {
			return strings.ToLower(none[i].Name) < strings.ToLower(none[j].Name)
		})
		running = append(running, networkGroup{Name: "none", Containers: none})
	}

	sort.Slice(running, func(i, j int) bool {
		return strings.ToLower(running[i].Name) < strings.ToLower(running[j].Name)
	})
	sort.Slice(stopped, func(i, j int) bool {
		return strings.ToLower(stopped[i].Name) < strings.ToLower(stopped[j].Name)
	})

	return networkingResponse{Running: running, Stopped: stopped}
}

// toNetworkPorts converts Docker API port mappings into the networking view's
// port structs (PrivatePort → Internal, PublicPort → External).
func toNetworkPorts(ports []dockerPort) []networkPort {
	if len(ports) == 0 {
		return []networkPort{}
	}
	out := make([]networkPort, 0, len(ports))
	for _, p := range ports {
		out = append(out, networkPort{
			Internal: p.PrivatePort,
			External: p.PublicPort,
			Type:     p.Type,
			HostIP:   p.IP,
		})
	}
	return out
}

// cleanIP strips the CIDR suffix (e.g. "172.17.0.2/16" → "172.17.0.2").
func cleanIP(addr string) string {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}
