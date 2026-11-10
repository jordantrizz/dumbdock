package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Convention defaults
// ---------------------------------------------------------------------------

const (
	// defaultContainerRoot is where compose projects belong on the host.
	defaultContainerRoot = "/opt/containers"
	// defaultDataRoot is where persistent container data belongs on the host.
	defaultDataRoot = "/srv/containers"
	// defaultBackupMaxAge is the freshness target for a required backup.
	defaultBackupMaxAge = 26 * time.Hour
	// defaultStorageScanInterval bounds how often the expensive part of the
	// collection (container inspects and the filesystem scan) runs.
	defaultStorageScanInterval = 5 * time.Minute

	storageBackupDirName = "backups"

	// maxEvidenceEntries bounds a single backups directory listing.
	maxEvidenceEntries = 200
	// maxSQLiteScanEntries bounds the recursive sqlite detection scan.
	maxSQLiteScanEntries = 500
	// maxSQLiteScanDepth bounds the recursive sqlite detection scan.
	maxSQLiteScanDepth = 4
)

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// dataEngine is the database family a dataset belongs to.
type dataEngine string

const (
	enginePostgres dataEngine = "postgres"
	engineMySQL    dataEngine = "mysql"
	engineMariaDB  dataEngine = "mariadb"
	engineSQLite   dataEngine = "sqlite"
)

// storageVerdict is the backup state of one dataset. "unverified" and "exempt"
// are deliberately not findings: the first means dumbdock cannot see evidence,
// the second means the operator waived the requirement.
type storageVerdict string

const (
	verdictOK         storageVerdict = "ok"
	verdictStale      storageVerdict = "stale"
	verdictMissing    storageVerdict = "missing"
	verdictUnverified storageVerdict = "unverified"
	verdictExempt     storageVerdict = "exempt"
)

// Backup data location kinds.
const (
	dataKindBind      = "bind"
	dataKindVolume    = "volume"
	dataKindAnonymous = "anonymous"
	dataKindContainer = "container"
)

// Mount classification, used by both the Storage table and the issue collector.
const (
	mountClassOK      = "ok"
	mountClassConfig  = "config"
	mountClassCompose = "compose"
	mountClassVolume  = "volume"
	mountClassAnon    = "anonymous"
	mountClassOutside = "outside"
	mountClassIgnored = "ignored"
	mountClassOther   = "other"
)

// storageSettings is the config-file shape for the storage feature. Every value
// is optional; environment variables win over these.
type storageSettings struct {
	ContainerRoot string                    `json:"containerRoot,omitempty"`
	DataRoot      string                    `json:"dataRoot,omitempty"`
	BackupRoot    string                    `json:"backupRoot,omitempty"`
	BackupMaxAge  string                    `json:"backupMaxAge,omitempty"`
	ScanInterval  string                    `json:"scanInterval,omitempty"`
	Backups       map[string]backupOverride `json:"backups,omitempty"`
}

// backupOverride tunes one dataset, keyed "<project>/<service>".
type backupOverride struct {
	MaxAge      string   `json:"maxAge,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
	Engine      string   `json:"engine,omitempty"`
	SQLitePaths []string `json:"sqlitePaths,omitempty"`
}

// storageConfig is the resolved storage configuration.
type storageConfig struct {
	ContainerRoot string
	DataRoot      string
	BackupRoot    string
	DefaultMaxAge time.Duration
	ScanInterval  time.Duration
	Overrides     map[string]backupOverride
}

// backupPolicy is the resolved freshness rule for one dataset.
type backupPolicy struct {
	MaxAge   time.Duration
	Required bool
	Disabled bool
	Source   string
}

// backupEvidence is one observed backup artifact or report.
type backupEvidence struct {
	Provider string    `json:"provider"`
	Status   string    `json:"status"` // "ok" or "failed"
	At       time.Time `json:"at"`
	Artifact string    `json:"artifact,omitempty"`
	Detail   string    `json:"detail,omitempty"`
}

// storageDataset is one database (or sqlite workload) with its data location,
// policy, and evidence.
type storageDataset struct {
	Key              string           `json:"key"`
	Project          string           `json:"project"`
	Service          string           `json:"service"`
	ContainerName    string           `json:"containerName"`
	ContainerID      string           `json:"containerId"`
	Engine           dataEngine       `json:"engine"`
	Database         string           `json:"database,omitempty"`
	SQLitePaths      []string         `json:"sqlitePaths,omitempty"`
	DataKind         string           `json:"dataKind"`
	DataDestination  string           `json:"dataDestination,omitempty"`
	DataPath         string           `json:"dataPath,omitempty"`
	DataOK           bool             `json:"dataPathOk"`
	DataIssue        string           `json:"dataIssue,omitempty"`
	ExpectedDataPath string           `json:"expectedDataPath"`
	BackupDir        string           `json:"backupDir"`
	PolicyMaxAge     string           `json:"policyMaxAge"`
	PolicySource     string           `json:"policySource"`
	Required         bool             `json:"required"`
	Exempt           bool             `json:"exempt"`
	Verdict          storageVerdict   `json:"verdict"`
	Reason           string           `json:"reason,omitempty"`
	Evidence         []backupEvidence `json:"evidence"`
}

// storageMount is one mount considered by the convention rules.
type storageMount struct {
	Project          string `json:"project"`
	Service          string `json:"service"`
	ContainerName    string `json:"containerName"`
	ContainerID      string `json:"containerId"`
	Type             string `json:"type"`
	Source           string `json:"source"`
	Destination      string `json:"destination"`
	RW               bool   `json:"rw"`
	Class            string `json:"class"`
	ExpectedDataPath string `json:"expectedDataPath"`
}

// storageProviderCoverage reports whether one evidence provider was usable.
type storageProviderCoverage struct {
	Name          string     `json:"name"`
	Enabled       bool       `json:"enabled"`
	Configured    bool       `json:"configured"`
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// storageCoverage is the collection-quality metadata of a storage snapshot.
type storageCoverage struct {
	CollectionComplete bool                      `json:"collectionComplete"`
	ContainerRoot      string                    `json:"containerRoot"`
	DataRoot           string                    `json:"dataRoot"`
	BackupRoot         string                    `json:"backupRoot"`
	Providers          []storageProviderCoverage `json:"providers"`
	Warnings           []string                  `json:"warnings,omitempty"`
}

// storageResponse is the payload of GET /api/storage.
type storageResponse struct {
	Datasets  []storageDataset `json:"datasets"`
	Mounts    []storageMount   `json:"mounts"`
	Coverage  storageCoverage  `json:"coverage"`
	CheckedAt time.Time        `json:"checkedAt"`
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// resolveStorageConfig reads the storage settings. Environment variables win
// over the config file, which wins over the defaults.
func resolveStorageConfig(cfg *overrideConfig) storageConfig {
	c := storageConfig{
		ContainerRoot: defaultContainerRoot,
		DataRoot:      defaultDataRoot,
		DefaultMaxAge: defaultBackupMaxAge,
		ScanInterval:  defaultStorageScanInterval,
		Overrides:     map[string]backupOverride{},
	}
	if cfg != nil && cfg.Storage != nil {
		s := cfg.Storage
		if v := strings.TrimSpace(s.ContainerRoot); v != "" {
			c.ContainerRoot = v
		}
		if v := strings.TrimSpace(s.DataRoot); v != "" {
			c.DataRoot = v
		}
		if v := strings.TrimSpace(s.BackupRoot); v != "" {
			c.BackupRoot = v
		}
		if d, err := time.ParseDuration(strings.TrimSpace(s.BackupMaxAge)); err == nil && d > 0 {
			c.DefaultMaxAge = d
		}
		if d, err := time.ParseDuration(strings.TrimSpace(s.ScanInterval)); err == nil && d > 0 {
			c.ScanInterval = d
		}
		if len(s.Backups) > 0 {
			c.Overrides = s.Backups
		}
	}
	if v := strings.TrimSpace(os.Getenv("DUMBDOCK_CONTAINER_ROOT")); v != "" {
		c.ContainerRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("DUMBDOCK_DATA_ROOT")); v != "" {
		c.DataRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("DUMBDOCK_BACKUP_ROOT")); v != "" {
		c.BackupRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("DUMBDOCK_BACKUP_MAX_AGE")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.DefaultMaxAge = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("DUMBDOCK_BACKUP_SCAN_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.ScanInterval = d
		}
	}
	c.ContainerRoot = filepath.Clean(c.ContainerRoot)
	c.DataRoot = filepath.Clean(c.DataRoot)
	if c.BackupRoot == "" {
		c.BackupRoot = c.DataRoot
	}
	c.BackupRoot = filepath.Clean(c.BackupRoot)
	return c
}

// validateStorageConfig reports advisory problems with the storage settings.
// It never reads the data root: an unmounted path is a coverage state, not a
// startup failure.
func validateStorageConfig(cfg storageConfig) []configWarning {
	var warnings []configWarning
	for _, field := range []struct {
		name  string
		value string
	}{
		{"DUMBDOCK_CONTAINER_ROOT", cfg.ContainerRoot},
		{"DUMBDOCK_DATA_ROOT", cfg.DataRoot},
		{"DUMBDOCK_BACKUP_ROOT", cfg.BackupRoot},
	} {
		if _, ok := normalizeHostPath(field.value); !ok {
			warnings = append(warnings, configWarning{
				Field:   field.name,
				Message: fmt.Sprintf("invalid absolute path %q; storage checks will be skipped", field.value),
			})
		}
	}
	if cfg.DefaultMaxAge <= 0 {
		warnings = append(warnings, configWarning{Field: "DUMBDOCK_BACKUP_MAX_AGE", Message: "must be a positive duration; using 26h"})
	}
	if cfg.ScanInterval <= 0 {
		warnings = append(warnings, configWarning{Field: "DUMBDOCK_BACKUP_SCAN_INTERVAL", Message: "must be a positive duration; using 5m"})
	}
	return warnings
}

// ---------------------------------------------------------------------------
// Path safety
// ---------------------------------------------------------------------------

// normalizeHostPath accepts only an absolute, NUL-free path with no ".."
// segment, and returns it cleaned. A rejected path is never repaired: silently
// cleaning "../../etc" into a plausible path is how a scanner escapes its root.
func normalizeHostPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", false
	}
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return "", false
		}
	}
	return filepath.Clean(p), true
}

// pathWithin reports whether p is root or sits below it. Both paths must be
// absolute and free of ".."; anything else is false.
func pathWithin(root, p string) bool {
	r, ok := normalizeHostPath(root)
	if !ok {
		return false
	}
	q, ok := normalizeHostPath(p)
	if !ok {
		return false
	}
	if q == r {
		return true
	}
	return strings.HasPrefix(q, r+string(filepath.Separator))
}

// safePathSegment accepts the characters Docker compose project and service
// names use. Anything else is skipped rather than escaped: the segment becomes
// part of a filesystem path, so a refused value is the only safe default.
func safePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." || len(s) > 128 {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return ""
		}
	}
	return s
}

// isConfigDestination reports whether a mount destination looks like a config
// file rather than a data directory. Extension-only matching for files; an
// /etc/ prefix covers distribution config trees.
func isConfigDestination(dest string) bool {
	d := strings.ToLower(filepath.ToSlash(dest))
	if strings.HasPrefix(d, "/etc/") {
		return true
	}
	switch filepath.Ext(d) {
	case ".conf", ".ini", ".cfg", ".yml", ".yaml", ".json", ".env", ".toml",
		".properties", ".crt", ".key", ".pem", ".php", ".htpasswd":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Detection
// ---------------------------------------------------------------------------

// detectDataEngine maps an image repository to a database family. Only the last
// path segment is compared, so phpmyadmin is not a mysql server.
func detectDataEngine(image string, labels map[string]string) dataEngine {
	if e := engineFromLabel(labels["dumbdock.backup.engine"]); e != "" {
		return e
	}
	last := strings.ToLower(imagePath(image))
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	switch last {
	case "postgres", "postgresql", "postgis", "timescaledb", "timescaledb-ha":
		return enginePostgres
	case "mysql", "mysql-server", "percona-server", "percona":
		return engineMySQL
	case "mariadb":
		return engineMariaDB
	}
	if strings.EqualFold(strings.TrimSpace(labels["dumbdock.backup.sqlite"]), "") == false {
		return engineSQLite
	}
	return ""
}

// engineFromLabel normalizes a label/config engine name.
func engineFromLabel(v string) dataEngine {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "postgres", "postgresql":
		return enginePostgres
	case "mysql":
		return engineMySQL
	case "mariadb":
		return engineMariaDB
	case "sqlite", "sqlite3":
		return engineSQLite
	}
	return ""
}

// dataDestinations returns the in-container datadirs to look for, in priority
// order.
func dataDestinations(e dataEngine) []string {
	switch e {
	case enginePostgres:
		return []string{"/var/lib/postgresql/data", "/bitnami/postgresql"}
	case engineMySQL, engineMariaDB:
		return []string{"/var/lib/mysql", "/bitnami/mysql/data"}
	}
	return nil
}

// dataDestinationFor resolves the datadir destination for one container. An
// explicit label wins, then PGDATA, then the first known destination that is
// actually mounted, then the engine default.
func dataDestinationFor(c dockerContainer, inspect dockerInspect, e dataEngine) string {
	if v := strings.TrimSpace(c.Labels["dumbdock.data.mount"]); v != "" {
		for _, entry := range strings.Split(v, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if i := strings.LastIndex(entry, ":"); i >= 0 {
				return strings.TrimSpace(entry[i+1:])
			}
		}
	}
	if e == enginePostgres {
		if pg := strings.TrimSpace(envValue(inspect.Config.Env, "PGDATA")); pg != "" {
			return pg
		}
	}
	candidates := dataDestinations(e)
	for _, d := range candidates {
		if _, ok := findMountByDestination(c.Mounts, d); ok {
			return d
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// findMountByDestination returns the mount whose destination equals dest.
func findMountByDestination(mounts []dockerMount, dest string) (dockerMount, bool) {
	dest = strings.TrimSpace(dest)
	for _, m := range mounts {
		if m.Destination == dest {
			return m, true
		}
	}
	return dockerMount{}, false
}

// findMountContaining returns the mount whose destination contains the given
// in-container path.
func findMountContaining(mounts []dockerMount, p string) (dockerMount, bool) {
	p = strings.TrimSpace(p)
	for _, m := range mounts {
		dest := strings.TrimSpace(m.Destination)
		if dest == "" {
			continue
		}
		if p == dest || strings.HasPrefix(p, dest+"/") {
			return m, true
		}
	}
	return dockerMount{}, false
}

// sqlitePathsFromLabels splits the dumbdock.backup.sqlite label/config value.
func sqlitePathsFromLabels(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ---------------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------------

// resolvePolicy resolves the freshness rule for one dataset. Precedence:
// container label, then config override, then the configured default.
func resolvePolicy(cfg storageConfig, project, service string, labels map[string]string, e dataEngine) backupPolicy {
	policy := backupPolicy{
		MaxAge:   cfg.DefaultMaxAge,
		Required: e != "",
		Source:   "default",
	}

	if o, ok := cfg.Overrides[project+"/"+service]; ok {
		if d, err := time.ParseDuration(strings.TrimSpace(o.MaxAge)); err == nil && d > 0 {
			policy.MaxAge = d
			policy.Source = "config"
		}
		if o.Disabled {
			policy.Disabled = true
			policy.Source = "config"
		}
	}

	if v := strings.TrimSpace(labels["dumbdock.backup.max-age"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			policy.MaxAge = d
			policy.Source = "label"
		}
	}
	if disabledLabel(labels["dumbdock.backup.disabled"]) {
		policy.Disabled = true
		policy.Source = "label"
	}
	if v := strings.TrimSpace(labels["dumbdock.backup.required"]); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no", "off":
			policy.Required = false
			policy.Source = "label"
		case "true", "1", "yes", "on":
			policy.Required = true
			policy.Source = "label"
		}
	}
	return policy
}

// disabledLabel reports whether a boolean-ish label value means "off".
func disabledLabel(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// evaluateDataset sets the verdict and reason from the collected evidence.
// providersConfigured is false when no evidence source is usable at all, which
// must never be reported as a missing backup.
func evaluateDataset(ds *storageDataset, providersConfigured bool, now time.Time) {
	if ds.Exempt {
		ds.Verdict = verdictExempt
		ds.Reason = "backup requirement is waived for this dataset"
		return
	}
	if !providersConfigured {
		ds.Verdict = verdictUnverified
		ds.Reason = "backup evidence is not configured"
		return
	}

	var newest *backupEvidence
	for i := range ds.Evidence {
		ev := &ds.Evidence[i]
		if ev.Status != "ok" {
			continue
		}
		if newest == nil || ev.At.After(newest.At) {
			newest = ev
		}
	}

	maxAge, _ := time.ParseDuration(ds.PolicyMaxAge)
	if newest == nil {
		if ds.Required {
			ds.Verdict = verdictMissing
			ds.Reason = "no backup evidence found"
			return
		}
		ds.Verdict = verdictUnverified
		ds.Reason = "no backup evidence found; a backup is not required for this dataset"
		return
	}
	age := now.Sub(newest.At)
	if age < 0 {
		age = 0
	}
	if maxAge > 0 && age > maxAge {
		ds.Verdict = verdictStale
		ds.Reason = fmt.Sprintf("last backup is %s old (limit %s)", formatDurationShort(age), formatDurationShort(maxAge))
		return
	}
	ds.Verdict = verdictOK
	ds.Reason = "last backup is " + formatDurationShort(age) + " old"
}

// formatDurationShort renders a duration for a human detail string.
func formatDurationShort(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	if d < 48*time.Hour {
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

// ---------------------------------------------------------------------------
// Mount classification
// ---------------------------------------------------------------------------

// ignoredDestinations parses dumbdock.storage.ignore.
func ignoredDestinations(v string) map[string]bool {
	out := map[string]bool{}
	for _, entry := range strings.Split(v, ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			out[entry] = true
		}
	}
	return out
}

// classifyMount labels one mount for the Storage table and the issue collector.
func classifyMount(m dockerMount, workingDir, dataRoot, expectedDataPath string, ignore map[string]bool) string {
	if ignore["all"] || ignore[m.Destination] {
		return mountClassIgnored
	}
	switch m.Type {
	case "bind":
		if isConfigDestination(m.Destination) {
			return mountClassConfig
		}
		if workingDir != "" && pathWithin(workingDir, m.Source) {
			return mountClassCompose
		}
		if dataRoot != "" && pathWithin(dataRoot, m.Source) {
			return mountClassOK
		}
		return mountClassOutside
	case "volume":
		if strings.TrimSpace(m.Name) == "" {
			return mountClassAnon
		}
		return mountClassVolume
	}
	return mountClassOther
}

// buildStorageMounts classifies every mount of every compose-managed container.
func buildStorageMounts(containers []dockerContainer, cfg storageConfig) []storageMount {
	var out []storageMount
	for _, c := range containers {
		project := safePathSegment(c.composeProject())
		service := safePathSegment(c.composeService())
		if project == "" || service == "" {
			continue
		}
		ignore := ignoredDestinations(c.Labels["dumbdock.storage.ignore"])
		expected := filepath.Join(cfg.DataRoot, project, service)
		workingDir := c.composeWorkingDir()
		for _, m := range c.Mounts {
			if !m.RW {
				continue
			}
			out = append(out, storageMount{
				Project:          project,
				Service:          service,
				ContainerName:    containerName(c),
				ContainerID:      shortContainerID(c.ID),
				Type:             m.Type,
				Source:           m.Source,
				Destination:      m.Destination,
				RW:               m.RW,
				Class:            classifyMount(m, workingDir, cfg.DataRoot, expected, ignore),
				ExpectedDataPath: expected,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Destination < out[j].Destination
	})
	return out
}

// ---------------------------------------------------------------------------
// Dataset construction
// ---------------------------------------------------------------------------

// datasetDataLocation resolves the data kind/path for a dataset from its
// candidate destination or, for sqlite, from the service's data mounts.
func datasetDataLocation(c dockerContainer, destination string, cfg storageConfig, project, service string) (kind, path, issue string, ok bool) {
	if destination != "" {
		if m, found := findMountByDestination(c.Mounts, destination); found {
			switch m.Type {
			case "bind":
				if cfg.DataRoot != "" && pathWithin(cfg.DataRoot, m.Source) {
					return dataKindBind, m.Source, "", true
				}
				if wd := c.composeWorkingDir(); wd != "" && pathWithin(wd, m.Source) {
					return dataKindBind, m.Source, "data-beside-compose", false
				}
				return dataKindBind, m.Source, "data-outside-root", false
			case "volume":
				if strings.TrimSpace(m.Name) == "" {
					return dataKindAnonymous, m.Name, "data-anonymous-volume", false
				}
				return dataKindVolume, m.Name, "data-named-volume", false
			}
		}
		return dataKindContainer, destination, "data-container-layer", false
	}

	// sqlite: prefer a bind mount under the data root, then any bind, then a
	// volume. There is no fixed destination to match.
	for _, m := range c.Mounts {
		if m.Type == "bind" && cfg.DataRoot != "" && pathWithin(cfg.DataRoot, m.Source) {
			return dataKindBind, m.Source, "", true
		}
	}
	for _, m := range c.Mounts {
		if m.Type == "bind" && !isConfigDestination(m.Destination) {
			if wd := c.composeWorkingDir(); wd != "" && pathWithin(wd, m.Source) {
				return dataKindBind, m.Source, "data-beside-compose", false
			}
			return dataKindBind, m.Source, "data-outside-root", false
		}
	}
	for _, m := range c.Mounts {
		if m.Type == "volume" {
			if strings.TrimSpace(m.Name) == "" {
				return dataKindAnonymous, m.Name, "data-anonymous-volume", false
			}
			return dataKindVolume, m.Name, "data-named-volume", false
		}
	}
	return dataKindContainer, "", "data-container-layer", false
}

// buildStorageDatasets derives one dataset per database workload. scannedSQLite
// carries host paths found by the filesystem scan, keyed "<project>/<service>";
// it is nil when the scan did not run.
func buildStorageDatasets(containers []dockerContainer, inspects map[string]dockerInspect, cfg storageConfig, scannedSQLite map[string][]string) []storageDataset {
	var out []storageDataset
	for _, c := range containers {
		project := safePathSegment(c.composeProject())
		service := safePathSegment(c.composeService())
		if project == "" || service == "" {
			continue
		}
		key := project + "/" + service
		inspect := inspects[shortContainerID(c.ID)]

		engine := detectDataEngine(c.Image, c.Labels)
		if e := engineFromLabel(cfg.Overrides[key].Engine); e != "" && engine == "" {
			engine = e
		}
		sqlitePaths := sqlitePathsFromLabels(c.Labels["dumbdock.backup.sqlite"])
		if len(sqlitePaths) == 0 {
			sqlitePaths = append(sqlitePaths, cfg.Overrides[key].SQLitePaths...)
		}
		if scanned := scannedSQLite[key]; len(scanned) > 0 {
			engine = engineSQLite
			sqlitePaths = append(sqlitePaths, scanned...)
		}
		if engine == "" {
			continue
		}
		sqlitePaths = dedupeStrings(sqlitePaths)

		destination := ""
		if engine != engineSQLite {
			destination = dataDestinationFor(c, inspect, engine)
		}
		kind, dataPath, dataIssue, dataOK := datasetDataLocation(c, destination, cfg, project, service)

		policy := resolvePolicy(cfg, project, service, c.Labels, engine)
		expected := filepath.Join(cfg.DataRoot, project, service)
		ds := storageDataset{
			Key:              key,
			Project:          project,
			Service:          service,
			ContainerName:    containerName(c),
			ContainerID:      shortContainerID(c.ID),
			Engine:           engine,
			Database:         databaseName(engine, c.Labels, inspect),
			SQLitePaths:      sqlitePaths,
			DataKind:         kind,
			DataDestination:  destination,
			DataPath:         dataPath,
			DataOK:           dataOK,
			DataIssue:        dataIssue,
			ExpectedDataPath: expected,
			BackupDir:        filepath.Join(cfg.BackupRoot, project, service, storageBackupDirName),
			PolicyMaxAge:     policy.MaxAge.String(),
			PolicySource:     policy.Source,
			Required:         policy.Required,
			Exempt:           policy.Disabled,
			Evidence:         []backupEvidence{},
		}
		out = append(out, ds)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Engine < out[j].Engine
	})
	return out
}

// databaseName returns a display-only database identifier. Only the database
// name is read, never a password or any other environment value.
func databaseName(engine dataEngine, labels map[string]string, inspect dockerInspect) string {
	if v := strings.TrimSpace(labels["dumbdock.backup.database"]); v != "" {
		return truncateForLog(v, 64)
	}
	switch engine {
	case enginePostgres:
		return truncateForLog(envValue(inspect.Config.Env, "POSTGRES_DB"), 64)
	case engineMySQL, engineMariaDB:
		return truncateForLog(envValue(inspect.Config.Env, "MYSQL_DATABASE"), 64)
	}
	return ""
}

// dedupeStrings removes duplicates and empty values, preserving order.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Findings
// ---------------------------------------------------------------------------

// describeDataIssue turns a dataset data deviation into one issue row.
func storageDataIssue(ds storageDataset) (hostIssue, bool) {
	expected := ds.ExpectedDataPath
	detail := ""
	title := ""
	severity := severityWarning
	switch ds.DataIssue {
	case "data-container-layer":
		title = "Database data is stored in the container filesystem"
		detail = fmt.Sprintf("%s data (%s) is not on a mounted path, so it is lost when the container is recreated. Move it to %s.", ds.Engine, truncateForLog(ds.DataDestination, 120), expected)
		severity = severityError
	case "data-named-volume":
		title = "Database data is stored in a Docker volume"
		detail = fmt.Sprintf("%s data uses volume %q instead of %s, so a host backup of the data root does not cover it.", ds.Engine, truncateForLog(ds.DataPath, 80), expected)
	case "data-anonymous-volume":
		title = "Database data is stored in an anonymous volume"
		detail = fmt.Sprintf("%s data uses an anonymous volume instead of %s, so a host backup of the data root does not cover it.", ds.Engine, expected)
	case "data-beside-compose":
		title = "Database data is stored beside the compose file"
		detail = fmt.Sprintf("%s data is at %s, inside the compose project directory. Move it to %s.", ds.Engine, truncateForLog(ds.DataPath, 160), expected)
	case "data-outside-root":
		title = "Database data is stored outside the data root"
		detail = fmt.Sprintf("%s data is at %s, outside %s. Move it to %s.", ds.Engine, truncateForLog(ds.DataPath, 160), defaultDataRoot, expected)
	default:
		return hostIssue{}, false
	}
	return hostIssue{
		ID:            newIssueID(categoryStorage, "dataset", ds.Key),
		Category:      categoryStorage,
		Severity:      severity,
		Title:         title,
		Detail:        detail,
		Source:        "storage",
		ContainerID:   ds.ContainerID,
		ContainerName: ds.ContainerName,
	}, true
}

// collectStorageIssues derives the storage findings from the current mounts and
// datasets. Compose-location findings are per project; mounting and dataset
// findings are per object, with the dataset row suppressing a duplicate volume
// mount row for the same destination.
func collectStorageIssues(containers []dockerContainer, mounts []storageMount, datasets []storageDataset, cfg storageConfig) []hostIssue {
	var issues []hostIssue

	// Compose file location, one row per project.
	type projectCheck struct {
		name        string
		containerID string
		bad         []string
	}
	checks := map[string]*projectCheck{}
	var order []string
	for _, c := range containers {
		project := safePathSegment(c.composeProject())
		if project == "" {
			continue
		}
		pc, ok := checks[project]
		if !ok {
			pc = &projectCheck{name: project, containerID: shortContainerID(c.ID)}
			checks[project] = pc
			order = append(order, project)
		}
		expected := filepath.Join(cfg.ContainerRoot, project)
		for _, f := range c.composeConfigFiles() {
			if pathWithin(expected, f) {
				continue
			}
			normalized, ok := normalizeHostPath(f)
			if !ok {
				normalized = truncateForLog(f, 160)
			}
			pc.bad = append(pc.bad, normalized)
		}
	}
	sort.Strings(order)
	for _, project := range order {
		pc := checks[project]
		if len(pc.bad) == 0 {
			continue
		}
		bad := dedupeStrings(pc.bad)
		sort.Strings(bad)
		issues = append(issues, hostIssue{
			ID:          newIssueID(categoryStorage, "compose", project),
			Category:    categoryStorage,
			Severity:    severityWarning,
			Title:       "Compose file is stored outside the container root",
			Detail:      fmt.Sprintf("%s: move the compose file to %s. Found: %s.", project, filepath.Join(cfg.ContainerRoot, project), truncateForLog(strings.Join(bad, ", "), 200)),
			Source:      "storage",
			ContainerID: pc.containerID,
		})
	}

	// A volume holding a database datadir is reported once, by the dataset row.
	covered := map[string]bool{}
	for _, ds := range datasets {
		if ds.DataKind == dataKindVolume || ds.DataKind == dataKindAnonymous {
			covered[ds.Project+"/"+ds.Service+"/"+ds.DataDestination] = true
		}
	}

	for _, m := range mounts {
		key := m.Project + "/" + m.Service
		switch m.Class {
		case mountClassCompose:
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryStorage, "mount", key+"/"+m.Destination),
				Category:      categoryStorage,
				Severity:      severityWarning,
				Title:         "Container data is stored beside the compose file",
				Detail:        fmt.Sprintf("%s mounts %s at %s, inside the compose project directory. Move the data to %s.", key, truncateForLog(m.Source, 160), truncateForLog(m.Destination, 80), m.ExpectedDataPath),
				Source:        "storage",
				ContainerID:   m.ContainerID,
				ContainerName: m.ContainerName,
			})
		case mountClassVolume, mountClassAnon:
			if covered[key+"/"+m.Destination] {
				continue
			}
			kind := "named"
			if m.Class == mountClassAnon {
				kind = "anonymous"
			}
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryStorage, "mount", key+"/"+m.Destination),
				Category:      categoryStorage,
				Severity:      severityWarning,
				Title:         "Persistent data is stored in a Docker volume",
				Detail:        fmt.Sprintf("%s mounts a %s volume at %s. Use a bind mount from %s so host backups cover it.", key, kind, truncateForLog(m.Destination, 80), m.ExpectedDataPath),
				Source:        "storage",
				ContainerID:   m.ContainerID,
				ContainerName: m.ContainerName,
			})
		}
	}

	for _, ds := range datasets {
		if ds.DataOK || ds.DataIssue == "" {
			continue
		}
		if issue, ok := storageDataIssue(ds); ok {
			issues = append(issues, issue)
		}
	}
	return issues
}

// collectBackupIssues derives the backup findings from the evaluated datasets.
// Unverified and exempt datasets never produce a row.
func collectBackupIssues(datasets []storageDataset) []hostIssue {
	var issues []hostIssue
	for _, ds := range datasets {
		switch ds.Verdict {
		case verdictStale:
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryBackup, "backup", ds.Key),
				Category:      categoryBackup,
				Severity:      severityWarning,
				Title:         "Database backup is stale",
				Detail:        fmt.Sprintf("%s %s: %s. Write a fresh dump to %s.", ds.Engine, ds.Key, ds.Reason, ds.BackupDir),
				Source:        "backup",
				ContainerID:   ds.ContainerID,
				ContainerName: ds.ContainerName,
			})
		case verdictMissing:
			issues = append(issues, hostIssue{
				ID:            newIssueID(categoryBackup, "backup", ds.Key),
				Category:      categoryBackup,
				Severity:      severityError,
				Title:         "Database has no backup evidence",
				Detail:        fmt.Sprintf("%s %s requires a backup every %s and none was found. Write a dump to %s, or waive it with dumbdock.backup.disabled=true.", ds.Engine, ds.Key, ds.PolicyMaxAge, ds.BackupDir),
				Source:        "backup",
				ContainerID:   ds.ContainerID,
				ContainerName: ds.ContainerName,
			})
		}
	}
	return issues
}

// ---------------------------------------------------------------------------
// Snapshot store
// ---------------------------------------------------------------------------

// storageStore publishes an immutable storage snapshot and the issue rows that
// derive from it. Readers receive values whose slices are never mutated.
type storageStore struct {
	mu        sync.RWMutex
	published storageResponse
	issues    []hostIssue
	warnings  []string
	complete  bool
	evaluated bool
}

func newStorageStore() *storageStore {
	return &storageStore{
		published: storageResponse{
			Datasets: []storageDataset{},
			Mounts:   []storageMount{},
		},
	}
}

// apply replaces the published snapshot and its derived findings.
func (s *storageStore) apply(resp storageResponse, issues []hostIssue, warnings []string, complete, evaluated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resp.Datasets == nil {
		resp.Datasets = []storageDataset{}
	}
	if resp.Mounts == nil {
		resp.Mounts = []storageMount{}
	}
	s.published = resp
	s.issues = issues
	s.warnings = append([]string(nil), warnings...)
	s.complete = complete
	s.evaluated = evaluated
}

// snapshot returns the current published storage response.
func (s *storageStore) snapshot() storageResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.published
}

// findings returns the storage-derived issues plus coverage information.
func (s *storageStore) findings() ([]hostIssue, []string, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]hostIssue(nil), s.issues...), append([]string(nil), s.warnings...), s.complete, s.evaluated
}

// hasDataset reports whether key is a currently detected dataset.
func (s *storageStore) hasDataset(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ds := range s.published.Datasets {
		if ds.Key == key {
			return true
		}
	}
	return false
}

// datasetEngine returns the detected engine for a dataset key.
func (s *storageStore) datasetEngine(key string) (dataEngine, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ds := range s.published.Datasets {
		if ds.Key == key {
			return ds.Engine, true
		}
	}
	return "", false
}
