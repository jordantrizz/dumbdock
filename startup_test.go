package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestRedactSecret(t *testing.T) {
	if got := redactSecret(""); got != "(not set)" {
		t.Errorf("redactSecret(\"\") = %q, want %q", got, "(not set)")
	}
	if got := redactSecret("hunter2"); got != "(set)" {
		t.Errorf("redactSecret(\"hunter2\") = %q, want %q", got, "(set)")
	}
}

func TestLogStartupConfig(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	logStartupConfig(startupConfig{
		ConfigPath:         "/config/dumbdock.json",
		ConfigStatus:       "loaded",
		ListenAddr:         ":8080",
		SocketPath:         "/var/run/docker.sock",
		PollInterval:       10 * time.Second,
		AuthMode:           "web-auth",
		Password:           "supersecret",
		UpdateCheck:        true,
		UpdateInterval:     6 * time.Hour,
		AutoDetection:      true,
		AutoDetectedGroup:  "Auto Detected",
		ServiceBlacklist:   []string{"server", "db"},
		ContainerBlacklist: []string{"traefik"},
		IconSetNames:       []string{"selfhst", "dashboard-icons"},
		StateDir:           "/data",
		DashboardURL:       "https://dumbdock.example.com",
		NtfyTopic:          "mytopic",
		GotifyURL:          "https://gotify.example.com",
		GotifyToken:        "gotify-secret-token",
		AlertCooldown:      5 * time.Minute,
		TraefikAPIURL:      "http://traefik:8080",
		TraefikAPIToken:    "traefik-secret-token",
		TraefikAPIUser:     "admin",
		TraefikAPIPass:     "traefik-secret-pass",
		ContainerRoot:      "/opt/containers",
		DataRoot:           "/srv/containers",
		BackupRoot:         "/srv/containers",
		BackupMaxAge:       26 * time.Hour,
		BackupScanInterval: 5 * time.Minute,
		BackupToken:        "backup-secret-token",
	})

	out := buf.String()
	if !strings.Contains(out, "startup config:") {
		t.Fatalf("output missing header:\n%s", out)
	}
	for _, want := range []string{
		"auth password: (set)",
		"gotify token: (set)",
		"traefik api token: (set)",
		"traefik api pass: (set)",
		"issues state dir: /data",
		"container root: /opt/containers",
		"data root: /srv/containers",
		"backup root: /srv/containers",
		"backup max age: 26h0m0s",
		"storage scan interval: 5m0s",
		"backup report token: (set)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{
		"supersecret",
		"gotify-secret-token",
		"traefik-secret-token",
		"traefik-secret-pass",
		"backup-secret-token",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("output leaked secret %q:\n%s", secret, out)
		}
	}
}
