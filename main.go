package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sort"
	"time"
)

//go:embed index.html
var indexHTML string

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

	cfg, err := loadConfig(configPath())
	if err != nil {
		log.Printf("warning: config load: %v", err)
	}

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

		for _, c := range containers {
			card := parseLabels(c.Labels)
			card.ContainerID = c.ID[:12]
			card.ContainerName = containerName(c)
			card.Image = c.Image
			card.Status = c.Status
			card.State = c.State
			card.Ports = formatPorts(c.Ports)

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

		for _, cards := range grouped {
			sort.Slice(cards, func(i, j int) bool {
				if cards[i].Order != cards[j].Order {
					return cards[i].Order < cards[j].Order
				}
				return cards[i].Name < cards[j].Name
			})
		}
		sort.Slice(unlabeled, func(i, j int) bool {
			return unlabeled[i].Name < unlabeled[j].Name
		})

		groups = make([]string, 0, len(grouped))
		for g := range grouped {
			groups = append(groups, g)
		}
		sort.Slice(groups, func(i, j int) bool {
			gi := grouped[groups[i]]
			gj := grouped[groups[j]]
			minI, minJ := 999, 999
			for _, c := range gi {
				if c.Order < minI {
					minI = c.Order
				}
			}
			for _, c := range gj {
				if c.Order < minJ {
					minJ = c.Order
				}
			}
			if minI != minJ {
				return minI < minJ
			}
			return groups[i] < groups[j]
		})

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
		resp := cardResponse{
			Cards:     cards,
			Unlabeled: unlabeled,
			Groups:    groups,
			Grouped:   grouped,
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(indexHTML))
	})

	log.Printf("listening on %s (socket: %s, poll: %s)", listenAddr, socketPath, pollInterval)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}
