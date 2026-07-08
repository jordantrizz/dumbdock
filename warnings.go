package main

import (
	"sort"
	"strings"
)

// checkPortBindings examines the host IP for each port binding. If any port
// is bound to an IP that is not 127.0.0.1 or ::1 (including 0.0.0.0, which
// Docker reports as an empty IP string), the container is flagged as having
// a public binding.
func checkPortBindings(ports []dockerPort) (hasPublic bool, ips []string) {
	seen := make(map[string]bool)
	for _, p := range ports {
		// Ports with PublicPort == 0 are exposed but not published/bound
		// to the host. Skip them to avoid false positives.
		if p.PublicPort == 0 {
			continue
		}
		ip := p.IP
		if ip == "" {
			// Docker reports 0.0.0.0 as an empty IP string in
			// /containers/json. Treat it as a public binding.
			ip = "0.0.0.0"
		}
		if ip == "127.0.0.1" || ip == "::1" {
			continue
		}
		hasPublic = true
		if !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	return
}

// parseTraefikLabels scans container labels for Traefik proxy configuration.
// It checks for traefik.enable=true and extracts hostnames from
// traefik.http.routers.<name>.rule labels that use the Host(`...`) matcher.
// URLs are always returned with the https:// scheme.
func parseTraefikLabels(labels map[string]string) (enabled bool, urls []string) {
	hasEnable := false
	allHosts := make(map[string]bool)

	for k, v := range labels {
		if k == "traefik.enable" && v == "true" {
			hasEnable = true
			continue
		}

		if !strings.HasPrefix(k, "traefik.http.routers.") {
			continue
		}

		rest := strings.TrimPrefix(k, "traefik.http.routers.")
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			continue
		}
		suffix := parts[1]

		if suffix == "rule" {
			hosts := extractTraefikHosts(v)
			for _, h := range hosts {
				if h != "" {
					allHosts[h] = true
				}
			}
		}
	}

	if len(allHosts) == 0 {
		return hasEnable, nil
	}

	urls = make([]string, 0, len(allHosts))
	for h := range allHosts {
		urls = append(urls, "https://"+h)
	}
	sort.Strings(urls)

	return true, urls
}

// extractTraefikHosts extracts all hostnames from a Traefik rule value
// containing one or more Host(`hostname`) matchers. It handles:
//   - Single host:          Host(`example.com`)
//   - Comma-separated:      Host(`a.com`, `b.com`)
//   - Multiple matchers:    Host(`a.com`) || Host(`b.com`)
//   - Mixed with other:     Host(`a.com`, `b.com`) || Host(`c.com`)
func extractTraefikHosts(rule string) []string {
	hosts := make(map[string]bool)
	searchFrom := 0

	for {
		// Find the next Host( occurrence.
		idx := strings.Index(rule[searchFrom:], "Host(")
		if idx < 0 {
			break
		}
		idx += searchFrom

		// Find the matching closing parenthesis by tracking depth.
		pos := idx + 5 // len("Host(")
		depth := 1
		for depth > 0 && pos < len(rule) {
			switch rule[pos] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth > 0 {
				pos++
			}
		}
		if depth != 0 {
			// Unbalanced; skip forward and continue.
			searchFrom = idx + 1
			continue
		}

		// Content between Host( and its matching ).
		inner := rule[idx+5 : pos]

		// Extract all backtick-quoted segments from the inner content.
		btPos := 0
		for {
			btStart := strings.Index(inner[btPos:], "`")
			if btStart < 0 {
				break
			}
			btStart += btPos
			btEnd := strings.Index(inner[btStart+1:], "`")
			if btEnd < 0 {
				break
			}
			btEnd += btStart + 1
			host := strings.TrimSpace(inner[btStart+1 : btEnd])
			if host != "" {
				hosts[host] = true
			}
			btPos = btEnd + 1
		}

		searchFrom = pos + 1
	}

	result := make([]string, 0, len(hosts))
	for h := range hosts {
		result = append(result, h)
	}
	sort.Strings(result)
	return result
}
