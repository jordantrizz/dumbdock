package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type cardOverride struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Icon        string `json:"icon"`
	Href        string `json:"href"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

type overrideConfig struct {
	AutoDetection              *bool                   `json:"autoDetection,omitempty"`
	AutoDetectedGroup          string                  `json:"autoDetectedGroup,omitempty"`
	AutoDetectionServiceBlacklist []string             `json:"autoDetectionServiceBlacklist,omitempty"`
	ContainerBlacklist         []string                `json:"containerBlacklist,omitempty"`
	Containers                 map[string]cardOverride  `json:"containers"`
	IconSets                   []IconSetConfig         `json:"iconSets,omitempty"`
	TraefikAPIToken            string                  `json:"traefikAPIToken,omitempty"`
}

func loadConfig(path string) (*overrideConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &overrideConfig{Containers: map[string]cardOverride{}}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg overrideConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Containers == nil {
		cfg.Containers = map[string]cardOverride{}
	}
	return &cfg, nil
}

func applyOverrides(cfg *overrideConfig, unlabeled []containerCard) (labeled, stillUnlabeled []containerCard) {
	for _, c := range unlabeled {
		override, ok := cfg.Containers[c.ContainerName]
		if !ok {
			stillUnlabeled = append(stillUnlabeled, c)
			continue
		}
		if override.Name != "" {
			c.Name = override.Name
		} else {
			c.Name = c.ContainerName
		}
		if override.Group != "" {
			c.Group = override.Group
		}
		if override.Icon != "" {
			c.Icon = override.Icon
		}
		if override.Href != "" {
			c.Href = override.Href
		}
		if override.Description != "" {
			c.Description = override.Description
		}
		if override.Order != 0 {
			c.Order = override.Order
		}
		c.HasLabels = true
		labeled = append(labeled, c)
	}
	return
}

func configPath() string {
	p := os.Getenv("DUMBDOCK_CONFIG")
	if p == "" {
		return "/config/dumbdock.json"
	}
	return p
}

// isAutoDetectionEnabled returns true if auto-detection of container icons is
// enabled. Checks the AUTO_DETECTION env var first (accepting
// "false"/"0"/"no"/"off" as false), then falls back to the config file value,
// then defaults to true.
func isAutoDetectionEnabled(cfg *overrideConfig) bool {
	switch env := os.Getenv("AUTO_DETECTION"); env {
	case "false", "0", "no", "off", "OFF":
		return false
	case "true", "1", "yes", "on", "ON":
		return true
	}
	if cfg != nil && cfg.AutoDetection != nil {
		return *cfg.AutoDetection
	}
	return true
}

// autoDetectedGroupName returns the group name to use for auto-detected
// containers. Checks the AUTODETECT_GROUP_NAME env var first, then falls back
// to the config file value, then defaults to "Auto Detected".
func autoDetectedGroupName(cfg *overrideConfig) string {
	if env := os.Getenv("AUTODETECT_GROUP_NAME"); env != "" {
		return env
	}
	if cfg != nil && cfg.AutoDetectedGroup != "" {
		return cfg.AutoDetectedGroup
	}
	return "Auto Detected"
}

// autoDetectionServiceBlacklist returns the list of compose service names
// that should be skipped during auto-detection fallback. These are generic
// names (e.g., "server", "db", "app") that are unreliable for icon matching.
// Checks the AUTO_DETECTION_SERVICE_BLACKLIST env var first (comma-separated),
// then falls back to the config file value, then defaults to
// ["server", "db", "app"].
func autoDetectionServiceBlacklist(cfg *overrideConfig) []string {
	if env := os.Getenv("AUTO_DETECTION_SERVICE_BLACKLIST"); env != "" {
		parts := strings.Split(env, ",")
		list := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				list = append(list, strings.ToLower(trimmed))
			}
		}
		return list
	}
	if cfg != nil && len(cfg.AutoDetectionServiceBlacklist) > 0 {
		list := make([]string, len(cfg.AutoDetectionServiceBlacklist))
		for i, v := range cfg.AutoDetectionServiceBlacklist {
			list[i] = strings.ToLower(v)
		}
		return list
	}
	return []string{"server", "db", "app"}
}

// containerBlacklist returns the list of container names to hide entirely from
// the dashboard. Checks the CONTAINER_BLACKLIST env var first
// (comma-separated), then falls back to the config file value, then defaults
// to an empty list (show all containers).
func containerBlacklist(cfg *overrideConfig) []string {
	if env := os.Getenv("CONTAINER_BLACKLIST"); env != "" {
		parts := strings.Split(env, ",")
		list := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				list = append(list, strings.ToLower(trimmed))
			}
		}
		return list
	}
	if cfg != nil && len(cfg.ContainerBlacklist) > 0 {
		list := make([]string, len(cfg.ContainerBlacklist))
		for i, v := range cfg.ContainerBlacklist {
			list[i] = strings.ToLower(v)
		}
		return list
	}
	return []string{}
}

// isInBlacklist reports whether name matches any entry in the given blacklist
// using case-insensitive comparison.
func isInBlacklist(name string, blacklist []string) bool {
	lower := strings.ToLower(name)
	for _, b := range blacklist {
		if lower == b {
			return true
		}
	}
	return false
}


