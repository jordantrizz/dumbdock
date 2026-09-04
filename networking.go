package main

import (
	"encoding/json"
	"fmt"
	"log"
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
	// ExposedPublic is true when at least one published port is bound to
	// 0.0.0.0 (or the IPv6 wildcard ::), i.e. reachable on all interfaces.
	ExposedPublic bool `json:"exposedPublic,omitempty"`
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
	// PublicExposed lists the names of running containers that publish ports
	// on 0.0.0.0 (all interfaces), for the networking page warning banner.
	PublicExposed []string `json:"publicExposed,omitempty"`
}

// fetchNetworks returns the list of Docker networks from the Docker API.
// The list endpoint omits the Containers map, so each network's detail is
// fetched to populate the attached containers and their IPs.
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

	for i := range networks {
		detail, err := fetchNetworkDetail(client, networks[i].ID)
		if err != nil {
			log.Printf("warning: fetch network detail %s: %v", networks[i].Name, err)
			continue
		}
		networks[i].Containers = detail.Containers
	}
	return networks, nil
}

// fetchNetworkDetail returns a single network's detail, which includes the
// Containers map (the list endpoint leaves it null).
func fetchNetworkDetail(client *http.Client, id string) (dockerNetwork, error) {
	resp, err := client.Get("http://localhost/v1.45/networks/" + id)
	if err != nil {
		return dockerNetwork{}, fmt.Errorf("fetch network detail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return dockerNetwork{}, fmt.Errorf("docker API returned status %d", resp.StatusCode)
	}

	var network dockerNetwork
	if err := json.NewDecoder(resp.Body).Decode(&network); err != nil {
		return dockerNetwork{}, fmt.Errorf("decode network detail: %w", err)
	}
	return network, nil
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

	// Track running containers that publish ports on 0.0.0.0 (all interfaces).
	exposedSet := make(map[string]bool)

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
			exposed := hasPublicExposure(c.Ports)
			if exposed {
				exposedSet[containerName(c)] = true
			}
			group.Containers = append(group.Containers, networkContainer{
				Name:          containerName(c),
				ID:            c.ID[:12],
				Image:         c.Image,
				State:         c.State,
				IP:            cleanIP(member.IPv4Address),
				Ports:         toNetworkPorts(c.Ports),
				ExposedPublic: exposed,
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
			exposed := hasPublicExposure(c.Ports)
			if exposed {
				exposedSet[containerName(c)] = true
			}
			none = append(none, networkContainer{
				Name:          containerName(c),
				ID:            c.ID[:12],
				Image:         c.Image,
				State:         c.State,
				Ports:         toNetworkPorts(c.Ports),
				ExposedPublic: exposed,
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

	// Sorted list of containers exposed on all interfaces for the warning banner.
	publicExposed := make([]string, 0, len(exposedSet))
	for name := range exposedSet {
		publicExposed = append(publicExposed, name)
	}
	sort.Strings(publicExposed)

	return networkingResponse{Running: running, Stopped: stopped, PublicExposed: publicExposed}
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

// isExposedOnAllInterfaces reports whether a port binding IP exposes the port
// on all host interfaces. Docker reports an unset IP as 0.0.0.0; "::" is the
// IPv6 wildcard equivalent.
func isExposedOnAllInterfaces(ip string) bool {
	return ip == "" || ip == "0.0.0.0" || ip == "::"
}

// hasPublicExposure reports whether any published port of the container is
// bound to 0.0.0.0 (all interfaces).
func hasPublicExposure(ports []dockerPort) bool {
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		if isExposedOnAllInterfaces(p.IP) {
			return true
		}
	}
	return false
}
