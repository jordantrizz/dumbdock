package main

import (
	"reflect"
	"testing"
)

func TestParseDependsOn(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  []string
	}{
		{
			name:  "full condition and restart entries",
			label: "db:service_started:false,redis:service_healthy:true",
			want:  []string{"db", "redis"},
		},
		{
			name:  "single entry without condition",
			label: "db",
			want:  []string{"db"},
		},
		{
			name:  "empty label",
			label: "",
			want:  nil,
		},
		{
			name:  "entries with surrounding whitespace",
			label: " db :service_started:false, redis ",
			want:  []string{"db", "redis"},
		},
		{
			name:  "trailing comma",
			label: "db,redis,",
			want:  []string{"db", "redis"},
		},
		{
			name:  "only commas",
			label: ",,",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDependsOn(tt.label)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDependsOn(%q) = %v, want %v", tt.label, got, tt.want)
			}
		})
	}
}
