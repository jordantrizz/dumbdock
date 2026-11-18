package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// declaredIssueCategoryPattern finds every category constant declared in
// issues.go, so a new category cannot be added without a catalog entry and a
// Help section.
var declaredIssueCategoryPattern = regexp.MustCompile(`issueCategory\s*=\s*"([a-z0-9-]+)"`)

// helpAnchorPattern finds the Help sections that document a category.
var helpAnchorPattern = regexp.MustCompile(`id="(help-issue-[a-z0-9-]+)"`)

func declaredIssueCategories(t *testing.T) map[issueCategory]bool {
	t.Helper()
	data, err := os.ReadFile("issues.go")
	if err != nil {
		t.Fatalf("read issues.go: %v", err)
	}
	matches := declaredIssueCategoryPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("found no issueCategory declarations in issues.go; the drift scan is broken")
	}
	out := make(map[issueCategory]bool, len(matches))
	for _, m := range matches {
		out[issueCategory(m[1])] = true
	}
	return out
}

// TestIssueCatalogCoversEveryDeclaredCategory is the drift gate: every category
// the collectors can emit has exactly one catalog entry, and the catalog has no
// entry for a category that no longer exists.
func TestIssueCatalogCoversEveryDeclaredCategory(t *testing.T) {
	declared := declaredIssueCategories(t)

	catalog := make(map[issueCategory]int, len(issueCategoryCatalog))
	for _, info := range issueCategoryCatalog {
		catalog[info.Category]++
	}

	for category := range declared {
		switch catalog[category] {
		case 0:
			t.Errorf("issue category %q is declared in issues.go but missing from issueCategoryCatalog", category)
		case 1:
		default:
			t.Errorf("issue category %q appears %d times in issueCategoryCatalog, want 1", category, catalog[category])
		}
	}
	for category, count := range catalog {
		if !declared[category] {
			t.Errorf("issueCategoryCatalog documents %q, which issues.go no longer declares (%d entries)", category, count)
		}
	}
}

// TestIssueCategoryInfoIsWellFormed keeps the summaries usable in a popover and
// the severities honest.
func TestIssueCategoryInfoIsWellFormed(t *testing.T) {
	valid := map[issueSeverity]bool{severityError: true, severityWarning: true}

	for _, info := range issueCategoryCatalog {
		name := string(info.Category)
		if strings.TrimSpace(info.Label) == "" {
			t.Errorf("%s: empty label", name)
		}
		summary := strings.TrimSpace(info.Summary)
		if summary == "" {
			t.Errorf("%s: empty summary", name)
		}
		if len([]rune(summary)) > 200 {
			t.Errorf("%s: summary is %d characters, want at most 200", name, len([]rune(summary)))
		}
		if strings.ContainsAny(summary, "<>&") {
			t.Errorf("%s: summary contains markup characters: %q", name, summary)
		}
		if info.HelpAnchor != "help-issue-"+name {
			t.Errorf("%s: HelpAnchor = %q, want %q", name, info.HelpAnchor, "help-issue-"+name)
		}
		if len(info.Severities) == 0 {
			t.Errorf("%s: no severities", name)
		}
		seen := make(map[issueSeverity]bool, len(info.Severities))
		for _, sev := range info.Severities {
			if !valid[sev] {
				t.Errorf("%s: invalid severity %q", name, sev)
			}
			if seen[sev] {
				t.Errorf("%s: duplicate severity %q", name, sev)
			}
			seen[sev] = true
		}
	}

	if _, ok := issueCategoryDoc(categoryHealth); !ok {
		t.Errorf("issueCategoryDoc(%q) not found", categoryHealth)
	}
	if _, ok := issueCategoryDoc(issueCategory("not-a-category")); ok {
		t.Error("issueCategoryDoc documented an unknown category")
	}
}

// TestIssueCatalogHelpAnchors proves the Help tab declares every category and
// carries no orphan issue section.
func TestIssueCatalogHelpAnchors(t *testing.T) {
	data, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(data)

	catalog := make(map[string]bool, len(issueCategoryCatalog))
	for _, info := range issueCategoryCatalog {
		catalog[info.HelpAnchor] = true
		count := strings.Count(html, `id="`+info.HelpAnchor+`"`)
		if count != 1 {
			t.Errorf("Help anchor %q appears %d times in index.html, want exactly 1", info.HelpAnchor, count)
		}
		// Two links per category: the table of contents entry and the summary
		// table row. A section with neither is documented but unreachable.
		links := strings.Count(html, `href="#`+info.HelpAnchor+`"`)
		if links < 2 {
			t.Errorf("Help anchor %q is linked %d times in index.html, want at least 2 (contents and summary table)", info.HelpAnchor, links)
		}
	}

	for _, m := range helpAnchorPattern.FindAllStringSubmatch(html, -1) {
		if !catalog[m[1]] {
			t.Errorf("index.html has Help section %q, which no catalog entry declares", m[1])
		}
	}
}

// TestIssueCategoriesInPayload proves the catalog rides with the snapshot: with
// no findings, after a finding, and after an acknowledgement.
func TestIssueCategoriesInPayload(t *testing.T) {
	store := newIssueStore(t.TempDir())
	fixed := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }

	assertCategories := func(label string, snap issuesResponse) {
		t.Helper()
		if len(snap.Categories) != len(issueCategoryCatalog) {
			t.Fatalf("%s: %d categories, want %d", label, len(snap.Categories), len(issueCategoryCatalog))
		}
		for i, info := range issueCategoryCatalog {
			if snap.Categories[i].Category != info.Category {
				t.Errorf("%s: category %d = %q, want %q", label, i, snap.Categories[i].Category, info.Category)
			}
			if snap.Categories[i].HelpAnchor != info.HelpAnchor {
				t.Errorf("%s: category %d anchor = %q, want %q", label, i, snap.Categories[i].HelpAnchor, info.HelpAnchor)
			}
		}
	}

	assertCategories("empty store", store.snapshotIssues())

	iss := testIssue(categoryHealth, "container", "c1", "Unhealthy")
	if err := store.apply([]hostIssue{iss}, completeCoverage(), fixed); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertCategories("after apply", store.snapshotIssues())

	if err := store.acknowledge(iss.ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	assertCategories("after acknowledge", store.snapshotIssues())

	// A snapshot is the reader's own copy: mutating it (including a severity)
	// must not corrupt the declaration or the next rebuild.
	snap := store.snapshotIssues()
	snap.Categories[0].Label = "mutated"
	snap.Categories[0].Severities[0] = issueSeverity("mutated")
	if issueCategoryCatalog[0].Label == "mutated" || issueCategoryCatalog[0].Severities[0] == issueSeverity("mutated") {
		t.Fatal("mutating a snapshot reached the category declaration")
	}
	if err := store.apply([]hostIssue{iss}, completeCoverage(), fixed); err != nil {
		t.Fatalf("apply after mutation: %v", err)
	}
	next := store.snapshotIssues()
	if next.Categories[0].Label != issueCategoryCatalog[0].Label {
		t.Errorf("rebuild label = %q, want %q", next.Categories[0].Label, issueCategoryCatalog[0].Label)
	}
	if next.Categories[0].Severities[0] != issueCategoryCatalog[0].Severities[0] {
		t.Errorf("rebuild severity = %q, want %q", next.Categories[0].Severities[0], issueCategoryCatalog[0].Severities[0])
	}

	// The catalog is stable across snapshots: marshaling twice yields the same
	// bytes, so a rebuild can never reorder or drop a documented category.
	first, err := json.Marshal(store.snapshotIssues())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(store.snapshotIssues())
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("the published catalog changed between snapshots")
	}
}

// TestIssueCategoriesOnHandlers proves the catalog reaches the browser on the
// real routes: GET /api/issues and the acknowledge response both carry it.
func TestIssueCategoriesOnHandlers(t *testing.T) {
	store := newIssueStore(t.TempDir())
	iss := testIssue(categoryHealth, "container", "c1", "Unhealthy")
	if err := store.apply([]hostIssue{iss}, completeCoverage(), time.Now()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	mux := newIssueTestMux(store)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/issues", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if got := len(decodeIssues(t, rec.Body.Bytes()).Categories); got != len(issueCategoryCatalog) {
		t.Errorf("GET categories = %d, want %d", got, len(issueCategoryCatalog))
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+iss.ID+"/acknowledge", nil)
	req.Header.Set(issueRequestHeader, issueRequestValue)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("acknowledge status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	resp := decodeIssues(t, rec.Body.Bytes())
	if len(resp.Categories) != len(issueCategoryCatalog) {
		t.Errorf("acknowledge categories = %d, want %d", len(resp.Categories), len(issueCategoryCatalog))
	}
	if len(resp.Acknowledged) != 1 {
		t.Errorf("acknowledged rows = %d, want 1", len(resp.Acknowledged))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/issues/"+iss.ID+"/acknowledge", nil)
	req.Header.Set(issueRequestHeader, issueRequestValue)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unacknowledge status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	resp = decodeIssues(t, rec.Body.Bytes())
	if len(resp.Categories) != len(issueCategoryCatalog) {
		t.Errorf("unacknowledge categories = %d, want %d", len(resp.Categories), len(issueCategoryCatalog))
	}
	if resp.Count != 1 {
		t.Errorf("unacknowledged active rows = %d, want 1", resp.Count)
	}
}
