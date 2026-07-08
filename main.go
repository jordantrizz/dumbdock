package main

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

//go:embed index.html
var indexHTML string

// version is set at build time via -ldflags (e.g., "-X main.version=<git-sha>-<timestamp>")
// Defaults to "dev" when built without ldflags.
var version = "dev"

//go:embed VERSION
var versionFileContent string

//go:embed dumbdock.svg
var dumbdockIcon []byte

// appVersion is the semantic version embedded from the VERSION file.
var appVersion = strings.TrimSpace(versionFileContent)

// buildNumber is set at build time via -ldflags (e.g., "-X main.buildNumber=$(git rev-list --count HEAD)")
// Defaults to "0" when built without ldflags.
var buildNumber = "0"

type cardResponse struct {
	Cards      []containerCard `json:"cards"`
	Unlabeled  []containerCard `json:"unlabeled"`
	Groups     []string        `json:"groups"`
	Grouped    map[string][]containerCard `json:"grouped"`
}

func main() {
	socketPath := os.Getenv("DOCKER_SOCK")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	pollInterval := 10 * time.Second
	if s := os.Getenv("POLL_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			pollInterval = d
		}
	}

	authPassword := os.Getenv("DUMBDOCK_PASSWORD")

	cfg, err := loadConfig(configPath())
	if err != nil {
		log.Printf("warning: config load: %v", err)
	}

	// Initialize icon sets (fetches indexes from configured providers).
	iconHTTPClient := &http.Client{Timeout: 15 * time.Second}
	initIconSets(cfg, iconHTTPClient)

	alerts := newAlertManager()

	client := newDockerClient(socketPath)
	var cards []containerCard
	var unlabeled []containerCard
	var groups []string
	var grouped map[string][]containerCard

	refresh := func() {
		containers, err := fetchContainers(client)
		if err != nil {
			log.Printf("error fetching containers: %v", err)
			return
		}

		cards = nil
		unlabeled = nil
		grouped = make(map[string][]containerCard)

		contBlacklist := containerBlacklist(cfg)

		for _, c := range containers {
			// Skip containers in the visibility blacklist entirely.
			if isInBlacklist(containerName(c), contBlacklist) {
				continue
			}

			card := parseLabels(c.Labels)
			card.ContainerID = c.ID[:12]
			card.ContainerName = containerName(c)
			card.Image = c.Image
			card.Status = c.Status
			card.State = c.State
			card.Ports = formatPorts(c.Ports)
			card.Labels = c.Labels
			card.Created = c.Created

			card.HasPublicBinding, card.PublicBindingIPs = checkPortBindings(c.Ports)
			card.TraefikEnabled, card.TraefikURLs = parseTraefikLabels(c.Labels)

			if card.Icon == "" {
				card.Icon, _, _ = resolveIcon(c.Image)
			}

			if card.HasLabels && card.Name != "" {
				cards = append(cards, card)
				g := card.Group
				if g == "" {
					g = "Other"
				}
				grouped[g] = append(grouped[g], card)
			} else {
				card.Name = card.ContainerName
				unlabeled = append(unlabeled, card)
			}
		}

		overridden, remaining := applyOverrides(cfg, unlabeled)
		for _, card := range overridden {
			g := card.Group
			if g == "" {
				g = "Other"
			}
			grouped[g] = append(grouped[g], card)
			cards = append(cards, card)
		}
		unlabeled = remaining

		// Auto-detection pass: for containers that remain unlabeled, try to
		// detect an icon from the image name. If successful, place them in the
		// auto-detected group instead of "Unlabeled".
		if isAutoDetectionEnabled(cfg) {
			groupName := autoDetectedGroupName(cfg)
			serviceBlacklist := autoDetectionServiceBlacklist(cfg)
			var autoDetected, stillUnlabeled []containerCard
			for _, c := range unlabeled {
				// Check if this container is running dumbdock itself.
				if isDumbdockImage(c.Image, c.Labels) {
					c.Icon = "/dumbdock.svg"
					c.Group = groupName
					c.HasLabels = true
					autoDetected = append(autoDetected, c)
					continue
				}

				iconURL, ok, diag := resolveIcon(c.Image)

				// Fallback 1: Try compose service name (unless blacklisted).
				if !ok {
					if svcName, hasSvc := c.Labels["com.docker.compose.service"]; hasSvc && svcName != "" {
						if isInBlacklist(svcName, serviceBlacklist) {
							diag = fmt.Sprintf("%s; skipped service name %q (blacklisted)", diag, svcName)
						} else {
							var diag2 string
							iconURL, ok, diag2 = resolveIcon(svcName)
							if ok {
								c.Name = svcName
								diag = fmt.Sprintf("%s; then service name %q: %s", diag, svcName, diag2)
							} else {
								diag = fmt.Sprintf("%s; also tried service name %q: %s", diag, svcName, diag2)
							}
						}
					}
				}

				// Fallback 2: Try the app_name label if present.
				if !ok {
					if appName, hasAppName := c.Labels["app_name"]; hasAppName && appName != "" {
						var diag2 string
						iconURL, ok, diag2 = resolveIcon(appName)
						if ok {
							c.Name = appName
							diag = fmt.Sprintf("%s; then app_name label %q: %s", diag, appName, diag2)
						} else {
							diag = fmt.Sprintf("%s; also tried app_name label %q: %s", diag, appName, diag2)
						}
					}
				}

				// Fallback 3: Try the OCI image title label if present.
				ociTitle := ""
				if !ok {
					if title, hasTitle := c.Labels["org.opencontainers.image.title"]; hasTitle && title != "" {
						ociTitle = title
						var diag2 string
						iconURL, ok, diag2 = resolveIcon(title)
						if ok {
							diag = fmt.Sprintf("%s; then OCI title %q: %s", diag, title, diag2)
						} else {
							diag = fmt.Sprintf("%s; also tried OCI title %q: %s", diag, title, diag2)
						}
					}
				}

				if ok {
					c.Icon = iconURL
					c.Group = groupName
					c.HasLabels = true
					if ociTitle != "" {
						c.Name = ociTitle
					}
					autoDetected = append(autoDetected, c)
				} else {
					c.AutoDetectFailedReason = diag
					stillUnlabeled = append(stillUnlabeled, c)
				}
			}
			for _, card := range autoDetected {
				grouped[groupName] = append(grouped[groupName], card)
				cards = append(cards, card)
			}
			unlabeled = stillUnlabeled
		}

		for _, cards := range grouped {
			sort.Slice(cards, func(i, j int) bool {
				return strings.ToLower(cards[i].Name) < strings.ToLower(cards[j].Name)
			})
		}
		sort.Slice(unlabeled, func(i, j int) bool {
			return strings.ToLower(unlabeled[i].Name) < strings.ToLower(unlabeled[j].Name)
		})

		groups = make([]string, 0, len(grouped))
		for g := range grouped {
			groups = append(groups, g)
		}
		sort.Strings(groups)

		if alerts.enabled() {
			alerts.checkNew(unlabeled)
		}
	}

	refresh()

	go func() {
		for range time.NewTicker(pollInterval).C {
			refresh()
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/containers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		resp := cardResponse{
			Cards:     cards,
			Unlabeled: unlabeled,
			Groups:    groups,
			Grouped:   grouped,
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("GET /api/icons", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(GetIconsData())
	})

	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{
			"version": appVersion,
			"build":   buildNumber,
		})
	})

	mux.HandleFunc("GET /dumbdock.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(dumbdockIcon)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", fmt.Sprintf("\"%s\"", version))

		if r.Header.Get("If-None-Match") == fmt.Sprintf("\"%s\"", version) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Write([]byte(indexHTML))
	})

	log.Printf("dumbdock v%s (build %s)", appVersion, buildNumber)
	log.Printf("listening on %s (socket: %s, poll: %s)", listenAddr, socketPath, pollInterval)

	var handler http.Handler = mux
	if authPassword != "" {
		log.Println("Basic auth enabled")
		handler = basicAuth(mux, authPassword)
	}
	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

// basicAuth returns an HTTP handler that requires HTTP Basic Authentication.
// The username is accepted unconditionally; only the password is checked.
func basicAuth(next http.Handler, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="dumbdock"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isDumbdockImage reports whether the given Docker image reference or
// container labels correspond to a known dumbdock container.
// It checks:
//   1. The image path against known dumbdock image patterns.
//   2. The compose project name (com.docker.compose.project) for "dumbdock".
//   3. The compose service name (com.docker.compose.service) for "dumbdock".
func isDumbdockImage(image string, labels map[string]string) bool {
	// Step 1: Check the image path against known patterns.
	path := imagePath(image)
	if path != "" {
		path = strings.ToLower(path)
		known := []string{
			"dumbdock",
			"jordantrizz/dumbdock",
			"ghcr.io/jordantrizz/dumbdock",
		}
		for _, k := range known {
			if path == k {
				return true
			}
		}
	}

	// Step 2: Check compose project label.
	if project, ok := labels["com.docker.compose.project"]; ok {
		if strings.ToLower(project) == "dumbdock" {
			return true
		}
	}

	// Step 3: Check compose service label.
	if svc, ok := labels["com.docker.compose.service"]; ok {
		if strings.ToLower(svc) == "dumbdock" {
			return true
		}
	}

	return false
}
