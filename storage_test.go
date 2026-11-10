package main

import (
	"path/filepath"
	"testing"
	"time"
)

func clearStorageEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"DUMBDOCK_CONTAINER_ROOT",
		"DUMBDOCK_DATA_ROOT",
		"DUMBDOCK_BACKUP_ROOT",
		"DUMBDOCK_BACKUP_MAX_AGE",
		"DUMBDOCK_BACKUP_SCAN_INTERVAL",
	} {
		t.Setenv(name, "")
	}
}

func TestDetectDataEngine(t *testing.T) {
	tests := []struct {
		image  string
		labels map[string]string
		want   dataEngine
	}{
		{"postgres:16-alpine", nil, enginePostgres},
		{"postgis/postgis:16-3.4", nil, enginePostgres},
		{"bitnami/postgresql:16", nil, enginePostgres},
		{"timescale/timescaledb:latest-pg16", nil, enginePostgres},
		{"mysql:8", nil, engineMySQL},
		{"mysql/mysql-server:8.0", nil, engineMySQL},
		{"percona/percona-server:8.0", nil, engineMySQL},
		{"mariadb:11", nil, engineMariaDB},
		{"phpmyadmin:latest", nil, ""},
		{"my-postgres-backup:1", nil, ""},
		{"alpine:3.20", nil, ""},
		{"alpine:3.20", map[string]string{"dumbdock.backup.engine": "sqlite"}, engineSQLite},
		{"alpine:3.20", map[string]string{"dumbdock.backup.sqlite": "/data/app.db"}, engineSQLite},
		{"postgres:16", map[string]string{"dumbdock.backup.engine": "mysql"}, engineMySQL},
	}
	for _, tc := range tests {
		if got := detectDataEngine(tc.image, tc.labels); got != tc.want {
			t.Errorf("detectDataEngine(%q, %v) = %q, want %q", tc.image, tc.labels, got, tc.want)
		}
	}
}

func TestNormalizeHostPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/srv/containers/mwp", "/srv/containers/mwp", true},
		{"/srv/containers/", "/srv/containers", true},
		{"/srv/../etc", "", false},
		{"/srv/containers/../etc", "", false},
		{"relative/path", "", false},
		{"", "", false},
		{"/srv/cont\x00ainers", "", false},
	}
	for _, tc := range tests {
		got, ok := normalizeHostPath(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeHostPath(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPathWithin(t *testing.T) {
	tests := []struct {
		root, path string
		want       bool
	}{
		{"/srv/containers", "/srv/containers", true},
		{"/srv/containers", "/srv/containers/mwp/postgres", true},
		{"/srv/containers", "/srv/containers2/mwp", false},
		{"/srv/containers", "/srv", false},
		{"/srv/containers", "/srv/containers/../etc/passwd", false},
		{"", "/srv/containers", false},
	}
	for _, tc := range tests {
		if got := pathWithin(tc.root, tc.path); got != tc.want {
			t.Errorf("pathWithin(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestSafePathSegment(t *testing.T) {
	tests := map[string]string{
		"mwp":           "mwp",
		"my_app-1.2":    "my_app-1.2",
		"../etc":        "",
		"a/b":           "",
		"":              "",
		"name with gap": "",
		"naïve":         "",
		"/absolute":     "",
	}
	for in, want := range tests {
		if got := safePathSegment(in); got != want {
			t.Errorf("safePathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveStorageConfig(t *testing.T) {
	clearStorageEnv(t)

	cfg := resolveStorageConfig(nil)
	if cfg.ContainerRoot != defaultContainerRoot {
		t.Errorf("ContainerRoot = %q, want %q", cfg.ContainerRoot, defaultContainerRoot)
	}
	if cfg.DataRoot != defaultDataRoot {
		t.Errorf("DataRoot = %q, want %q", cfg.DataRoot, defaultDataRoot)
	}
	if cfg.BackupRoot != defaultDataRoot {
		t.Errorf("BackupRoot = %q, want %q", cfg.BackupRoot, defaultDataRoot)
	}
	if cfg.DefaultMaxAge != defaultBackupMaxAge {
		t.Errorf("DefaultMaxAge = %s, want %s", cfg.DefaultMaxAge, defaultBackupMaxAge)
	}
	if cfg.ScanInterval != defaultStorageScanInterval {
		t.Errorf("ScanInterval = %s, want %s", cfg.ScanInterval, defaultStorageScanInterval)
	}

	t.Setenv("DUMBDOCK_DATA_ROOT", "/mnt/data")
	t.Setenv("DUMBDOCK_BACKUP_ROOT", "/mnt/backups")
	t.Setenv("DUMBDOCK_BACKUP_MAX_AGE", "48h")

	cfg = resolveStorageConfig(&overrideConfig{Storage: &storageSettings{
		ContainerRoot: "/srv/compose",
		DataRoot:      "/srv/data",
		ScanInterval:  "1m",
		Backups:       map[string]backupOverride{"mwp/postgres": {MaxAge: "12h"}},
	}})
	if cfg.ContainerRoot != "/srv/compose" {
		t.Errorf("ContainerRoot = %q, want config value", cfg.ContainerRoot)
	}
	if cfg.DataRoot != "/mnt/data" {
		t.Errorf("DataRoot = %q, want env override", cfg.DataRoot)
	}
	if cfg.BackupRoot != "/mnt/backups" {
		t.Errorf("BackupRoot = %q, want env override", cfg.BackupRoot)
	}
	if cfg.DefaultMaxAge != 48*time.Hour {
		t.Errorf("DefaultMaxAge = %s, want 48h", cfg.DefaultMaxAge)
	}
	if cfg.ScanInterval != time.Minute {
		t.Errorf("ScanInterval = %s, want 1m", cfg.ScanInterval)
	}
	if o, ok := cfg.Overrides["mwp/postgres"]; !ok || o.MaxAge != "12h" {
		t.Errorf("Overrides not carried through: %+v", cfg.Overrides)
	}
}

func TestResolvePolicy(t *testing.T) {
	clearStorageEnv(t)
	cfg := resolveStorageConfig(&overrideConfig{Storage: &storageSettings{
		Backups: map[string]backupOverride{
			"mwp/postgres": {MaxAge: "12h"},
			"app/db":       {Disabled: true},
		},
	}})

	p := resolvePolicy(cfg, "mwp", "postgres", nil, enginePostgres)
	if p.MaxAge != 12*time.Hour || p.Source != "config" || !p.Required {
		t.Errorf("config policy = %+v", p)
	}

	p = resolvePolicy(cfg, "app", "db", nil, engineMySQL)
	if !p.Disabled {
		t.Errorf("config disabled not applied: %+v", p)
	}

	labels := map[string]string{
		"dumbdock.backup.max-age":  "3h",
		"dumbdock.backup.required": "false",
	}
	p = resolvePolicy(cfg, "mwp", "postgres", labels, enginePostgres)
	if p.MaxAge != 3*time.Hour || p.Source != "label" {
		t.Errorf("label max age not applied: %+v", p)
	}
	if p.Required {
		t.Errorf("dumbdock.backup.required=false did not clear Required: %+v", p)
	}

	p = resolvePolicy(cfg, "other", "db", map[string]string{"dumbdock.backup.disabled": "true"}, engineMariaDB)
	if !p.Disabled {
		t.Errorf("disabled label not applied: %+v", p)
	}
}

func TestDataDestinationFor(t *testing.T) {
	c := dockerContainer{
		Image: "postgres:16",
		Mounts: []dockerMount{{
			Type: "bind", Source: "/srv/containers/mwp/postgres", Destination: "/var/lib/postgresql/data", RW: true,
		}},
	}
	if got := dataDestinationFor(c, dockerInspect{}, enginePostgres); got != "/var/lib/postgresql/data" {
		t.Errorf("default destination = %q", got)
	}

	c.Labels = map[string]string{"dumbdock.data.mount": "/host/pg:/custom/pgdata"}
	if got := dataDestinationFor(c, dockerInspect{}, enginePostgres); got != "/custom/pgdata" {
		t.Errorf("label destination = %q", got)
	}

	c.Labels = nil
	inspect := dockerInspect{}
	inspect.Config.Env = []string{"PGDATA=/var/lib/postgresql/pgdata"}
	if got := dataDestinationFor(c, inspect, enginePostgres); got != "/var/lib/postgresql/pgdata" {
		t.Errorf("PGDATA destination = %q", got)
	}
}

func TestBuildStorageMounts(t *testing.T) {
	clearStorageEnv(t)
	cfg := resolveStorageConfig(nil)
	containers := []dockerContainer{{
		ID:    "0123456789abcdef",
		Image: "app:latest",
		Labels: map[string]string{
			"com.docker.compose.project":             "app",
			"com.docker.compose.service":             "web",
			"com.docker.compose.project.working_dir": "/home/user/git/app",
		},
		Mounts: []dockerMount{
			{Type: "bind", Source: "/home/user/git/app", Destination: "/var/www/html", RW: true},
			{Type: "bind", Source: "/home/user/git/app/php.ini", Destination: "/usr/local/etc/php/php.ini", RW: true},
			{Type: "bind", Source: "/srv/containers/app/web", Destination: "/data", RW: true},
			{Type: "bind", Source: "/mnt/scratch", Destination: "/scratch", RW: true},
			{Type: "volume", Name: "app_uploads", Destination: "/uploads", RW: true},
			{Type: "volume", Destination: "/anon", RW: true},
			{Type: "bind", Source: "/home/user/git/app", Destination: "/devnull", RW: false},
		},
	}}

	mounts := buildStorageMounts(containers, cfg)
	byDest := map[string]storageMount{}
	for _, m := range mounts {
		byDest[m.Destination] = m
	}
	checks := map[string]string{
		"/var/www/html":              mountClassCompose,
		"/usr/local/etc/php/php.ini": mountClassConfig,
		"/data":                      mountClassOK,
		"/scratch":                   mountClassOutside,
		"/uploads":                   mountClassVolume,
		"/anon":                      mountClassAnon,
	}
	for dest, want := range checks {
		m, ok := byDest[dest]
		if !ok {
			t.Errorf("mount %q missing", dest)
			continue
		}
		if m.Class != want {
			t.Errorf("mount %q class = %q, want %q", dest, m.Class, want)
		}
	}
	if _, ok := byDest["/devnull"]; ok {
		t.Errorf("read-only bind should not be classified")
	}

	// Standalone containers are out of scope.
	if got := buildStorageMounts([]dockerContainer{{ID: "abc", Mounts: containers[0].Mounts}}, cfg); len(got) != 0 {
		t.Errorf("standalone container produced %d mounts", len(got))
	}
}

func TestBuildStorageDatasets(t *testing.T) {
	clearStorageEnv(t)
	cfg := resolveStorageConfig(nil)

	base := func(service string) dockerContainer {
		return dockerContainer{
			ID:    "0123456789abcdef",
			Image: "postgres:16",
			Labels: map[string]string{
				"com.docker.compose.project": "mwp",
				"com.docker.compose.service": service,
			},
		}
	}

	bind := base("postgres")
	bind.Mounts = []dockerMount{{Type: "bind", Source: "/srv/containers/mwp/postgres", Destination: "/var/lib/postgresql/data", RW: true}}

	volume := base("postgres")
	volume.Mounts = []dockerMount{{Type: "volume", Name: "mwp_pgdata", Destination: "/var/lib/postgresql/data", RW: true}}

	layer := base("postgres")

	datasets := buildStorageDatasets([]dockerContainer{bind}, nil, cfg, nil)
	if len(datasets) != 1 {
		t.Fatalf("bind: got %d datasets", len(datasets))
	}
	ds := datasets[0]
	if ds.Engine != enginePostgres || ds.DataKind != dataKindBind || !ds.DataOK || ds.DataPath != "/srv/containers/mwp/postgres" {
		t.Errorf("bind dataset = %+v", ds)
	}
	if ds.BackupDir != filepath.Join("/srv/containers", "mwp", "postgres", "backups") {
		t.Errorf("BackupDir = %q", ds.BackupDir)
	}
	if ds.PolicyMaxAge != "26h0m0s" || ds.PolicySource != "default" || !ds.Required {
		t.Errorf("default policy = %+v", ds)
	}

	datasets = buildStorageDatasets([]dockerContainer{volume}, nil, cfg, nil)
	if len(datasets) != 1 || datasets[0].DataKind != dataKindVolume || datasets[0].DataOK || datasets[0].DataIssue != "data-named-volume" {
		t.Errorf("volume dataset = %+v", datasets)
	}

	datasets = buildStorageDatasets([]dockerContainer{layer}, nil, cfg, nil)
	if len(datasets) != 1 || datasets[0].DataKind != dataKindContainer || datasets[0].DataIssue != "data-container-layer" {
		t.Errorf("container-layer dataset = %+v", datasets)
	}

	// A scanned sqlite file makes a non-database image a sqlite dataset.
	app := dockerContainer{
		ID:    "fedcba9876543210",
		Image: "alpine:3.20",
		Labels: map[string]string{
			"com.docker.compose.project": "notes",
			"com.docker.compose.service": "app",
		},
		Mounts: []dockerMount{{Type: "bind", Source: "/srv/containers/notes/app", Destination: "/data", RW: true}},
	}
	datasets = buildStorageDatasets([]dockerContainer{app}, nil, cfg, map[string][]string{"notes/app": {"/srv/containers/notes/app/notes.db"}})
	if len(datasets) != 1 || datasets[0].Engine != engineSQLite {
		t.Fatalf("sqlite dataset = %+v", datasets)
	}
	if len(datasets[0].SQLitePaths) != 1 || datasets[0].SQLitePaths[0] != "/srv/containers/notes/app/notes.db" {
		t.Errorf("sqlite paths = %v", datasets[0].SQLitePaths)
	}

	// Compose-dir data is classified as a deviation.
	dev := dockerContainer{
		ID:    "aaaabbbbccccdddd",
		Image: "postgres:16",
		Labels: map[string]string{
			"com.docker.compose.project":             "dev",
			"com.docker.compose.service":             "postgres",
			"com.docker.compose.project.working_dir": "/home/user/git/dev",
		},
		Mounts: []dockerMount{{Type: "bind", Source: "/home/user/git/dev/pgdata", Destination: "/var/lib/postgresql/data", RW: true}},
	}
	datasets = buildStorageDatasets([]dockerContainer{dev}, nil, cfg, nil)
	if len(datasets) != 1 || datasets[0].DataIssue != "data-beside-compose" || datasets[0].DataOK {
		t.Errorf("compose-dir dataset = %+v", datasets)
	}
}

func TestValidateStorageConfig(t *testing.T) {
	clearStorageEnv(t)
	cfg := resolveStorageConfig(nil)
	if got := validateStorageConfig(cfg); len(got) != 0 {
		t.Errorf("default config warnings = %v", got)
	}
	cfg.DataRoot = "relative"
	if got := validateStorageConfig(cfg); len(got) != 1 || got[0].Field != "DUMBDOCK_DATA_ROOT" {
		t.Errorf("invalid root warnings = %v", got)
	}
}
