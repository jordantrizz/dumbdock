package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type cardOverride struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Icon        string `json:"icon"`
	Href        string `json:"href"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

type overrideConfig struct {
	Containers map[string]cardOverride `json:"containers"`
}

func loadConfig(path string) (*overrideConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &overrideConfig{Containers: map[string]cardOverride{}}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg overrideConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Containers == nil {
		cfg.Containers = map[string]cardOverride{}
	}
	return &cfg, nil
}

func applyOverrides(cfg *overrideConfig, unlabeled []containerCard) (labeled, stillUnlabeled []containerCard) {
	for _, c := range unlabeled {
		override, ok := cfg.Containers[c.ContainerName]
		if !ok {
			stillUnlabeled = append(stillUnlabeled, c)
			continue
		}
		if override.Name != "" {
			c.Name = override.Name
		} else {
			c.Name = c.ContainerName
		}
		if override.Group != "" {
			c.Group = override.Group
		}
		if override.Icon != "" {
			c.Icon = override.Icon
		}
		if override.Href != "" {
			c.Href = override.Href
		}
		if override.Description != "" {
			c.Description = override.Description
		}
		if override.Order != 0 {
			c.Order = override.Order
		}
		c.HasLabels = true
		labeled = append(labeled, c)
	}
	return
}

func configPath() string {
	p := os.Getenv("DUMBDOCK_CONFIG")
	if p == "" {
		return "/config/dumbdock.json"
	}
	return p
}


