package main

import (
	"strconv"
	"strings"
)

const prefix = "dumbdock."

type containerCard struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Icon        string `json:"icon"`
	Href        string `json:"href"`
	Description string `json:"description"`
	Order       int    `json:"order"`

	ContainerID   string `json:"containerId"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	Status        string `json:"status"`
	State         string `json:"state"`
	Ports         string `json:"ports"`
	HasLabels     bool   `json:"hasLabels"`
}

func parseLabels(labels map[string]string) containerCard {
	card := containerCard{HasLabels: false}

	for k, v := range labels {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		card.HasLabels = true
		key := strings.TrimPrefix(k, prefix)

		switch key {
		case "name":
			card.Name = v
		case "group":
			card.Group = v
		case "icon":
			card.Icon = v
		case "href":
			card.Href = v
		case "description":
			card.Description = v
		case "order":
			if n, err := strconv.Atoi(v); err == nil {
				card.Order = n
			}
		}
	}

	if !card.HasLabels {
		card.Name = ""
	}

	return card
}
