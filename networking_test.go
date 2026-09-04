package main

import (
	"reflect"
	"testing"
)

func TestHasPublicExposure(t *testing.T) {
	tests := []struct {
		name  string
		ports []dockerPort
		want  bool
	}{
		{
			name:  "no ports",
			ports: nil,
			want:  false,
		},
		{
			name: "unpublished port",
			ports: []dockerPort{
				{PrivatePort: 8080, PublicPort: 0, Type: "tcp"},
			},
			want: false,
		},
		{
			name: "explicit 0.0.0.0 binding",
			ports: []dockerPort{
				{IP: "0.0.0.0", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			},
			want: true,
		},
		{
			name: "empty IP binding (docker reports as 0.0.0.0)",
			ports: []dockerPort{
				{IP: "", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			},
			want: true,
		},
		{
			name: "ipv6 wildcard binding",
			ports: []dockerPort{
				{IP: "::", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			},
			want: true,
		},
		{
			name: "localhost binding",
			ports: []dockerPort{
				{IP: "127.0.0.1", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			},
			want: false,
		},
		{
			name: "private ip binding",
			ports: []dockerPort{
				{IP: "192.168.1.10", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
			},
			want: false,
		},
		{
			name: "mixed localhost and wildcard",
			ports: []dockerPort{
				{IP: "127.0.0.1", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"},
				{IP: "0.0.0.0", PrivatePort: 9090, PublicPort: 9090, Type: "tcp"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasPublicExposure(tt.ports); got != tt.want {
				t.Errorf("hasPublicExposure(%v) = %v, want %v", tt.ports, got, tt.want)
			}
		})
	}
}

func TestBuildNetworkingDataPublicExposure(t *testing.T) {
	containers := []dockerContainer{
		{
			ID:    "aaaaaaaaaaaa",
			Names: []string{"/web"},
			State: "running",
			Ports: []dockerPort{
				{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 80, Type: "tcp"},
			},
		},
		{
			ID:    "bbbbbbbbbbbb",
			Names: []string{"/db"},
			State: "running",
			Ports: []dockerPort{
				{IP: "127.0.0.1", PrivatePort: 5432, PublicPort: 5432, Type: "tcp"},
			},
		},
		{
			ID:    "cccccccccccc",
			Names: []string{"/stopped-app"},
			State: "exited",
			Ports: []dockerPort{
				{IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 3000, Type: "tcp"},
			},
		},
	}

	networks := []dockerNetwork{
		{
			Name:   "bridge",
			Driver: "bridge",
			Containers: map[string]dockerNetMember{
				"aaaaaaaaaaaa": {Name: "web", IPv4Address: "172.17.0.2/16"},
				"bbbbbbbbbbbb": {Name: "db", IPv4Address: "172.17.0.3/16"},
			},
		},
	}

	got := buildNetworkingData(containers, networks)

	// Only the running "web" container is exposed on 0.0.0.0; the stopped
	// container's wildcard binding must not appear in the banner list.
	want := []string{"web"}
	if !reflect.DeepEqual(got.PublicExposed, want) {
		t.Errorf("PublicExposed = %v, want %v", got.PublicExposed, want)
	}

	// The exposed flag is set on the web container's row only.
	if len(got.Running) != 1 {
		t.Fatalf("expected 1 running group, got %d", len(got.Running))
	}
	byName := make(map[string]networkContainer)
	for _, c := range got.Running[0].Containers {
		byName[c.Name] = c
	}
	if !byName["web"].ExposedPublic {
		t.Errorf("expected web container to be marked ExposedPublic")
	}
	if byName["db"].ExposedPublic {
		t.Errorf("expected db container to NOT be marked ExposedPublic")
	}
}
