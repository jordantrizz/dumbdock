package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type alertConfig struct {
	NtfyTopic     string
	GotifyURL     string
	GotifyToken   string
	Cooldown      time.Duration
	ContainerURL  string
}

type alertManager struct {
	cfg       alertConfig
	client    *http.Client
	mu        sync.Mutex
	seen      map[string]bool
	lastAlert time.Time
}

type ntfyMessage struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
	Click    string   `json:"click,omitempty"`
}

type gotifyMessage struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

func newAlertManager() *alertManager {
	cfg := alertConfig{
		Cooldown:     5 * time.Minute,
		ContainerURL: os.Getenv("DUMBDOCK_URL"),
	}
	if t := os.Getenv("NTFY_TOPIC"); t != "" {
		cfg.NtfyTopic = t
	}
	if u := os.Getenv("GOTIFY_URL"); u != "" {
		cfg.GotifyURL = u
	}
	if t := os.Getenv("GOTIFY_TOKEN"); t != "" {
		cfg.GotifyToken = t
	}
	if s := os.Getenv("ALERT_COOLDOWN"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			cfg.Cooldown = d
		}
	}

	return &alertManager{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		seen:   make(map[string]bool),
	}
}

func (am *alertManager) enabled() bool {
	return am.cfg.NtfyTopic != "" || (am.cfg.GotifyURL != "" && am.cfg.GotifyToken != "")
}

func (am *alertManager) checkNew(unlabeled []containerCard) {
	am.mu.Lock()
	defer am.mu.Unlock()

	var newContainers []containerCard
	for _, c := range unlabeled {
		if !am.seen[c.ContainerID] {
			newContainers = append(newContainers, c)
		}
		am.seen[c.ContainerID] = true
	}

	if len(newContainers) == 0 {
		return
	}

	if time.Since(am.lastAlert) < am.cfg.Cooldown {
		return
	}

	for _, c := range newContainers {
		am.send(c)
	}

	am.lastAlert = time.Now()
}

func (am *alertManager) send(c containerCard) {
	msg := fmt.Sprintf("Container: %s\nImage: %s\nStatus: %s", c.ContainerName, c.Image, c.Status)
	if c.Ports != "" {
		msg += "\nPorts: " + c.Ports
	}

	if am.cfg.NtfyTopic != "" {
		am.sendNtfy(c, msg)
	}
	if am.cfg.GotifyURL != "" && am.cfg.GotifyToken != "" {
		am.sendGotify(c, msg)
	}
}

func (am *alertManager) sendNtfy(c containerCard, msg string) {
	m := ntfyMessage{
		Topic:    am.cfg.NtfyTopic,
		Title:    "New unlabeled container: " + c.ContainerName,
		Message:  msg,
		Priority: 3,
		Tags:     []string{"warning"},
	}
	if am.cfg.ContainerURL != "" {
		m.Click = am.cfg.ContainerURL
	}

	body, _ := json.Marshal(m)
	resp, err := am.client.Post("https://ntfy.sh", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("ntfy alert failed: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("sent ntfy alert for %s", c.ContainerName)
}

func (am *alertManager) sendGotify(c containerCard, msg string) {
	m := gotifyMessage{
		Title:    "New unlabeled container: " + c.ContainerName,
		Message:  msg,
		Priority: 5,
	}

	body, _ := json.Marshal(m)
	url := am.cfg.GotifyURL + "/message?token=" + am.cfg.GotifyToken
	resp, err := am.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("gotify alert failed: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("sent gotify alert for %s", c.ContainerName)
}
