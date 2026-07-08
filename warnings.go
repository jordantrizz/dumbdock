package main

import (
	"net"
	"sort"
	"strings"
)

// isPrivateIP checks if an IP address falls within a private or shared
// address space. It covers RFC 1918 (10.0.0.0/8, 172.16.0.0/12,
// 192.168.0.0/16), RFC 4193 (fc00::/7), and RFC 6598 Carrier-Grade NAT
// (100.64.0.0/10). Go's net.IP.IsPrivate() covers the first two but
// not CGNAT, so we check that range explicitly.
func isPrivateIP(ip net.IP) bool {
	// Pre-parsed CIDR for 100.64.0.0/10 (RFC 6598 Carrier-Grade NAT).
	// Go's IsPrivate() does not cover this range.
	cgnat := net.IPNet{IP: net.ParseIP("100.64.0.0"), Mask: net.CIDRMask(10, 32)}
	return ip.IsPrivate() || cgnat.Contains(ip)
}

// checkPortBindings examines the host IP for each port binding. Ports
// bound to 127.0.0.1 or ::1 are ignored (local-only). Ports bound to
// private IPs (RFC 1918, RFC 4193, RFC 6598 CGNAT) are classified as
// private. All other non-localhost bindings (including 0.0.0.0) are
// classified as public.
func checkPortBindings(ports []dockerPort) (hasPublic bool, ips []string, hasPrivate bool, privateIPs []string) {
	seenPublic := make(map[string]bool)
	seenPrivate := make(map[string]bool)
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		ip := p.IP
		if ip == "" {
			ip = "0.0.0.0"
		}
		if ip == "127.0.0.1" || ip == "::1" {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			// Unparseable IP — treat as public to be safe.
			hasPublic = true
			if !seenPublic[ip] {
				seenPublic[ip] = true
				ips = append(ips, ip)
			}
			continue
		}
		if isPrivateIP(parsed) {
			hasPrivate = true
			if !seenPrivate[ip] {
				seenPrivate[ip] = true
				privateIPs = append(privateIPs, ip)
			}
		} else {
			hasPublic = true
			if !seenPublic[ip] {
				seenPublic[ip] = true
				ips = append(ips, ip)
			}
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
