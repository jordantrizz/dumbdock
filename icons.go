package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

//go:embed icons.json
var iconsData []byte

// IconSetConfig defines the configuration for an icon provider.
type IconSetConfig struct {
	Name           string            `json:"name"`
	IndexUrl       string            `json:"indexUrl"`                 // URL or file path to fetch the icon index
	CdnUrlTemplate string            `json:"cdnUrlTemplate"`           // CDN URL with {slug} placeholder
	IndexFormat    string            `json:"indexFormat"`              // "map", "list", or "tree"
	IndexField     string            `json:"indexField,omitempty"`     // field name for tree format (e.g. "svg")
	Mappings       map[string]string `json:"mappings,omitempty"`       // image key → icon slug overrides
	Priority       int               `json:"priority"`                 // lower = checked first
}

// IconSet represents a resolved icon provider ready for lookups.
type IconSet struct {
	name           string
	cdnURLTemplate string
	mappings       map[string]string // image key → icon slug
	knownSlugs     map[string]bool   // set of valid icon slugs
	priority       int
}

func (is *IconSet) url(slug string) string {
	return strings.ReplaceAll(is.cdnURLTemplate, "{slug}", slug)
}

// resolve attempts to find an icon URL for the given image path.
// path is the full image path (e.g., "portainer/portainer-ce")
// last is the last path component (e.g., "portainer-ce")
func (is *IconSet) resolve(path, last string) (string, bool) {
	// 1. Try the full path as a key (e.g., "portainer/portainer-ce")
	if slug, ok := is.mappings[path]; ok {
		return is.url(slug), true
	}

	// 2. Try just the last path component (e.g., "nginx" from "library/nginx")
	if slug, ok := is.mappings[last]; ok {
		return is.url(slug), true
	}

	// 3. Check if the last component is a known slug name
	if is.knownSlugs[last] {
		return is.url(last), true
	}

	return "", false
}

// iconSets is the global slice of configured icon sets, ordered by priority.
var iconSets []*IconSet

// initIconSets builds the icon set list from config, fetching indexes as needed.
// If the user provides icon sets in config, they replace the built-in defaults entirely.
func initIconSets(cfg *overrideConfig, httpClient *http.Client) {
	var configs []IconSetConfig

	if cfg != nil && len(cfg.IconSets) > 0 {
		configs = cfg.IconSets
	} else {
		// Built-in defaults: use the embedded icons.json as the mappings for
		// the selfhst set, and fetch indexes at startup for up-to-date slug lists.
		var defaultMappings map[string]string
		if err := json.Unmarshal(iconsData, &defaultMappings); err != nil {
			log.Printf("warning: failed to parse embedded icons.json: %v", err)
			defaultMappings = map[string]string{}
		}

		configs = []IconSetConfig{
			{
				Name:           "selfhst",
				IndexUrl:       "https://cdn.jsdelivr.net/gh/selfhst/icons@main/index.json",
				CdnUrlTemplate: "https://cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg",
			IndexFormat:    "objects",
			IndexField:     "Reference",
				Mappings:       defaultMappings,
				Priority:       0,
			},
			{
				Name:           "dashboard-icons",
				IndexUrl:       "https://raw.githubusercontent.com/homarr-labs/dashboard-icons/refs/heads/main/tree.json",
				CdnUrlTemplate: "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/{slug}.svg",
				IndexFormat:    "tree",
				IndexField:     "svg",
				Priority:       1,
			},
		}
	}

	sets := make([]*IconSet, 0, len(configs))

	for _, cc := range configs {
		is := &IconSet{
			name:           cc.Name,
			cdnURLTemplate: cc.CdnUrlTemplate,
			mappings:       make(map[string]string),
			knownSlugs:     make(map[string]bool),
			priority:       cc.Priority,
		}

		// Copy static mappings into the set.
		// Both the key (image-derived name) and the value (icon slug) are
		// tracked so that slug-only resolution (step 3 in resolve) works.
		for k, v := range cc.Mappings {
			is.mappings[k] = v
			is.knownSlugs[v] = true
		}

		// Fetch and parse the remote / local index to discover available slugs.
		if cc.IndexUrl != "" {
			known, err := fetchAndParseIndex(cc.IndexUrl, cc.IndexFormat, cc.IndexField, httpClient)
			if err != nil {
				log.Printf("warning: icon set %q: failed to fetch/parse index from %s: %v",
					cc.Name, cc.IndexUrl, err)
			} else {
				for slug := range known {
					is.knownSlugs[slug] = true
				}
			}
		}

		// If a set has no explicit mappings but discovered slugs from its
		// index, auto-generate identity mappings (slug → slug) so that
		// every slug appears as a direct-match entry in the icons table
		// instead of showing "— (known slug)".
		if len(is.mappings) == 0 && len(is.knownSlugs) > 0 {
			for slug := range is.knownSlugs {
				is.mappings[slug] = slug
			}
		}

		sets = append(sets, is)
	}

	// Sort by priority (lower = checked first).
	sort.Slice(sets, func(i, j int) bool {
		return sets[i].priority < sets[j].priority
	})

	iconSets = sets
}

// fetchAndParseIndex downloads (or reads) an index file and parses it into a
// set of known icon slug names according to the given format and optional field.
func fetchAndParseIndex(indexUrl, format, field string, httpClient *http.Client) (map[string]bool, error) {
	data, err := readIndex(indexUrl, httpClient)
	if err != nil {
		return nil, err
	}
	return parseIndex(data, format, field)
}

// readIndex reads the index from a URL (http/https) or a local file path.
// Local paths can be absolute (e.g., "/config/selfhst-index.json") or
// prefixed with "file://".
func readIndex(url string, httpClient *http.Client) ([]byte, error) {
	// Detect local file paths.
	isLocal := strings.HasPrefix(url, "file://") ||
		(!strings.Contains(url, "://") && strings.HasPrefix(url, "/"))

	if isLocal {
		path := strings.TrimPrefix(url, "file://")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read local index %s: %w", path, err)
		}
		return data, nil
	}

	// HTTP/HTTPS fetch.
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "dumbdock/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// parseIndex parses an index body into a set of known icon slug names.
// Supported formats:
//
//	"map"     — flat JSON object {"key": "slug", …}; values are the slug names.
//	"list"    — JSON array of slug strings ["slug1", "slug2", …].
//	"tree"    — JSON object with an array field of filenames (e.g.
//	            {"svg": ["name.svg", …]}); field selects the array and
//	            ".svg"/".png"/".webp" extensions are stripped.
//	"objects" — JSON array of objects; field specifies the key in each object
//	            whose string value is the slug (e.g. {"Reference": "prunemate"}).
func parseIndex(data []byte, format, field string) (map[string]bool, error) {
	slugs := make(map[string]bool)

	switch format {
	case "map", "":
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse as map: %w", err)
		}
		for _, v := range m {
			slugs[v] = true
		}

	case "list":
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("parse as list: %w", err)
		}
		for _, s := range arr {
			if s != "" {
				slugs[s] = true
			}
		}

	case "tree":
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse as tree: %w", err)
		}
		f := field
		if f == "" {
			f = "svg"
		}
		arr, ok := raw[f].([]interface{})
		if !ok {
			return nil, fmt.Errorf("field %q not found or not an array in tree", f)
		}
		for _, item := range arr {
			name, ok := item.(string)
			if !ok || name == "" {
				continue
			}
			slug := strings.TrimSuffix(name, ".svg")
			slug = strings.TrimSuffix(slug, ".png")
			slug = strings.TrimSuffix(slug, ".webp")
			if slug != "" {
				slugs[slug] = true
			}
		}

	case "objects":
		if field == "" {
			return nil, fmt.Errorf("objects format requires a non-empty field name")
		}
		var arr []map[string]interface{}
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("parse as objects: %w", err)
		}
		for _, obj := range arr {
			raw, ok := obj[field]
			if !ok {
				continue
			}
			slug, ok := raw.(string)
			if !ok || slug == "" {
				continue
			}
			slug = strings.TrimSuffix(slug, ".svg")
			slug = strings.TrimSuffix(slug, ".png")
			slug = strings.TrimSuffix(slug, ".webp")
			if slug != "" {
				slugs[slug] = true
			}
		}

	default:
		return nil, fmt.Errorf("unknown index format %q (expected map/list/tree/objects)", format)
	}

	return slugs, nil
}

// imagePath strips the registry prefix, digest, and tag from a Docker image
// reference and returns the remaining image path.
//
// Examples:
//
//	"docker.n8n.io/n8nio/n8n:latest"       → "n8nio/n8n"
//	"localhost:5000/nginx:latest"          → "nginx"
//	"portainer/portainer-ce:latest"        → "portainer/portainer-ce"
//	"nginx:latest"                         → "nginx"
func imagePath(image string) string {
	// Strip registry prefix first (first component with '.' or ':').
	if i := strings.Index(image, "/"); i >= 0 {
		first := image[:i]
		if strings.Contains(first, ".") || strings.Contains(first, ":") {
			image = image[i+1:]
		}
	}
	// Now strip digest (@sha256:...) and tag (:latest) from the remaining path.
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i >= 0 {
		image = image[:i]
	}
	return image
}

// resolveIcon iterates over all configured icon sets (in priority order) and
// returns the first matching icon URL. If no icon set matches, returns a
// best-guess URL from the highest-priority set with ok=false. The third return
// value is a human-readable diagnostic string describing the resolution result.
func resolveIcon(image string) (string, bool, string) {
	path := imagePath(image)
	if path == "" {
		return "", false, "empty image path"
	}

	last := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		last = path[i+1:]
	}
	if last == "" {
		return "", false, "empty last component"
	}

	for _, is := range iconSets {
		url, ok := is.resolve(path, last)
		if ok {
			return url, true, fmt.Sprintf("matched %q in icon set %q", last, is.name)
		}
	}

	// No match in any set — return a best-guess URL from the first (highest
	// priority) set, but signal failure so the caller knows it's a guess.
	if len(iconSets) > 0 {
		return iconSets[0].url(last), false,
			fmt.Sprintf("no match for %q in %d icon set(s); using guess", last, len(iconSets))
	}
	return "", false, "no icon sets configured"
}

// IconSetAPI is the API representation of an icon set for the /api/icons endpoint.
type IconSetAPI struct {
	Name           string            `json:"name"`
	CDNUrlTemplate string            `json:"cdnUrlTemplate"`
	Mappings       map[string]string `json:"mappings"`
	Slugs          []string          `json:"slugs"`
}

// IconsAPIResponse is the response for the /api/icons endpoint.
type IconsAPIResponse struct {
	IconSets []IconSetAPI `json:"iconSets"`
}

// GetIconsData returns all icon set data for the /api/icons endpoint.
func GetIconsData() *IconsAPIResponse {
	resp := &IconsAPIResponse{}
	for _, is := range iconSets {
		slugs := make([]string, 0, len(is.knownSlugs))
		for s := range is.knownSlugs {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)

		mappings := make(map[string]string, len(is.mappings))
		for k, v := range is.mappings {
			mappings[k] = v
		}

		resp.IconSets = append(resp.IconSets, IconSetAPI{
			Name:           is.name,
			CDNUrlTemplate: is.cdnURLTemplate,
			Mappings:       mappings,
			Slugs:          slugs,
		})
	}
	return resp
}
