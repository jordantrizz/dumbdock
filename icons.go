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

// iconIndexMaxBytes caps every icon index read, local or remote. An index is
// attacker-influenced input (a custom iconSets entry may point anywhere), so a
// body larger than this is refused rather than read into memory (H-04).
const iconIndexMaxBytes = 16 << 20

// iconInfo is what an icon index knows about one slug. The zero value is a
// valid svg icon with no aliases.
type iconInfo struct {
	Base       string            `json:"base,omitempty"`
	Aliases    []string          `json:"aliases,omitempty"`
	Colors     map[string]string `json:"colors,omitempty"`
	Categories []string          `json:"categories,omitempty"`
}

// IconSetConfig defines the configuration for an icon provider.
type IconSetConfig struct {
	Name           string            `json:"name"`
	Enabled        *bool             `json:"enabled,omitempty"`    // false removes the set; absent means enabled
	IndexUrl       string            `json:"indexUrl"`             // URL or file path to fetch the icon index
	CdnUrlTemplate string            `json:"cdnUrlTemplate"`       // CDN URL with {slug}, {base} and {ext} placeholders
	IndexFormat    string            `json:"indexFormat"`          // "map", "list", "tree", "objects", or "metadata"
	IndexField     string            `json:"indexField,omitempty"` // field name for tree and objects formats (e.g. "svg")
	Mappings       map[string]string `json:"mappings,omitempty"`   // image key → icon slug overrides
	Priority       int               `json:"priority"`             // lower = checked first
}

// IconSet represents a resolved icon provider ready for lookups.
type IconSet struct {
	name           string
	cdnURLTemplate string
	indexURL       string
	indexFormat    string
	mappings       map[string]string   // image key → icon slug
	icons          map[string]iconInfo // known slug → index metadata
	aliasIndex     map[string]string   // normalized alias → slug
	slugIndex      map[string]string   // normalized slug → canonical slug
	priority       int
}

// url renders a slug through the set's template. {slug} is always replaced;
// {base} and {ext} are replaced with the icon's file base, so a template that
// names neither keeps its previous behaviour exactly.
func (is *IconSet) url(slug string) string {
	base := is.base(slug)
	u := strings.ReplaceAll(is.cdnURLTemplate, "{slug}", slug)
	u = strings.ReplaceAll(u, "{base}", base)
	return strings.ReplaceAll(u, "{ext}", iconExtension(base))
}

// normalizeIconKey reduces an image name, slug or alias to one comparable key:
// lowercase, spaces and underscores to hyphens, and no repeated or edge
// hyphens. It is deliberately ASCII-safe so a docker image name and an index
// alias reach the same key.
func normalizeIconKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if r == ' ' || r == '_' || r == '-' {
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
	}
	return strings.Trim(b.String(), "-")
}

// buildLookupIndexes fills the normalized slug and alias indexes. Slugs are
// visited in sorted order so an alias shared by two icons resolves the same way
// on every run, and a real slug is never shadowed by an alias.
func (is *IconSet) buildLookupIndexes() {
	is.slugIndex = make(map[string]string, len(is.icons))
	is.aliasIndex = make(map[string]string)

	slugs := make([]string, 0, len(is.icons))
	for slug := range is.icons {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	for _, slug := range slugs {
		if key := normalizeIconKey(slug); key != "" {
			if _, ok := is.slugIndex[key]; !ok {
				is.slugIndex[key] = slug
			}
		}
	}
	for _, slug := range slugs {
		for _, alias := range is.icons[slug].Aliases {
			key := normalizeIconKey(alias)
			if key == "" {
				continue
			}
			if _, ok := is.slugIndex[key]; ok {
				continue
			}
			if _, ok := is.aliasIndex[key]; ok {
				continue
			}
			is.aliasIndex[key] = slug
		}
	}
}

// base returns the file base for a slug, defaulting to svg for a slug the index
// does not describe.
func (is *IconSet) base(slug string) string {
	return iconBase(is.icons[slug].Base)
}

// iconBase normalizes an index-declared base. Only svg, png and webp are
// accepted; anything else, including an empty value, means svg.
func iconBase(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "png":
		return "png"
	case "webp":
		return "webp"
	default:
		return "svg"
	}
}

// iconExtension is the file extension for a base. The template exposes it as
// {ext} so a provider may name it separately from the directory ({base}).
func iconExtension(base string) string {
	return iconBase(base)
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
	if _, ok := is.icons[last]; ok {
		return is.url(last), true
	}

	// 4. Check the last component as a normalized known slug (e.g. "Nextcloud").
	if slug, ok := is.slugIndex[normalizeIconKey(last)]; ok {
		return is.url(slug), true
	}

	// 5. Check the last component as a normalized index alias (e.g. "WireGuard").
	if slug, ok := is.aliasIndex[normalizeIconKey(last)]; ok {
		return is.url(slug), true
	}

	return "", false
}

// iconSets is the global slice of configured icon sets, ordered by priority.
var iconSets []*IconSet

// builtinIconSets returns the icon sets dumbdock ships with, before config.
// The embedded icons.json provides the selfhst image-name mappings; the
// dashboard-icons index is the repository's metadata.json, because it is the
// only index that says whether an icon is an svg or a png.
func builtinIconSets() []IconSetConfig {
	var defaultMappings map[string]string
	if err := json.Unmarshal(iconsData, &defaultMappings); err != nil {
		log.Printf("warning: failed to parse embedded icons.json: %v", err)
		defaultMappings = map[string]string{}
	}

	return []IconSetConfig{
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
			IndexUrl:       "https://raw.githubusercontent.com/homarr-labs/dashboard-icons/refs/heads/main/metadata.json",
			CdnUrlTemplate: "https://cdn.statically.io/gh/homarr-labs/dashboard-icons/main/{base}/{slug}.{base}",
			IndexFormat:    "metadata",
			Priority:       1,
		},
	}
}

// mergeIconSetConfig layers a config entry over a set it names. An empty field
// leaves the lower layer's value; mappings merge with the config winning.
func mergeIconSetConfig(base, override IconSetConfig) IconSetConfig {
	merged := base
	if override.IndexUrl != "" {
		merged.IndexUrl = override.IndexUrl
	}
	if override.CdnUrlTemplate != "" {
		merged.CdnUrlTemplate = override.CdnUrlTemplate
	}
	if override.IndexFormat != "" {
		merged.IndexFormat = override.IndexFormat
	}
	if override.IndexField != "" {
		merged.IndexField = override.IndexField
	}
	if override.Priority != 0 {
		merged.Priority = override.Priority
	}
	if override.Enabled != nil {
		merged.Enabled = override.Enabled
	}
	if len(override.Mappings) > 0 {
		mappings := make(map[string]string, len(base.Mappings)+len(override.Mappings))
		for k, v := range base.Mappings {
			mappings[k] = v
		}
		for k, v := range override.Mappings {
			mappings[k] = v
		}
		merged.Mappings = mappings
	}
	return merged
}

// resolveIconSets merges the built-in sets with config: an entry that names a
// built-in set (case-insensitive) overrides it field by field, any other name
// appends a set, and enabled:false removes a set. The result is ordered by
// priority, with the built-ins first when priorities are equal.
func resolveIconSets(cfg *overrideConfig) []IconSetConfig {
	sets := builtinIconSets()
	if cfg == nil || len(cfg.IconSets) == 0 {
		sortIconSetConfigs(sets)
		return sets
	}

	index := make(map[string]int, len(sets))
	for i, set := range sets {
		index[strings.ToLower(strings.TrimSpace(set.Name))] = i
	}

	for _, override := range cfg.IconSets {
		name := strings.TrimSpace(override.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if i, ok := index[key]; ok {
			sets[i] = mergeIconSetConfig(sets[i], override)
			continue
		}
		merged := override
		merged.Name = name
		sets = append(sets, merged)
		index[key] = len(sets) - 1
	}

	effective := sets[:0]
	for _, set := range sets {
		if set.isEnabled() && strings.TrimSpace(set.Name) != "" {
			effective = append(effective, set)
		}
	}
	sortIconSetConfigs(effective)
	return effective
}

// sortIconSetConfigs orders sets by priority and keeps equal priorities in
// their existing order, so a config change never reshuffles unrelated sets.
func sortIconSetConfigs(sets []IconSetConfig) {
	sort.SliceStable(sets, func(i, j int) bool {
		return sets[i].Priority < sets[j].Priority
	})
}

// initIconSets builds the icon set list from config, fetching indexes as needed.
func initIconSets(cfg *overrideConfig, httpClient *http.Client) {
	configs := resolveIconSets(cfg)

	sets := make([]*IconSet, 0, len(configs))

	for _, cc := range configs {
		is := &IconSet{
			name:           cc.Name,
			cdnURLTemplate: cc.CdnUrlTemplate,
			indexURL:       cc.IndexUrl,
			indexFormat:    cc.IndexFormat,
			mappings:       make(map[string]string),
			icons:          make(map[string]iconInfo),
			priority:       cc.Priority,
		}

		// Copy static mappings into the set.
		// Both the key (image-derived name) and the value (icon slug) are
		// tracked so that slug-only resolution (step 3 in resolve) works.
		for k, v := range cc.Mappings {
			is.mappings[k] = v
			if _, ok := is.icons[v]; !ok {
				is.icons[v] = iconInfo{Base: "svg"}
			}
		}

		// Fetch and parse the remote / local index to discover available slugs
		// and what the index knows about each one.
		if cc.IndexUrl != "" {
			icons, err := fetchAndParseIndex(cc.IndexUrl, cc.IndexFormat, cc.IndexField, httpClient)
			if err != nil {
				log.Printf("warning: icon set %q: failed to fetch/parse index from %s: %v",
					cc.Name, cc.IndexUrl, err)
			} else {
				for slug, info := range icons {
					is.icons[slug] = info
				}
			}
		}

		// If a set has no explicit mappings but discovered slugs from its
		// index, auto-generate identity mappings (slug → slug) so that
		// every slug appears as a direct-match entry in the icons table
		// instead of showing "— (known slug)".
		if len(is.mappings) == 0 && len(is.icons) > 0 {
			for slug := range is.icons {
				is.mappings[slug] = slug
			}
		}

		is.buildLookupIndexes()

		sets = append(sets, is)
	}

	iconSets = sets
}

// isEnabled reports whether the entry should produce a set.
func (c IconSetConfig) isEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// fetchAndParseIndex downloads (or reads) an index file and parses it into the
// icons it describes according to the given format and optional field.
func fetchAndParseIndex(indexUrl, format, field string, httpClient *http.Client) (map[string]iconInfo, error) {
	data, err := readIndex(indexUrl, httpClient)
	if err != nil {
		return nil, err
	}
	return parseIndex(data, format, field)
}

// readIndex reads the index from a URL (http/https) or a local file path.
// Local paths can be absolute (e.g., "/config/selfhst-index.json") or
// prefixed with "file://". Every read is capped at iconIndexMaxBytes.
func readIndex(url string, httpClient *http.Client) ([]byte, error) {
	// Detect local file paths.
	isLocal := strings.HasPrefix(url, "file://") ||
		(!strings.Contains(url, "://") && strings.HasPrefix(url, "/"))

	if isLocal {
		path := strings.TrimPrefix(url, "file://")
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read local index %s: %w", path, err)
		}
		defer f.Close()
		if info, err := f.Stat(); err == nil && info.Size() > int64(iconIndexMaxBytes) {
			return nil, indexTooLarge(path)
		}
		return readIndexBody(f, path)
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
	if resp.ContentLength > int64(iconIndexMaxBytes) {
		return nil, indexTooLarge(url)
	}

	return readIndexBody(resp.Body, url)
}

// readIndexBody reads at most iconIndexMaxBytes from r. One byte past the cap
// is enough to refuse the body, so a truncated index is an error and never a
// smaller valid-looking one.
func readIndexBody(r io.Reader, source string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(iconIndexMaxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read index from %s: %w", source, err)
	}
	if len(data) > iconIndexMaxBytes {
		return nil, indexTooLarge(source)
	}
	return data, nil
}

// indexTooLarge is the single error for an index over the cap.
func indexTooLarge(source string) error {
	return fmt.Errorf("index from %s exceeds %d MiB limit", source, iconIndexMaxBytes>>20)
}

// trimIconExtension removes a known image extension from an index filename.
func trimIconExtension(name string) string {
	name = strings.TrimSuffix(name, ".svg")
	name = strings.TrimSuffix(name, ".png")
	return strings.TrimSuffix(name, ".webp")
}

// parseIndex parses an index body into the icon slugs it describes.
// Supported formats:
//
//	"map"      — flat JSON object {"key": "slug", …}; values are the slug names.
//	"list"     — JSON array of slug strings ["slug1", "slug2", …].
//	"tree"     — JSON object with an array field of filenames (e.g.
//	             {"svg": ["name.svg", …]}); field selects the array, sets the
//	             base when it is svg/png/webp, and extensions are stripped.
//	"objects"  — JSON array of objects; field specifies the key in each object
//	             whose string value is the slug (e.g. {"Reference": "prunemate"}).
//	"metadata" — JSON object keyed by slug, each value carrying "base",
//	             "aliases", "colors" and "categories" (the
//	             homarr-labs/dashboard-icons metadata.json shape).
func parseIndex(data []byte, format, field string) (map[string]iconInfo, error) {
	icons := make(map[string]iconInfo)

	switch format {
	case "map", "":
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse as map: %w", err)
		}
		for _, v := range m {
			if v != "" {
				icons[v] = iconInfo{Base: "svg"}
			}
		}

	case "list":
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("parse as list: %w", err)
		}
		for _, s := range arr {
			if s != "" {
				icons[s] = iconInfo{Base: "svg"}
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
		base := iconBase(f)
		for _, item := range arr {
			name, ok := item.(string)
			if !ok || name == "" {
				continue
			}
			if slug := trimIconExtension(name); slug != "" {
				icons[slug] = iconInfo{Base: base}
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
			if slug = trimIconExtension(slug); slug != "" {
				icons[slug] = iconInfo{Base: "svg"}
			}
		}

	case "metadata":
		var raw map[string]struct {
			Base       string            `json:"base"`
			Aliases    []string          `json:"aliases"`
			Colors     map[string]string `json:"colors"`
			Categories []string          `json:"categories"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse as metadata: %w", err)
		}
		for slug, info := range raw {
			if slug == "" {
				continue
			}
			icons[slug] = iconInfo{
				Base:       iconBase(info.Base),
				Aliases:    info.Aliases,
				Colors:     info.Colors,
				Categories: info.Categories,
			}
		}

	default:
		return nil, fmt.Errorf("unknown index format %q (expected map/list/tree/objects/metadata)", format)
	}

	return icons, nil
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
	Name           string              `json:"name"`
	CDNUrlTemplate string              `json:"cdnUrlTemplate"`
	IndexUrl       string              `json:"indexUrl,omitempty"`
	IndexFormat    string              `json:"indexFormat,omitempty"`
	Mappings       map[string]string   `json:"mappings"`
	Slugs          []string            `json:"slugs"`
	Bases          map[string]string   `json:"bases,omitempty"`   // slug → svg|png|webp
	Aliases        map[string][]string `json:"aliases,omitempty"` // slug → index aliases
}

// IconsAPIResponse is the response for the /api/icons endpoint.
type IconsAPIResponse struct {
	IconSets []IconSetAPI `json:"iconSets"`
}

// GetIconsData returns all icon set data for the /api/icons endpoint.
func GetIconsData() *IconsAPIResponse {
	resp := &IconsAPIResponse{}
	for _, is := range iconSets {
		slugs := make([]string, 0, len(is.icons))
		for s := range is.icons {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)

		mappings := make(map[string]string, len(is.mappings))
		for k, v := range is.mappings {
			mappings[k] = v
		}

		bases := make(map[string]string, len(is.icons))
		aliases := make(map[string][]string)
		for slug, info := range is.icons {
			bases[slug] = is.base(slug)
			if len(info.Aliases) > 0 {
				aliases[slug] = append([]string(nil), info.Aliases...)
			}
		}

		resp.IconSets = append(resp.IconSets, IconSetAPI{
			Name:           is.name,
			CDNUrlTemplate: is.cdnURLTemplate,
			IndexUrl:       is.indexURL,
			IndexFormat:    is.indexFormat,
			Mappings:       mappings,
			Slugs:          slugs,
			Bases:          bases,
			Aliases:        aliases,
		})
	}
	return resp
}
