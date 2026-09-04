package main

import (
	"strconv"
	"strings"
)

const prefix = "dumbdock."

type containerCard struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Icon        string `json:"icon"`
	Href        string `json:"href"`
	Description string `json:"description"`
	Order       int    `json:"order"`

	// ComposeProject is extracted from com.docker.compose.project label.
	ComposeProject string `json:"composeProject,omitempty"`

	// ServiceName is the compose service name (com.docker.compose.service label).
	ServiceName string `json:"serviceName,omitempty"`

	// DependsOn is the list of compose service names this container depends on,
	// parsed from the com.docker.compose.depends_on label.
	DependsOn []string `json:"dependsOn,omitempty"`

	ContainerID   string            `json:"containerId"`
	ContainerName string            `json:"containerName"`
	Image         string            `json:"image"`
	Status        string            `json:"status"`
	State         string            `json:"state"`
	Ports         string            `json:"ports"`
	Created       int64             `json:"created"`
	Labels        map[string]string `json:"labels"`
	HasLabels     bool              `json:"hasLabels"`

	AutoDetectFailedReason string `json:"autoDetectFailedReason,omitempty"`

	// Network warnings
	HasPublicBinding  bool     `json:"hasPublicBinding"`
	PublicBindingIPs  []string `json:"publicBindingIPs,omitempty"`
	HasPrivateBinding bool     `json:"hasPrivateBinding"`
	PrivateBindingIPs []string `json:"privateBindingIPs,omitempty"`
	HasLocalBinding   bool     `json:"hasLocalBinding"`
	LocalBindingIPs   []string `json:"localBindingIPs,omitempty"`
	TraefikEnabled    bool     `json:"traefikEnabled"`
	TraefikURLs       []string `json:"traefikURLs,omitempty"`
}

func parseLabels(labels map[string]string) containerCard {
	card := containerCard{HasLabels: false}

	for k, v := range labels {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		card.HasLabels = true
		key := strings.TrimPrefix(k, prefix)

		switch key {
		case "name":
			card.Name = v
		case "group":
			card.Group = v
		case "icon":
			card.Icon = v
		case "href":
			card.Href = v
		case "description":
			card.Description = v
		case "order":
			if n, err := strconv.Atoi(v); err == nil {
				card.Order = n
			}
		}
	}

	if !card.HasLabels {
		card.Name = ""
	}

	return card
}

// parseDependsOn parses the com.docker.compose.depends_on label value into a
// list of service names. The label is a comma-separated list of
// "service:condition:restart" entries (e.g.
// "db:service_started:false,redis:service_healthy:true"). Only the service
// name (the part before the first ":") is kept; empty entries are dropped.
func parseDependsOn(label string) []string {
	if label == "" {
		return nil
	}
	var deps []string
	for _, entry := range strings.Split(label, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name := entry
		if idx := strings.Index(entry, ":"); idx >= 0 {
			name = entry[:idx]
		}
		name = strings.TrimSpace(name)
		if name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}
