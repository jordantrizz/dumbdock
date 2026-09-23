package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// configWarning describes a single configuration problem detected at startup.
// Validation is advisory: callers log each warning and the process continues
// with defaults.
type configWarning struct {
	Field   string
	Message string
}

// validateConfig checks the effective configuration and returns a warning for
// every invalid setting. It never fails the process; callers log the warnings
// and continue. Values resolved with a silent fallback (durations, port) are
// read from the environment here so invalid raw input is still reported.
func validateConfig(cfg *overrideConfig, loadErr error, authMode string) []configWarning {
	var warnings []configWarning

	if loadErr != nil {
		warnings = append(warnings, configWarning{
			Field:   "config file",
			Message: loadErr.Error(),
		})
	}

	switch authMode {
	case "none", "http-auth", "web-auth":
	default:
		warnings = append(warnings, configWarning{
			Field:   "auth mode",
			Message: fmt.Sprintf("unknown value %q (expected none, http-auth, or web-auth); auth disabled", authMode),
		})
	}
	if s := os.Getenv("POLL_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			warnings = append(warnings, configWarning{
				Field:   "POLL_INTERVAL",
				Message: fmt.Sprintf("invalid duration %q; using default 10s", s),
			})
		}
	}

	if s := os.Getenv("DUMBDOCK_UPDATE_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			warnings = append(warnings, configWarning{
				Field:   "DUMBDOCK_UPDATE_INTERVAL",
				Message: fmt.Sprintf("invalid duration %q; using default 6h", s),
			})
		}
	}

	if s := os.Getenv("ALERT_COOLDOWN"); s != "" {
		if d, err := time.ParseDuration(s); err != nil || d <= 0 {
			warnings = append(warnings, configWarning{
				Field:   "ALERT_COOLDOWN",
				Message: fmt.Sprintf("invalid duration %q; using default 5m", s),
			})
		}
	}

	if os.Getenv("LISTEN_ADDR") == "" {
		if port := os.Getenv("DUMBDOCK_PORT"); port != "" {
			if _, err := strconv.Atoi(strings.TrimPrefix(port, ":")); err != nil {
				warnings = append(warnings, configWarning{
					Field:   "DUMBDOCK_PORT",
					Message: fmt.Sprintf("invalid port %q; expected a number", port),
				})
			}
		}
	}

	if cfg != nil {
		for i, is := range cfg.IconSets {
			field := fmt.Sprintf("iconSets[%d]", i)
			if strings.TrimSpace(is.Name) == "" {
				warnings = append(warnings, configWarning{Field: field, Message: "missing name"})
			}
			if strings.TrimSpace(is.CdnUrlTemplate) == "" {
				warnings = append(warnings, configWarning{Field: field, Message: "missing cdnUrlTemplate"})
			}
			switch is.IndexFormat {
			case "", "map", "list", "tree", "objects":
			default:
				warnings = append(warnings, configWarning{
					Field:   field,
					Message: fmt.Sprintf("unknown indexFormat %q (expected map, list, tree, or objects)", is.IndexFormat),
				})
			}
		}
	}

	return warnings
}

// redactSecret renders a secret-bearing value for logs without revealing it.
func redactSecret(v string) string {
	if v == "" {
		return "(not set)"
	}
	return "(set)"
}

// valueOrNone renders an optional non-secret value, or "(not set)" when empty.
func valueOrNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(not set)"
	}
	return v
}

// listOrNone joins a list for logs, or returns "(none)" when empty.
func listOrNone(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	return strings.Join(list, ", ")
}

// iconSetNames returns the names of the configured icon sets, in priority
// order. initIconSets must have run first.
func iconSetNames() []string {
	names := make([]string, 0, len(iconSets))
	for _, is := range iconSets {
		if is != nil && is.name != "" {
			names = append(names, is.name)
		}
	}
	return names
}

// startupConfig carries the effective runtime configuration for the startup
// echo. Secret fields are stored raw and redacted by logStartupConfig.
type startupConfig struct {
	ConfigPath         string
	ConfigStatus       string
	ListenAddr         string
	SocketPath         string
	PollInterval       time.Duration
	AuthMode           string
	Password           string
	UpdateCheck        bool
	UpdateInterval     time.Duration
	AutoDetection      bool
	AutoDetectedGroup  string
	ServiceBlacklist   []string
	ContainerBlacklist []string
	IconSetNames       []string
	DashboardURL       string
	NtfyTopic          string
	GotifyURL          string
	GotifyToken        string
	AlertCooldown      time.Duration
	TraefikAPIURL      string
	TraefikAPIToken    string
	TraefikAPIUser     string
	TraefikAPIPass     string
}

// logStartupConfig logs a structured summary of the effective configuration.
// Secrets are redacted; only their presence is reported.
func logStartupConfig(s startupConfig) {
	log.Printf("startup config:")
	log.Printf("  version: %s (build %s)", appVersion, buildNumber)
	log.Printf("  config file: %s (%s)", s.ConfigPath, s.ConfigStatus)
	log.Printf("  listen address: %s", s.ListenAddr)
	log.Printf("  docker socket: %s", s.SocketPath)
	log.Printf("  poll interval: %s", s.PollInterval)
	log.Printf("  auth mode: %s", s.AuthMode)
	log.Printf("  auth password: %s", redactSecret(s.Password))
	log.Printf("  update checks: %t (interval %s)", s.UpdateCheck, s.UpdateInterval)
	log.Printf("  auto-detection: %t (group %q, service blacklist: %s)", s.AutoDetection, s.AutoDetectedGroup, listOrNone(s.ServiceBlacklist))
	log.Printf("  container blacklist: %s", listOrNone(s.ContainerBlacklist))
	log.Printf("  icon sets: %s", listOrNone(s.IconSetNames))
	log.Printf("  dashboard url: %s", valueOrNone(s.DashboardURL))
	log.Printf("  ntfy topic: %s", valueOrNone(s.NtfyTopic))
	log.Printf("  gotify url: %s", valueOrNone(s.GotifyURL))
	log.Printf("  gotify token: %s", redactSecret(s.GotifyToken))
	log.Printf("  alert cooldown: %s", s.AlertCooldown)
	log.Printf("  traefik api url: %s", valueOrNone(s.TraefikAPIURL))
	log.Printf("  traefik api token: %s", redactSecret(s.TraefikAPIToken))
	log.Printf("  traefik api user: %s", valueOrNone(s.TraefikAPIUser))
	log.Printf("  traefik api pass: %s", redactSecret(s.TraefikAPIPass))
}
