package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseIndexMap(t *testing.T) {
	icons, err := parseIndex([]byte(`{"actualbudget":"actual-budget","nginx":"nginx"}`), "map", "")
	if err != nil {
		t.Fatalf("parseIndex: %v", err)
	}
	if len(icons) != 2 {
		t.Fatalf("got %d icons, want 2", len(icons))
	}
	for _, slug := range []string{"actual-budget", "nginx"} {
		info, ok := icons[slug]
		if !ok {
			t.Fatalf("missing slug %q", slug)
		}
		if info.Base != "svg" {
			t.Errorf("%s base = %q, want svg", slug, info.Base)
		}
	}
}

func TestParseIndexList(t *testing.T) {
	icons, err := parseIndex([]byte(`["alpha","","beta"]`), "list", "")
	if err != nil {
		t.Fatalf("parseIndex: %v", err)
	}
	if len(icons) != 2 {
		t.Fatalf("got %d icons, want 2 (the empty entry is skipped)", len(icons))
	}
	if _, ok := icons["alpha"]; !ok {
		t.Error("missing alpha")
	}
}

func TestParseIndexTreeBase(t *testing.T) {
	body := []byte(`{"svg":["alpha.svg","beta.svg"],"png":["gamma.png","delta.webp"]}`)

	svgs, err := parseIndex(body, "tree", "svg")
	if err != nil {
		t.Fatalf("tree/svg: %v", err)
	}
	if len(svgs) != 2 {
		t.Fatalf("tree/svg got %d icons, want 2", len(svgs))
	}
	if svgs["alpha"].Base != "svg" {
		t.Errorf("alpha base = %q, want svg", svgs["alpha"].Base)
	}

	pngs, err := parseIndex(body, "tree", "png")
	if err != nil {
		t.Fatalf("tree/png: %v", err)
	}
	if len(pngs) != 2 {
		t.Fatalf("tree/png got %d icons, want 2", len(pngs))
	}
	if pngs["gamma"].Base != "png" {
		t.Errorf("gamma base = %q, want png", pngs["gamma"].Base)
	}
	if _, ok := pngs["delta"]; !ok {
		t.Error("delta.webp was not stripped to delta")
	}
}

func TestParseIndexTreeDefaultsToSvg(t *testing.T) {
	icons, err := parseIndex([]byte(`{"svg":["alpha.svg"]}`), "tree", "")
	if err != nil {
		t.Fatalf("parseIndex: %v", err)
	}
	if icons["alpha"].Base != "svg" {
		t.Errorf("base = %q, want svg", icons["alpha"].Base)
	}

	if _, err := parseIndex([]byte(`{"png":["alpha.png"]}`), "tree", "webp"); err == nil {
		t.Error("expected an error for a missing tree field")
	}
}

func TestParseIndexObjects(t *testing.T) {
	body := []byte(`[{"Reference":"prunemate"},{"Reference":"other.svg"},{"Other":"skip"}]`)
	icons, err := parseIndex(body, "objects", "Reference")
	if err != nil {
		t.Fatalf("parseIndex: %v", err)
	}
	if len(icons) != 2 {
		t.Fatalf("got %d icons, want 2", len(icons))
	}
	if icons["prunemate"].Base != "svg" {
		t.Errorf("base = %q, want svg", icons["prunemate"].Base)
	}
	if _, err := parseIndex(body, "objects", ""); err == nil {
		t.Error("expected an error for an empty objects field")
	}
}

func TestParseIndexMetadata(t *testing.T) {
	body := []byte(`{
		"portainer": {"base":"svg","aliases":[],"categories":[],"colors":{"dark":"portainer-dark","light":"portainer"}},
		"wbo": {"base":"png","aliases":["whiteboard"],"categories":["Organization"]},
		"odd": {"base":"gif","aliases":null},
		"blank": {},
		"": {"base":"png"}
	}`)
	icons, err := parseIndex(body, "metadata", "")
	if err != nil {
		t.Fatalf("parseIndex: %v", err)
	}
	if len(icons) != 4 {
		t.Fatalf("got %d icons, want 4 (the empty slug is skipped)", len(icons))
	}
	if got := icons["portainer"].Colors["dark"]; got != "portainer-dark" {
		t.Errorf("portainer dark variant = %q, want portainer-dark", got)
	}
	if icons["portainer"].Base != "svg" {
		t.Errorf("portainer base = %q, want svg", icons["portainer"].Base)
	}
	wbo := icons["wbo"]
	if wbo.Base != "png" {
		t.Errorf("wbo base = %q, want png", wbo.Base)
	}
	if len(wbo.Aliases) != 1 || wbo.Aliases[0] != "whiteboard" {
		t.Errorf("wbo aliases = %v, want [whiteboard]", wbo.Aliases)
	}
	if len(wbo.Categories) != 1 || wbo.Categories[0] != "Organization" {
		t.Errorf("wbo categories = %v, want [Organization]", wbo.Categories)
	}
	if icons["odd"].Base != "svg" {
		t.Errorf("odd base = %q, want svg for an unknown base", icons["odd"].Base)
	}
	if icons["blank"].Base != "svg" {
		t.Errorf("blank base = %q, want svg for an absent base", icons["blank"].Base)
	}
}

func TestParseIndexErrors(t *testing.T) {
	cases := []struct {
		name   string
		format string
		field  string
		body   string
	}{
		{"map", "map", "", `[`},
		{"list", "list", "", `{}`},
		{"tree-invalid", "tree", "svg", `[`},
		{"tree-field", "tree", "webp", `{"svg":[]}`},
		{"objects", "objects", "Reference", `{}`},
		{"metadata", "metadata", "", `[]`},
		{"unknown", "nope", "", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseIndex([]byte(tc.body), tc.format, tc.field); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestIconBase(t *testing.T) {
	cases := map[string]string{
		"":      "svg",
		"svg":   "svg",
		"SVG":   "svg",
		" png ": "png",
		"PNG":   "png",
		"webp":  "webp",
		"gif":   "svg",
	}
	for in, want := range cases {
		if got := iconBase(in); got != want {
			t.Errorf("iconBase(%q) = %q, want %q", in, got, want)
		}
		if want != "svg" && iconExtension(in) != want {
			t.Errorf("iconExtension(%q) = %q, want %q", in, iconExtension(in), want)
		}
	}
}

func TestReadIndexHTTP(t *testing.T) {
	big := bytes.Repeat([]byte("a"), iconIndexMaxBytes+1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "dumbdock/1.0" {
			t.Errorf("User-Agent = %q, want dumbdock/1.0", got)
		}
		io.WriteString(w, `{"svg":["alpha.svg"]}`)
	})
	mux.HandleFunc("/big-cl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(iconIndexMaxBytes+1))
	})
	mux.HandleFunc("/big-body", func(w http.ResponseWriter, r *http.Request) {
		// Flush the header first so the response is chunked: the code must
		// catch the over-cap body through the LimitReader, not Content-Length.
		w.(http.Flusher).Flush()
		w.Write(big)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	data, err := readIndex(srv.URL+"/ok", srv.Client())
	if err != nil {
		t.Fatalf("readIndex ok: %v", err)
	}
	if !strings.Contains(string(data), "alpha") {
		t.Errorf("body = %q, want the served index", data)
	}

	for _, path := range []string{"/big-cl", "/big-body"} {
		if _, err := readIndex(srv.URL+path, srv.Client()); err == nil {
			t.Fatalf("%s: expected an over-limit error", path)
		} else if !strings.Contains(err.Error(), "16 MiB") {
			t.Errorf("%s error = %q, want the 16 MiB limit", path, err)
		}
	}

	if _, err := readIndex(srv.URL+"/missing", srv.Client()); err == nil {
		t.Error("expected an HTTP error for a 404")
	}
}

func TestReadIndexLocal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	if err := os.WriteFile(path, []byte(`{"svg":["alpha.svg"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{path, "file://" + path} {
		if _, err := readIndex(source, http.DefaultClient); err != nil {
			t.Fatalf("readIndex(%q): %v", source, err)
		}
	}

	bigPath := filepath.Join(dir, "big.json")
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(iconIndexMaxBytes) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := readIndex(bigPath, http.DefaultClient); err == nil {
		t.Error("expected an over-limit error for a local file")
	} else if !strings.Contains(err.Error(), "16 MiB") {
		t.Errorf("local error = %q, want the 16 MiB limit", err)
	}

	if _, err := readIndex(filepath.Join(dir, "absent.json"), http.DefaultClient); err == nil {
		t.Error("expected an error for a missing local file")
	}
}

func testIconSet(t *testing.T, template string, icons map[string]iconInfo, mappings map[string]string) *IconSet {
	t.Helper()
	if mappings == nil {
		mappings = map[string]string{}
	}
	is := &IconSet{
		name:           "test",
		cdnURLTemplate: template,
		mappings:       mappings,
		icons:          icons,
	}
	is.buildLookupIndexes()
	return is
}

func TestIconSetURL(t *testing.T) {
	is := testIconSet(t, "https://cdn.example/{base}/{slug}.{ext}", map[string]iconInfo{
		"alpha": {Base: "svg"},
		"beta":  {Base: "png"},
		"gamma": {},
	}, nil)

	cases := map[string]string{
		"alpha":     "https://cdn.example/svg/alpha.svg",
		"beta":      "https://cdn.example/png/beta.png",
		"gamma":     "https://cdn.example/svg/gamma.svg",
		"unindexed": "https://cdn.example/svg/unindexed.svg",
	}
	for slug, want := range cases {
		if got := is.url(slug); got != want {
			t.Errorf("url(%q) = %q, want %q", slug, got, want)
		}
	}

	plain := testIconSet(t, "https://cdn.example/svg/{slug}.svg", map[string]iconInfo{
		"beta": {Base: "png"},
	}, nil)
	if got := plain.url("beta"); got != "https://cdn.example/svg/beta.svg" {
		t.Errorf("a template without a base placeholder changed: %q", got)
	}
}

func TestNormalizeIconKey(t *testing.T) {
	cases := map[string]string{
		"wireguard":      "wireguard",
		"WireGuard":      "wireguard",
		"Open WebUI":     "open-webui",
		" air_trail ":    "air-trail",
		"--a--b--":       "a-b",
		"Node-RED":       "node-red",
		"":               "",
		"   ":            "",
		"multi  spaces":  "multi-spaces",
		"trailing-dash-": "trailing-dash",
	}
	for in, want := range cases {
		if got := normalizeIconKey(in); got != want {
			t.Errorf("normalizeIconKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOrder(t *testing.T) {
	is := testIconSet(t, "https://cdn.example/{base}/{slug}.{ext}", map[string]iconInfo{
		"portainer":  {Base: "svg"},
		"minio":      {},
		"nginx":      {},
		"nextcloud":  {},
		"kali-linux": {Aliases: []string{"kali"}},
		"air-trail":  {Aliases: []string{"airtrail"}},
		"wireguard":  {Aliases: []string{"WireGuard"}},
		"open-webui": {},
		"other-tool": {Aliases: []string{"Open WebUI"}},
	}, map[string]string{
		"portainer/portainer-ce": "portainer",
		"mc":                     "minio",
	})

	cases := []struct {
		path string
		last string
		want string
	}{
		{"portainer/portainer-ce", "portainer-ce", "https://cdn.example/svg/portainer.svg"}, // mapping, full path
		{"mc", "mc", "https://cdn.example/svg/minio.svg"},                                   // mapping, last component
		{"nginx", "nginx", "https://cdn.example/svg/nginx.svg"},                             // exact slug
		{"Nextcloud", "Nextcloud", "https://cdn.example/svg/nextcloud.svg"},                 // normalized slug
		{"kali", "kali", "https://cdn.example/svg/kali-linux.svg"},                          // alias
		{"airtrail", "airtrail", "https://cdn.example/svg/air-trail.svg"},                   // alias
		{"WireGuard", "WireGuard", "https://cdn.example/svg/wireguard.svg"},                 // mixed-case alias
		{"Open WebUI", "Open WebUI", "https://cdn.example/svg/open-webui.svg"},              // a slug beats its own alias
	}
	for _, tc := range cases {
		got, ok := is.resolve(tc.path, tc.last)
		if !ok {
			t.Errorf("resolve(%q, %q) did not match", tc.path, tc.last)
			continue
		}
		if got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.path, tc.last, got, tc.want)
		}
	}

	if _, ok := is.resolve("nope", "nope"); ok {
		t.Error("an unknown component matched")
	}
}

func TestResolveAliasCollisions(t *testing.T) {
	is := testIconSet(t, "https://cdn.example/{slug}.svg", map[string]iconInfo{
		"aaa":   {Aliases: []string{"Shared Alias"}},
		"bbb":   {Aliases: []string{"shared alias", "shared-alias"}},
		"nginx": {Aliases: []string{"web"}},
	}, nil)

	// Sorted slug order decides, so the result never depends on map order.
	if got, _ := is.resolve("Shared Alias", "Shared Alias"); !strings.Contains(got, "/aaa.svg") {
		t.Errorf("colliding alias resolved to %q, want aaa", got)
	}
	if got, _ := is.resolve("Web", "Web"); !strings.Contains(got, "/nginx.svg") {
		t.Errorf("alias shadowing a real slug resolved to %q, want nginx", got)
	}
}

func TestResolveIconFallbackUsesBase(t *testing.T) {
	previous := iconSets
	defer func() { iconSets = previous }()

	iconSets = []*IconSet{testIconSet(t, "https://cdn.example/{base}/{slug}.{ext}", map[string]iconInfo{
		"small": {Base: "png"},
	}, nil)}

	url, ok, diag := resolveIcon("small")
	if !ok || url != "https://cdn.example/png/small.png" {
		t.Errorf("resolveIcon(small) = %q, %v; want the png URL", url, ok)
	}
	if !strings.Contains(diag, `matched "small"`) {
		t.Errorf("diagnostic = %q, want a matched line", diag)
	}

	url, ok, diag = resolveIcon("acme/unknown")
	if ok {
		t.Error("an unknown image reported a match")
	}
	if url != "https://cdn.example/svg/unknown.svg" {
		t.Errorf("guess = %q, want the svg fallback", url)
	}
	if !strings.Contains(diag, "using guess") {
		t.Errorf("diagnostic = %q, want the guess wording", diag)
	}

	if url, ok, _ := resolveIcon(""); ok || url != "" {
		t.Errorf("resolveIcon(empty) = %q, %v; want no URL", url, ok)
	}
}

func testBoolPtr(v bool) *bool {
	return &v
}

func iconSetNamesOf(sets []IconSetConfig) []string {
	names := make([]string, 0, len(sets))
	for _, set := range sets {
		names = append(names, set.Name)
	}
	return names
}

func TestBuiltinIconSets(t *testing.T) {
	sets := builtinIconSets()
	if len(sets) != 2 {
		t.Fatalf("got %d built-in sets, want 2", len(sets))
	}

	byName := make(map[string]IconSetConfig, len(sets))
	for _, set := range sets {
		byName[set.Name] = set
	}

	selfhst, ok := byName["selfhst"]
	if !ok {
		t.Fatal("missing the selfhst set")
	}
	if selfhst.IndexFormat != "objects" || selfhst.IndexField != "Reference" {
		t.Errorf("selfhst format = %q/%q, want objects/Reference", selfhst.IndexFormat, selfhst.IndexField)
	}
	if len(selfhst.Mappings) == 0 {
		t.Error("selfhst has no embedded mappings")
	}

	dash, ok := byName["dashboard-icons"]
	if !ok {
		t.Fatal("missing the dashboard-icons set")
	}
	if dash.IndexFormat != "metadata" {
		t.Errorf("dashboard-icons format = %q, want metadata", dash.IndexFormat)
	}
	if !strings.Contains(dash.IndexUrl, "metadata.json") {
		t.Errorf("dashboard-icons index = %q, want metadata.json", dash.IndexUrl)
	}
	if !strings.Contains(dash.CdnUrlTemplate, "cdn.statically.io") {
		t.Errorf("dashboard-icons template = %q, want the statically.io host", dash.CdnUrlTemplate)
	}
	if !strings.Contains(dash.CdnUrlTemplate, "{base}") {
		t.Errorf("dashboard-icons template = %q, want the {base} placeholder", dash.CdnUrlTemplate)
	}
	for _, value := range []string{dash.IndexUrl, dash.CdnUrlTemplate} {
		if strings.Contains(value, "cdn.jsdelivr.net") {
			t.Errorf("dashboard-icons still points at jsDelivr: %q", value)
		}
	}
	if dash.Priority <= selfhst.Priority {
		t.Errorf("dashboard-icons priority %d, want it after selfhst (%d)", dash.Priority, selfhst.Priority)
	}
}

func TestResolveIconSets(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		sets := resolveIconSets(nil)
		got := iconSetNamesOf(sets)
		want := []string{"selfhst", "dashboard-icons"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("names = %v, want %v", got, want)
		}
	})

	t.Run("disable one built-in", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "dashboard-icons", Enabled: testBoolPtr(false)},
		}})
		got := iconSetNamesOf(sets)
		if len(got) != 1 || got[0] != "selfhst" {
			t.Fatalf("names = %v, want [selfhst]", got)
		}
	})

	t.Run("disable both built-ins", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "dashboard-icons", Enabled: testBoolPtr(false)},
			{Name: "selfhst", Enabled: testBoolPtr(false)},
		}})
		if len(sets) != 0 {
			t.Fatalf("got %d sets, want none", len(sets))
		}
	})

	t.Run("override one field", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "dashboard-icons", CdnUrlTemplate: "https://example.com/{base}/{slug}.{ext}"},
		}})
		var dash IconSetConfig
		for _, set := range sets {
			if set.Name == "dashboard-icons" {
				dash = set
			}
		}
		if dash.CdnUrlTemplate != "https://example.com/{base}/{slug}.{ext}" {
			t.Errorf("template = %q, want the override", dash.CdnUrlTemplate)
		}
		if dash.IndexFormat != "metadata" {
			t.Errorf("format = %q, want the inherited metadata", dash.IndexFormat)
		}
		if !strings.Contains(dash.IndexUrl, "metadata.json") {
			t.Errorf("index = %q, want the inherited metadata.json", dash.IndexUrl)
		}
	})

	t.Run("mappings merge", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "selfhst", Mappings: map[string]string{"myapp": "my-slug", "nginx": "custom-nginx"}},
		}})
		var selfhst IconSetConfig
		for _, set := range sets {
			if set.Name == "selfhst" {
				selfhst = set
			}
		}
		if selfhst.Mappings["myapp"] != "my-slug" {
			t.Error("config mapping was not added")
		}
		if selfhst.Mappings["nginx"] != "custom-nginx" {
			t.Error("config mapping did not win over the embedded one")
		}
		if len(selfhst.Mappings) < 20 {
			t.Errorf("embedded mappings were dropped: %d remain", len(selfhst.Mappings))
		}
	})

	t.Run("append a custom set", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "my-db", CdnUrlTemplate: "https://example.com/{slug}.svg", IndexFormat: "list", IndexUrl: "https://example.com/list.json", Priority: 2},
		}})
		got := iconSetNamesOf(sets)
		want := []string{"selfhst", "dashboard-icons", "my-db"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("names = %v, want %v", got, want)
		}
	})

	t.Run("custom priority sorts first", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "my-db", CdnUrlTemplate: "https://example.com/{slug}.svg", IndexUrl: "https://example.com/list.json", IndexFormat: "list", Priority: -1},
		}})
		if sets[0].Name != "my-db" {
			t.Errorf("first set = %q, want my-db", sets[0].Name)
		}
	})

	t.Run("duplicate names last wins", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "my-db", CdnUrlTemplate: "https://one.example/{slug}.svg", IndexUrl: "https://one.example/list.json", IndexFormat: "list"},
			{Name: "my-db", CdnUrlTemplate: "https://two.example/{slug}.svg"},
		}})
		count := 0
		for _, set := range sets {
			if set.Name == "my-db" {
				count++
				if set.CdnUrlTemplate != "https://two.example/{slug}.svg" {
					t.Errorf("template = %q, want the last entry", set.CdnUrlTemplate)
				}
				if set.IndexUrl != "https://one.example/list.json" {
					t.Errorf("index = %q, want it inherited from the first entry", set.IndexUrl)
				}
			}
		}
		if count != 1 {
			t.Errorf("got %d my-db sets, want 1", count)
		}
	})

	t.Run("empty name is ignored", func(t *testing.T) {
		sets := resolveIconSets(&overrideConfig{IconSets: []IconSetConfig{
			{Name: "  ", CdnUrlTemplate: "https://example.com/{slug}.svg"},
		}})
		if len(sets) != 2 {
			t.Fatalf("got %d sets, want the two built-ins", len(sets))
		}
	})
}

func TestGetIconsData(t *testing.T) {
	previous := iconSets
	defer func() { iconSets = previous }()

	is := testIconSet(t, "https://cdn.example/{base}/{slug}.{ext}", map[string]iconInfo{
		"alpha": {Base: "svg"},
		"wbo":   {Base: "png", Aliases: []string{"whiteboard", "board"}},
	}, map[string]string{"whiteboard-app": "wbo"})
	is.indexURL = "https://example.com/metadata.json"
	is.indexFormat = "metadata"
	iconSets = []*IconSet{is}

	resp := GetIconsData()
	if len(resp.IconSets) != 1 {
		t.Fatalf("got %d sets, want 1", len(resp.IconSets))
	}
	set := resp.IconSets[0]
	if set.IndexUrl != "https://example.com/metadata.json" || set.IndexFormat != "metadata" {
		t.Errorf("source = %q/%q, want the index url and format", set.IndexUrl, set.IndexFormat)
	}
	if set.Bases["wbo"] != "png" || set.Bases["alpha"] != "svg" {
		t.Errorf("bases = %v, want wbo=png alpha=svg", set.Bases)
	}
	if got := set.Aliases["wbo"]; len(got) != 2 || got[0] != "whiteboard" {
		t.Errorf("aliases = %v, want the index aliases", got)
	}
	if strings.Join(set.Slugs, ",") != "alpha,wbo" {
		t.Errorf("slugs = %v, want sorted", set.Slugs)
	}

	// The payload is a copy: a consumer must not be able to mutate the set.
	set.Bases["wbo"] = "svg"
	set.Aliases["wbo"][0] = "mutated"
	set.Mappings["whiteboard-app"] = "mutated"
	if is.icons["wbo"].Base != "png" || is.icons["wbo"].Aliases[0] != "whiteboard" {
		t.Error("mutating the payload changed the live icon metadata")
	}
	if is.mappings["whiteboard-app"] != "wbo" {
		t.Error("mutating the payload changed the live mappings")
	}
}

// TestLiveDashboardIcons checks the real index and the real image host. It is
// skipped by default because it needs outbound HTTPS; run it with
// DUMBDOCK_ICON_LIVE=1.
func TestLiveDashboardIcons(t *testing.T) {
	if os.Getenv("DUMBDOCK_ICON_LIVE") != "1" {
		t.Skip("set DUMBDOCK_ICON_LIVE=1 to check the live dashboard-icons index and CDN")
	}

	var dash *IconSetConfig
	sets := resolveIconSets(nil)
	for i := range sets {
		if sets[i].Name == "dashboard-icons" {
			dash = &sets[i]
		}
	}
	if dash == nil {
		t.Fatal("the dashboard-icons set is missing from the built-ins")
	}
	if strings.Contains(dash.CdnUrlTemplate, "cdn.jsdelivr.net") {
		t.Fatalf("dashboard-icons still points at jsDelivr: %q", dash.CdnUrlTemplate)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	icons, err := fetchAndParseIndex(dash.IndexUrl, dash.IndexFormat, dash.IndexField, client)
	if err != nil {
		t.Fatalf("fetch the live index: %v", err)
	}
	if len(icons) < 3000 {
		t.Fatalf("live index has %d icons, want at least 3000", len(icons))
	}

	is := &IconSet{
		name:           dash.Name,
		cdnURLTemplate: dash.CdnUrlTemplate,
		mappings:       map[string]string{},
		icons:          icons,
	}
	is.buildLookupIndexes()

	svgSlug, pngSlug := "", ""
	for _, slug := range []string{"portainer", "nginx", "nextcloud"} {
		if info, ok := icons[slug]; ok && info.Base == "svg" {
			svgSlug = slug
			break
		}
	}
	for _, slug := range []string{"wbo", "ntopng", "filepizza"} {
		if info, ok := icons[slug]; ok && info.Base == "png" {
			pngSlug = slug
			break
		}
	}
	if svgSlug == "" || pngSlug == "" {
		t.Fatalf("could not find both an svg and a png icon (svg=%q png=%q)", svgSlug, pngSlug)
	}

	for _, tc := range []struct {
		label string
		slug  string
		base  string
	}{
		{"svg", svgSlug, "svg"},
		{"png", pngSlug, "png"},
	} {
		url := is.url(tc.slug)
		if !strings.Contains(url, "/"+tc.base+"/") {
			t.Errorf("%s url = %q, want a /%s/ path", tc.label, url, tc.base)
		}
		req, err := http.NewRequest(http.MethodHead, url, nil)
		if err != nil {
			t.Fatalf("build request for %s: %v", url, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HEAD %s: %v", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s: HTTP %d, want 200", url, resp.StatusCode)
		}
		t.Logf("%s icon: %s", tc.label, url)
	}
}
