package main

// issueCategoryInfo documents one issue category for the dashboard. It is static
// policy data: it never describes a specific finding and never carries Docker
// labels, image contents, or credentials, so it is safe to publish alongside the
// issues snapshot.
type issueCategoryInfo struct {
	Category   issueCategory   `json:"category"`
	Label      string          `json:"label"`
	Summary    string          `json:"summary"`
	Severities []issueSeverity `json:"severities"`
	HelpAnchor string          `json:"helpAnchor"`
}

// issueCategoryCatalog is the single declaration of every issue category, in the
// order the Issues page groups them. A new category has to add one entry here
// and one `#help-issue-<category>` section to index.html; issue_catalog_test.go
// fails until both exist.
//
// The slice is init-time state: it must never be appended to, sorted, or
// otherwise mutated. Every snapshot publishes a copy (see
// copyIssueCategoryCatalog) so a reader that alters a value in its own
// snapshot cannot corrupt the declaration.
var issueCategoryCatalog = []issueCategoryInfo{
	{
		Category:   categoryHealth,
		Label:      "Container health",
		Summary:    "Docker's healthcheck for this container reports unhealthy, so it is running but failing its own health test.",
		Severities: []issueSeverity{severityError},
		HelpAnchor: "help-issue-container-health",
	},
	{
		Category:   categoryTraefikAPI,
		Label:      "Traefik API",
		Summary:    "dumbdock could not read Traefik's API: every endpoint failed, one endpoint returned an error, or the API was disabled after repeated failures.",
		Severities: []issueSeverity{severityError, severityWarning},
		HelpAnchor: "help-issue-traefik-api",
	},
	{
		Category:   categoryTraefikBackend,
		Label:      "Traefik backends",
		Summary:    "A load-balancer server behind one of Traefik's services reports DOWN, so traffic through that service is failing.",
		Severities: []issueSeverity{severityError},
		HelpAnchor: "help-issue-traefik-backend",
	},
	{
		Category:   categoryTraefikEntity,
		Label:      "Traefik entities",
		Summary:    "A Traefik router, service, or middleware reports a disabled, warning, or error status.",
		Severities: []issueSeverity{severityError, severityWarning},
		HelpAnchor: "help-issue-traefik-entity",
	},
	{
		Category:   categoryBackup,
		Label:      "Database backups",
		Summary:    "A detected database has no backup evidence, or its newest verified dump is older than its policy.",
		Severities: []issueSeverity{severityError, severityWarning},
		HelpAnchor: "help-issue-container-backup",
	},
	{
		Category:   categoryStorage,
		Label:      "Storage paths",
		Summary:    "A compose file, data directory, or database path sits outside the storage layout dumbdock expects.",
		Severities: []issueSeverity{severityError, severityWarning},
		HelpAnchor: "help-issue-container-storage",
	},
	{
		Category:   categoryExposure,
		Label:      "Exposed ports",
		Summary:    "A published port is bound to every interface or a public IP, so anyone who can reach the host can connect.",
		Severities: []issueSeverity{severityWarning},
		HelpAnchor: "help-issue-container-exposure",
	},
	{
		Category:   categoryUpdate,
		Label:      "Image updates",
		Summary:    "The registry reports a newer image digest for the running tag; dumbdock never pulls or restarts anything.",
		Severities: []issueSeverity{severityWarning},
		HelpAnchor: "help-issue-container-update",
	},
	{
		Category:   categoryDockerVersion,
		Label:      "Docker Engine",
		Summary:    "The Docker daemon is older than the latest stable Moby release. This is advisory: distribution packages often lag upstream.",
		Severities: []issueSeverity{severityWarning},
		HelpAnchor: "help-issue-docker-version",
	},
	{
		Category:   categoryTraefikVersion,
		Label:      "Traefik version",
		Summary:    "The detected Traefik major version is not 3, which dumbdock requires for its Traefik tab.",
		Severities: []issueSeverity{severityWarning},
		HelpAnchor: "help-issue-traefik-version",
	},
	{
		Category:   categoryIntegrationAPI,
		Label:      "Integration API",
		Summary:    "dumbdock could not read an integration's API: it is unreachable, rejected its credentials, or was disabled after repeated failures.",
		Severities: []issueSeverity{severityError},
		HelpAnchor: "help-issue-integration-api",
	},
	{
		Category:   categoryIntegrationStatus,
		Label:      "Integration status",
		Summary:    "The integration reports a problem of its own, such as failed Watchtower updates or a PruneMate that has not pruned recently.",
		Severities: []issueSeverity{severityWarning},
		HelpAnchor: "help-issue-integration-status",
	},
}

// issueCategoryDoc returns the catalog entry for a category. A category the
// catalog does not know reports false so callers can fall back instead of
// inventing documentation.
func issueCategoryDoc(category issueCategory) (issueCategoryInfo, bool) {
	for _, info := range issueCategoryCatalog {
		if info.Category == category {
			return info, true
		}
	}
	return issueCategoryInfo{}, false
}

// copyIssueCategoryCatalog returns a snapshot-safe copy of the catalog. The
// severity slices are copied too, so mutating a published value cannot reach
// the declaration.
func copyIssueCategoryCatalog() []issueCategoryInfo {
	out := make([]issueCategoryInfo, len(issueCategoryCatalog))
	for i, info := range issueCategoryCatalog {
		info.Severities = append([]issueSeverity(nil), info.Severities...)
		out[i] = info
	}
	return out
}
