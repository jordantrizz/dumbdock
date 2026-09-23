package main

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestResolveAuthMode(t *testing.T) {
	tests := []struct {
		name string
		env  string
		cfg  *overrideConfig
		want string
	}{
		{
			name: "env wins over config",
			env:  "http-auth",
			cfg:  &overrideConfig{AuthMode: "web-auth"},
			want: "http-auth",
		},
		{
			name: "config used when env unset",
			env:  "",
			cfg:  &overrideConfig{AuthMode: "http-auth"},
			want: "http-auth",
		},
		{
			name: "nothing set defaults to web-auth",
			env:  "",
			cfg:  &overrideConfig{},
			want: "web-auth",
		},
		{
			name: "explicit none stays none",
			env:  "none",
			cfg:  &overrideConfig{AuthMode: "web-auth"},
			want: "none",
		},
		{
			name: "unknown env value passes through",
			env:  "bogus",
			cfg:  &overrideConfig{},
			want: "bogus",
		},
		{
			name: "unknown config value passes through",
			env:  "",
			cfg:  &overrideConfig{AuthMode: "weird"},
			want: "weird",
		},
		{
			name: "env whitespace and case normalized",
			env:  " Web-Auth ",
			cfg:  &overrideConfig{},
			want: "web-auth",
		},
		{
			name: "config whitespace and case normalized",
			env:  "",
			cfg:  &overrideConfig{AuthMode: " HTTP-Auth "},
			want: "http-auth",
		},
		{
			name: "nil cfg defaults to web-auth",
			env:  "",
			cfg:  nil,
			want: "web-auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DUMBDOCK_AUTH_MODE", tt.env)
			if got := resolveAuthMode(tt.cfg); got != tt.want {
				t.Errorf("resolveAuthMode(%+v) = %q, want %q", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestGenerateAuthPassword(t *testing.T) {
	first := generateAuthPassword()
	second := generateAuthPassword()

	if len(first) != 32 {
		t.Errorf("generateAuthPassword() length = %d, want 32", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Errorf("generateAuthPassword() = %q is not valid hex: %v", first, err)
	}
	if first != strings.ToLower(first) {
		t.Errorf("generateAuthPassword() = %q, want lowercase hex", first)
	}
	if first == second {
		t.Errorf("generateAuthPassword() returned the same value twice: %q", first)
	}
}

func TestValidateConfig(t *testing.T) {
	clearEnv := func(t *testing.T) {
		for _, k := range []string{
			"DUMBDOCK_PASSWORD",
			"POLL_INTERVAL",
			"DUMBDOCK_UPDATE_INTERVAL",
			"ALERT_COOLDOWN",
			"DUMBDOCK_PORT",
			"LISTEN_ADDR",
		} {
			t.Setenv(k, "")
		}
	}

	tests := []struct {
		name       string
		cfg        *overrideConfig
		loadErr    error
		authMode   string
		env        map[string]string
		wantFields []string
	}{
		{
			name:     "valid config yields no warnings",
			cfg:      &overrideConfig{},
			authMode: "none",
		},
		{
			name:       "config load error",
			cfg:        &overrideConfig{},
			loadErr:    errors.New("parse config: unexpected end of JSON input"),
			authMode:   "none",
			wantFields: []string{"config file"},
		},
		{
			name:       "unknown auth mode",
			cfg:        &overrideConfig{},
			authMode:   "bogus",
			wantFields: []string{"auth mode"},
		},
		{
			name:       "invalid poll interval",
			cfg:        &overrideConfig{},
			authMode:   "none",
			env:        map[string]string{"POLL_INTERVAL": "abc"},
			wantFields: []string{"POLL_INTERVAL"},
		},
		{
			name:       "non-positive update interval",
			cfg:        &overrideConfig{},
			authMode:   "none",
			env:        map[string]string{"DUMBDOCK_UPDATE_INTERVAL": "0s"},
			wantFields: []string{"DUMBDOCK_UPDATE_INTERVAL"},
		},
		{
			name:       "invalid alert cooldown",
			cfg:        &overrideConfig{},
			authMode:   "none",
			env:        map[string]string{"ALERT_COOLDOWN": "nope"},
			wantFields: []string{"ALERT_COOLDOWN"},
		},
		{
			name:       "invalid port",
			cfg:        &overrideConfig{},
			authMode:   "none",
			env:        map[string]string{"DUMBDOCK_PORT": "abc"},
			wantFields: []string{"DUMBDOCK_PORT"},
		},
		{
			name:     "port ignored when listen addr is set",
			cfg:      &overrideConfig{},
			authMode: "none",
			env: map[string]string{
				"DUMBDOCK_PORT": "abc",
				"LISTEN_ADDR":   ":9000",
			},
		},
		{
			name: "invalid icon set entry",
			cfg: &overrideConfig{IconSets: []IconSetConfig{
				{Name: "", CdnUrlTemplate: "", IndexFormat: "bogus"},
			}},
			authMode:   "none",
			wantFields: []string{"iconSets[0]", "iconSets[0]", "iconSets[0]"},
		},
		{
			name: "valid icon set entry",
			cfg: &overrideConfig{IconSets: []IconSetConfig{
				{Name: "custom", CdnUrlTemplate: "https://example.com/{slug}.svg", IndexFormat: "map"},
			}},
			authMode: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := validateConfig(tt.cfg, tt.loadErr, tt.authMode)
			if len(got) != len(tt.wantFields) {
				t.Fatalf("validateConfig() returned %d warnings (%+v), want %d", len(got), got, len(tt.wantFields))
			}
			for i, want := range tt.wantFields {
				if got[i].Field != want {
					t.Errorf("warning[%d].Field = %q, want %q", i, got[i].Field, want)
				}
			}
		})
	}
}
