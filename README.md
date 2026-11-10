# dumbdock

A simple, no-frills web dashboard for your Docker containers. dumbdock shows a clean overview of your running containers, grouped and organized with custom labels — and alerts you when new unlabeled containers appear.

> **🤖 AI-Assisted Development:** This project was developed with AI assistance. All AI-generated code has been reviewed by a human. See [AI-Assisted Development](#ai-assisted-development) below for details.

![screenshot](https://img.shields.io/badge/status-stable-brightgreen)
![go](https://img.shields.io/badge/go-1.22-blue)
![docker](https://img.shields.io/badge/docker-ready-2496ED)

## Features

- **Automatic discovery** — polls the Docker socket and lists all running containers.
- **Label-driven organization** — use `dumbdock.*` labels on your containers to set names, groups, icons, links, descriptions, and sort order.
- **Smart icon resolution** — automatically detects icons for unlabeled containers from [selfhst/icons](https://github.com/selfhst/icons), [dashboard-icons](https://github.com/homarr-labs/dashboard-icons), or any configured icon set based on the image name. Matched containers get their own group (default: "Auto Detected") so they don't clutter the "Unlabeled" section. Falls back to a generic placeholder when no specific match is found.
- **Dumbdock branding** — a built-in SVG icon (`/dumbdock.svg`) serves as favicon, nav bar logo, and auto-detection icon for dumbdock containers themselves.
- **Grouped card layout** — labeled containers appear in organized groups with responsive cards showing icon, name, description, link, and status.
- **Dependency grouping** — a third view groups containers under the container(s) they depend on, read automatically from the Docker Compose `com.docker.compose.depends_on` label (no `dumbdock.*` label needed). The dashboard toggle cycles **By Group → By Compose → By Dependency** and is only shown on the Dashboard tab.
- **Unlabeled container section** — containers without `dumbdock.*` labels appear in an expandable list that shows current labels, container info, and copy-paste-ready examples for labeling via docker-compose or `dumbdock.json`.
- **Config file overrides** — optionally define names, groups, icons, etc. in a JSON config file instead of (or in addition to) Docker labels.
- **Alert notifications** — get notified via [ntfy.sh](https://ntfy.sh) or [Gotify](https://gotify.net) when new unlabeled containers are detected, with configurable cooldown.
- **Password protection** — authentication via the `DUMBDOCK_PASSWORD` environment variable, in two modes selected by `DUMBDOCK_AUTH_MODE`: `web-auth` (web login form with username + password, "Remember me", and session cookies, the default) or `http-auth` (HTTP Basic Authentication). Set `DUMBDOCK_AUTH_MODE=none` to disable auth. When an auth mode is active but no password is set, a random password is generated and logged once at startup.
- **Network Warnings** — identifies containers with ports exposed on non-localhost IPs (▲) and surfaces [Traefik](https://traefik.io) proxy configuration (🔗) with clickable URLs extracted from `traefik.http.routers.*.rule` labels.
- **Image update checks** — a background checker compares each running container's local image digest against the digest currently served by Docker Hub or GHCR and flags containers whose tag has moved with an `⬆ update available` badge (unsupported/local images show a neutral `? update unknown`). See [Image Update Checks](#image-update-checks).
- **Dashboard scoreboard** — a clickable summary above the legend counts containers with image updates, private port bindings, localhost port bindings, and Traefik enabled; clicking a tile filters the dashboard to the matching containers. See [Dashboard Scoreboard](#dashboard-scoreboard).
- **Traefik Dashboard** — automatically detects running Traefik containers, inspects them to resolve the API URL (label, network IP, or published port), and renders a full Traefik status dashboard as a dedicated tab — showing version, overview stats, entrypoints, HTTP/TCP routers, services, middlewares, and TLS certificates.
- **Issues tab** — a dedicated tab beside Traefik that lists every actionable problem dumbdock can prove from the Docker and Traefik APIs: publicly bound ports, available image updates, containers Docker reports as `unhealthy`, Traefik API/entity/backend failures, unsupported Traefik versions, and an outdated Docker Engine. The tab carries a live count badge, supports acknowledging a finding until it resolves, and never repairs or mutates the host. See [Issues Tab](#issues-tab).
- **Storage tab** — a dedicated tab beside Issues that enforces the host conventions: compose files belong in `/opt/containers/<project>/`, persistent data in `/srv/containers/<project>/<service>/`, and data must not sit beside the compose file, in a Docker volume, or in the container layer. It detects PostgreSQL, MySQL, MariaDB, and SQLite datasets, shows the freshness of their backups, and marks a dataset `unverified` (never "unbacked") when no evidence source is configured. See [Storage Tab](#storage-tab).
- **Backup verification** — verifies backup evidence from three sources: a read-only scan of the data root, a container labeled `dumbdock.backup-for`, or authenticated push reports from a backup job. Stale and missing backups become acknowledgeable Issues rows. dumbdock never runs a backup, reads a dump, or moves a file. A reference host-side job is included in `scripts/`.
- **Networking tab** — a dedicated tab next to Dashboard showing every container and the networks it is attached to. Running containers are grouped under each network (with their in-network IP and open ports, internal → external), and stopped containers appear in a separate flat list. Data comes from a dedicated `GET /api/networking` endpoint that exposes only identity, state, network IPs, and ports — no Docker labels. Containers that publish ports on `0.0.0.0` (all interfaces) are flagged with a warning banner at the top of the page and a ⚠ marker on their row.
- **Help tab** — an always-visible **Help** tab (after Icons) with static getting-started guides. The first guide, **Configure Traefik API Access**, walks through enabling Traefik's read-only API (insecure entrypoint or secured `api@internal`), configuring auth for dumbdock, verifying with `curl /api/version`, and troubleshooting the errors the Traefik tab surfaces. See [Help Tab](#help-tab).
- **Persistent tabs** — the selected tab is kept in the URL fragment (`#networking`, `#traefik`, `#issues`, …), so a browser refresh (Ctrl+R) leaves you on the same tab, and any tab can be bookmarked or shared. See [Tab Persistence](#tab-persistence).
- **Tiny footprint** — multi-stage Docker build produces a ~10 MB static binary running from `alpine:3.20` (a minimal image with `wget`, used by the container healthcheck).
- **Container healthcheck** — the Compose stack ships a `healthcheck` that confirms the app is listening on its port (a TCP connect, no HTTP route or auth involved), so `docker compose ps` / `docker inspect` report the container as `healthy`. See [Container Healthcheck](#container-healthcheck).
- **Cache-aware HTTP headers** — serves the dashboard HTML with `ETag` and `Cache-Control: no-cache` headers, and API responses with `Cache-Control: no-cache`. The ETag is derived from a build-time version string (Git SHA + timestamp) injected via ldflags, enabling browsers to revalidate efficiently with `304 Not Modified`. Version defaults to `"dev"` for local builds; Docker builds automatically get a version tag.

## Quick Start

### Using Docker Compose (recommended)

```bash
# One command: creates docker-compose.yml from the example (if missing),
# creates .env, builds, starts the stack, and waits for readiness
./control.sh setup

# Or manually:
# cp docker-compose.yml.example docker-compose.yml
# (Optional) Edit docker-compose.yml to configure alerts or mount a config file
# docker compose up -d
```

Then open **http://localhost:8080**.

### Container Healthcheck

The `dumbdock` service defines a Compose `healthcheck` that runs inside the container
and checks that the app is listening on its port with a raw TCP connect:

```yaml
healthcheck:
  test: ["CMD", "nc", "-z", "-w", "2", "127.0.0.1", "8080"]
  interval: 30s
  timeout: 5s
  retries: 3
  start_period: 10s
```

- The probe uses `127.0.0.1:8080` — the fixed in-container listen port (the published
  host port set by `DUMBDOCK_PORT` is irrelevant inside the container) — which avoids any
  DNS lookup.
- It is a pure **TCP port check** (`nc -z`): it never issues an HTTP request, so it is
  independent of every route, its status code, and the auth mode — the healthcheck works
  with `DUMBDOCK_AUTH_MODE=none`, `http-auth`, and `web-auth` alike.
- The `nc` probe requires the Alpine runtime base (`scratch` has no shell or `nc`),
  which is why the image is built `FROM alpine:3.20`.
- Check status with `docker compose ps` (shows `healthy` / `unhealthy`) or
  `docker inspect --format '{{.State.Health.Status}}' dumbdock`.

### Using Docker directly

```bash
docker run -d \
  --name dumbdock \
  -p 127.0.0.1:8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /srv/containers:/srv/containers:ro \
  -e POLL_INTERVAL=10s \
  ghcr.io/jordantrizz/dumbdock:latest
```

The `/srv/containers` mount is optional but needed for the **Storage** tab to verify backup artifacts; without it the tab still shows every database and its data path, and marks each backup as *unverified* rather than claiming it is missing.

### From source

```bash
go build -o dumbdock .
./dumbdock
```

The embedded version (from `VERSION`) and an API endpoint (`/api/version`) let you check which build is running. For Docker builds a build number based on `git rev-parse --short HEAD` (short commit SHA) is also injected — local builds default to build `"0"`.

## Operations (control.sh)

A single `./control.sh` script manages the Docker Compose stack. It auto-detects the compose files, main service, and build args — no configuration needed. `setup` auto-creates `docker-compose.yml` from `docker-compose.yml.example` when the real file is missing, and creates `.env` from `.env.example` (auto-generating an empty `DUMBDOCK_PASSWORD`).

| Command | Description |
|---------|-------------|
| `./control.sh setup [--force] [dev\|prod]` | Create/refresh `.env`, start the stack |
| `./control.sh start` | Start the stack |
| `./control.sh stop` | Stop and remove containers |
| `./control.sh restart` | Restart containers (no rebuild) |
| `./control.sh rebuild` | `git pull` + rebuild images + recreate containers |
| `./control.sh status` | Show container + version status |
| `./control.sh logs [service]` | Tail logs (default: all services) |
| `./control.sh reset [--yes]` | **DESTRUCTIVE** wipe (volumes + data) + fresh setup |
| `./control.sh help` | Show help |

> **Note:** `./control.sh rebuild` replaces the former `rebuild.sh` script, which has been removed.

## Labeling Containers

dumbdock discovers container metadata through Docker labels with the `dumbdock.` prefix.

### docker-compose.yml

```yaml
services:
  myapp:
    image: nginx
    labels:
      dumbdock.name: "My Nginx"
      dumbdock.group: "Web Servers"
      dumbdock.icon: "https://example.com/nginx-icon.png"
      dumbdock.href: "https://myapp.example.com"
      dumbdock.description: "Serves the main website"
      dumbdock.order: "1"
```

| Label | Description |
|-------|-------------|
| `dumbdock.name` | Display name for the card |
| `dumbdock.group` | Group name to organize cards under |
| `dumbdock.icon` | URL to an icon image (SVG or PNG recommended) |
| `dumbdock.href` | Link — clicking the card name opens this URL |
| `dumbdock.description` | Short description shown below the name |
| `dumbdock.order` | Sort order within the group (lower = first) |

> **Note:** If no icon is specified, dumbdock tries to resolve one automatically from your image name. By default, containers with a matching icon are automatically placed in an "Auto Detected" group so they don't clutter the "Unlabeled" section. See [Icons](#icons) below.

### Config File (`dumbdock.json`)

You can also define overrides via a JSON config file — useful for containers you can't or don't want to re-label:

```json
{
  "autoDetection": true,
  "autoDetectedGroup": "Auto Detected",
  "authMode": "web-auth",
  "containers": {
    "my-container-name": {
      "name": "My App",
      "group": "Web Servers",
      "icon": "https://example.com/icon.png",
      "href": "https://myapp.example.com",
      "description": "Does something useful",
      "order": 1
    }
  }
}
```

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `autoDetection` | boolean | `true` | Enable automatic icon detection for unlabeled containers. When enabled and a matching icon is found, the container is placed in `autoDetectedGroup` instead of "Unlabeled". |
| `autoDetectedGroup` | string | `"Auto Detected"` | Group name to use for auto-detected containers. |
| `autoDetectionServiceBlacklist` | array of strings | `["server", "db", "app"]` | Compose service names to skip when trying service-name-based icon matching (e.g., `["server", "db"]`). Generic names are unreliable for icon matching; the container still goes through other fallbacks (OCI title, known slugs). |
| `containerBlacklist` | array of strings | `[]` | Container names to hide entirely from the dashboard (e.g., `["dumbdock", "traefik"]`). Useful for excluding infrastructure containers. |
| `authMode` | string | `web-auth` | Authentication mode: `none`, `http-auth`, or `web-auth`. `DUMBDOCK_AUTH_MODE` overrides it. See [Authentication](#authentication). |
| `containers` | object | `{}` | Per-container overrides keyed by container name. |
| `iconSets` | array | *see [Icons](#icons)* | Configure icon providers. Each entry defines an icon set name, index URL, CDN URL template, format, and optional mappings. When present, replaces the built-in defaults entirely. |

> **Precedence:** Config file overrides are applied first, then auto-detection. If a container has both an explicit config entry and a matching auto-detectable icon, the config entry wins.

Mount the config file:

```yaml
volumes:
  - ./dumbdock.json:/config/dumbdock.json:ro
```

### Startup Logging

On startup dumbdock validates its effective configuration and logs a redacted summary of what it
will use. Validation is advisory — an invalid setting produces a `warning: config: <field>: …`
line and the process continues with defaults, so a typo never takes the dashboard down. A missing
or unparseable `dumbdock.json` is handled the same way (the server falls back to an empty config
and keeps running).

The `startup config:` block lists the version/build, config-file path and status, listen address,
Docker socket, poll interval, auth mode, update-check settings, auto-detection settings,
blacklists, icon sets, alert targets, and Traefik API settings. Secret-bearing values are never
printed — they render as `(set)` / `(not set)`:

| Redacted value | Source |
|----------------|--------|
| Auth password | `DUMBDOCK_PASSWORD` |
| Gotify token | `GOTIFY_TOKEN` |
| Traefik API token | `TRAEFIK_API_TOKEN` or `traefikAPIToken` |
| Traefik API pass | `TRAEFIK_API_PASS` |

Example:

```
dumbdock v0.0.4 (build 9ac2872)
startup config:
  version: 0.0.4 (build 9ac2872)
  config file: /config/dumbdock.json (loaded)
  listen address: :8080
  docker socket: /var/run/docker.sock
  poll interval: 10s
  auth mode: web-auth
  auth password: (set)
  update checks: true (interval 6h0m0s)
  auto-detection: true (group "Auto Detected", service blacklist: server, db, app)
  container blacklist: (none)
  icon sets: selfhst, dashboard-icons
  dashboard url: https://dumbdock.example.com
  ntfy topic: (not set)
  gotify url: (not set)
  gotify token: (not set)
  alert cooldown: 5m0s
  traefik api url: (not set)
  traefik api token: (not set)
  traefik api user: (not set)
  traefik api pass: (not set)
```

### Grouping by Dependency

In addition to grouping by `dumbdock.group` labels or by Compose project, the dashboard can group containers by **dependency**. This view reads the `com.docker.compose.depends_on` label that Docker Compose sets automatically — no `dumbdock.*` label is required.

Containers are grouped under the container(s) they depend on, and the group heading is the dependency container's display name (`dumbdock.name` if set, otherwise the container name). For example, with this compose file:

```yaml
services:
  db:
    image: postgres:16
  app:
    image: myapp
    depends_on:
      - db
```

The **By Dependency** view shows the `app` container under a `db` group heading.

Behavior notes:

- **Direct dependencies only** — grouping uses the immediate `depends_on` targets; there is no transitive resolution (a container that depends on `api`, which itself depends on `db`, is grouped under `api`, not `db`).
- **Multiple dependencies** — a container that depends on several services appears in each of those dependency groups.
- **No resolvable dependency** — containers with no `depends_on` label, or whose dependency service isn't running, go under a `Standalone` group.
- **Toggle** — the button in the nav bar cycles **By Group → By Compose → By Dependency → By Group**. Your choice is remembered across page loads. The toggle is only shown on the Dashboard tab, because it affects only the container grid.

### Storage and backup labels

The **Storage** tab reads these labels; none is required for the dashboard to work.

| Label | Applies to | Effect |
|-------|-----------|--------|
| `dumbdock.backup.engine` | any container | Declares the database family: `postgres`, `mysql`, `mariadb`, or `sqlite`. Overrides image detection. |
| `dumbdock.backup.sqlite` | SQLite workload | Comma-separated **in-container** paths, e.g. `/data/app.db`. Also makes an otherwise generic image count as a SQLite dataset. |
| `dumbdock.backup.database` | database container | Database name shown on the Storage tab and used by the reference backup job. Only a name — never a password. |
| `dumbdock.backup.max-age` | dataset | Freshness window as a Go duration (`26h`, `48h`, `90m`). Overrides the config file and the default. |
| `dumbdock.backup.required` | dataset | `false` keeps a dataset visible but never raises a *missing backup* finding. |
| `dumbdock.backup.disabled` | dataset | `true` waives the backup requirement (`exempt`): visible, never a finding. |
| `dumbdock.backup-for` | backup container | Declares that this container backs up `<project>/<service>`. A clean exit-0 run is evidence; a running container is not. |
| `dumbdock.data.mount` | any container | `<source>:<destination>` (comma-separated) when the datadir is not the engine default. |
| `dumbdock.storage.ignore` | any container | Comma-separated mount destinations, or `all`, to waive data-path findings for deliberate mounts. |

```yaml
services:
  postgres:
    image: postgres:16
    labels:
      dumbdock.backup.database: app
      dumbdock.backup.max-age: 26h
    volumes:
      - /srv/containers/mwp/postgres:/var/lib/postgresql/data

  notes:
    image: alpine:3.20
    labels:
      dumbdock.backup.engine: sqlite
      dumbdock.backup.sqlite: /data/notes.db
    volumes:
      - /srv/containers/notes/app:/data
```

## Traefik Dashboard

The **Traefik** tab is **always visible** in the navigation bar. When a running Traefik container is detected (image name containing "traefik"), clicking the tab displays a full status dashboard fetched from the Traefik API. If no Traefik container is running, the tab shows standard install guidance (including a `docker run` example); if a Traefik container is detected but the API is unreachable, it shows the general error with the resolved API URL for troubleshooting. dumbdock requires **Traefik v3**; other major versions display an "Unsupported Traefik version" error (see [Supported Traefik version](#supported-traefik-version)).

### Auto-Discovery

dumbdock automatically finds the Traefik API URL using a fallback chain:

1. **`dumbdock.traefik.api` label** — if the Traefik container has this Docker label, the value is used as-is.
2. **`TRAEFIK_API_URL` environment variable** — set on the dumbdock container (not Traefik).
3. **Docker network IP** — reads the container's IP address from its first Docker network and appends port `:8080`. If the container's command specifies a custom entrypoint port (`--entrypoints.traefik.address=:PORT`), that port is used instead.
4. **Published port** — falls back to the first published TCP port on `127.0.0.1` (preferring 8080).

**Fallback probing:** if every Traefik API endpoint fails at the primary (container-IP) URL, dumbdock probes candidate URLs in order — the published `8080/tcp` host port, the container IP on `8081` (Traefik's default insecure API entrypoint), and any other published TCP port — and adopts the first one that answers `/api/version`. Probing uses a 2s timeout, sends no credentials, and runs at most once per poll.

If none of these succeed, the Traefik tab shows an error explaining that the API URL could not be resolved.

### Data Displayed

Once the API URL is resolved, dumbdock fetches these Traefik API endpoints concurrently:

| Endpoint | Data Shown |
|----------|------------|
| `/api/version` | Version, codename, start date, Go version |
| `/api/overview` | Counts of HTTP/TCP routers, services, middlewares |
| `/api/entrypoints` | Entrypoint names and addresses |
| `/api/http/routers` | Router name, status, rule, service, entrypoints, middlewares, TLS |
| `/api/http/services` | Service name, status, type, per-server health (UP/DOWN) |
| `/api/http/middlewares` | Middleware name, type, status |
| `/api/tcp/routers` | TCP router name, status, rule, service, entrypoints, TLS |
| `/api/tcp/services` | TCP service name, status, type, per-server health |
| `/api/tls/certificates` | Certificate name, domains, subject, expiry, stores |

Individual endpoint errors are reported gracefully — if an endpoint returns a 404 (e.g., no TLS certificates configured), a "Not available" or "Not configured" message is shown instead of breaking the whole page. The `GET /api/traefik` response lists such endpoints under `notConfigured` (informational) rather than `endpointErrors`, so a 404 never triggers the "Some Traefik API endpoints returned errors" banner or the unreachable-API state.

dumbdock also tolerates both Traefik v2 and v3 API shapes: `/api/overview` counts are accepted either as bare integers (v2) or as objects such as `{"total": N, "warnings": 0, "errors": 0}` (v3).

### Traefik API Enablement

dumbdock reads Traefik's read-only API, which must be enabled and listening where dumbdock can reach it. The most common cause of `connection refused` errors in the logs is a Traefik API that is not exposed:

- **Insecure entrypoint:** start Traefik with `--api.insecure=true` so the dashboard/API is served on the `traefik` entrypoint (default port `8080`).
- **Secured API:** expose the `api@internal` service through a router (optionally with auth), and point dumbdock at it with `TRAEFIK_API_URL` or the `dumbdock.traefik.api` label.

When the API is entirely unreachable, dumbdock logs the failure **once per error-state change** (not on every poll), logs a single `traefik: API recovered` line when it comes back, and the Traefik tab shows a "container detected but API is not accessible" banner containing the resolved API URL and the underlying error.

### API Authentication

Traefik's API can be secured. dumbdock supports two auth modes:

- **Bearer token** — set `TRAEFIK_API_TOKEN` (env var) or `traefikAPIToken` (config file).
- **Basic auth** — set `TRAEFIK_API_USER` and `TRAEFIK_API_PASS` env vars.

The auth header is added server-side only — credentials never reach the browser.

> **Note:** The Traefik tab is always visible in the navigation bar (see above); when a running container with "traefik" in its image name is detected it shows the status dashboard, otherwise it shows install guidance.

## Issues Tab

The **Issues** tab sits between **Traefik** and **Icons** and lists every actionable problem dumbdock can prove from the Docker and Traefik APIs. The navigation button carries a count badge for unacknowledged findings, refreshed on every poll after authentication.

### What is checked

| Category | Severity | Finding |
|----------|----------|---------|
| Container health | `error` | Docker reports the container's health status as `unhealthy` (via the daemon's `health=unhealthy` filter, never inferred from the `Status` text) |
| Port exposure | `warning` | A published port is bound to a non-localhost address (for example `0.0.0.0` or a public IP) |
| Image updates | `warning` | The background registry check found a newer digest for the running tag |
| Traefik API | `error` / `warning` | Every Traefik API endpoint failed (unreachable), or one endpoint returned an error |
| Traefik version | `warning` | The detected major version is not 3 |
| Traefik entities | `error` / `warning` | A router, service, or middleware reports status `error` (error), `disabled`, or `warning` (warning) |
| Traefik backends | `error` | A load-balancer server reports `DOWN` |
| Docker Engine | `warning` | The daemon version is older than the latest stable Moby release (advisory) |
| Storage paths | `warning` / `error` | A compose file outside `/opt/containers/<project>/`, data beside the compose file, or data in a Docker volume (warning); database data in the container layer (error) |
| Database backups | `warning` / `error` | The newest verified backup is older than the dataset's policy (warning) or no backup exists although the policy requires one (error) |

### What is deliberately not a finding

- Containers hidden by `CONTAINER_BLACKLIST` / `containerBlacklist`.
- Stopped, paused, exited, and restarting containers; only currently listed containers are evaluated.
- Containers without a healthcheck, and containers whose health is still `starting`.
- Image update states `unknown` and `error` (shown on cards, but not issues).
- Ports bound to `127.0.0.1`, `::1`, or a private address.
- A missing Traefik container (that is install guidance on the Traefik tab).
- Traefik endpoints that return 404 / "not configured" (for example no TLS certificates).
- Raw container logs — dumbdock does not read log output for this feature.
- A database whose backup is `unverified` because no evidence source is configured, and any dataset waived with `dumbdock.backup.disabled=true`.
- Config-file mounts (matched by extension, or a destination under `/etc/`) and mounts waived with `dumbdock.storage.ignore`.

### Counts and ordering

One row is one finding, and the badge equals the number of **active** (unacknowledged) rows. A container can produce more than one row: an unhealthy container whose image also has an update is two findings. Rows are ordered by severity (`error` first), then category, then title.

### Acknowledging a finding

Each active row has an **Acknowledge** button. Acknowledging removes the finding from the count and moves it to the **Acknowledged** section, where it can be restored with **Unacknowledge**.

- Acknowledgements are stored server-side so every browser sees the same state, and they survive a restart when the `/data` volume is kept.
- An acknowledgement is automatically dropped once a **complete** collection confirms the finding is gone. The next recurrence is active again.
- If a collection pass is incomplete (Docker health list unavailable, or a Traefik endpoint failed), dumbdock keeps existing acknowledgements and labels the page as **partial** rather than assuming the finding resolved.

### Docker Engine version comparison

dumbdock reads the daemon version from `GET /version` and, at most once every 24 hours, the latest stable `moby/moby` GitHub release. If the daemon is older, a warning appears. The comparison is advisory: distribution packages and managed container hosts may intentionally pin an older Docker Engine, so verify before upgrading. When the comparison is unavailable, the page reports it as coverage metadata rather than inventing a finding.

### Coverage and refresh

The full snapshot is rebuilt in the background poll; the Issues page itself only reads a cached snapshot from `/api/issues`, so it never blocks on Docker or GitHub. The badge refreshes every 10 seconds; the page re-renders only while it is the visible tab. The **Collection** line and any warning banner state whether all inputs were checked.

> **Read-only:** the Issues tab never pulls images, restarts containers, changes port bindings, upgrades Docker, or edits Traefik. Acknowledging a finding changes only dumbdock's own state file.

## Storage Tab

The **Storage** tab sits between **Issues** and **Icons**. It answers two questions per compose project: *is the compose file and its data where the host convention says it should be?* and *when was each database last backed up?*

### Conventions

| Rule | Default | Override |
|------|---------|----------|
| Compose file | `/opt/containers/<project>/docker-compose.yml` (any path under `<project>/`) | `DUMBDOCK_CONTAINER_ROOT` / `storage.containerRoot` |
| Persistent data | `/srv/containers/<project>/<service>/` | `DUMBDOCK_DATA_ROOT` / `storage.dataRoot` |
| Backup artifacts | `/srv/containers/<project>/<service>/backups/` | `DUMBDOCK_BACKUP_ROOT` / `storage.backupRoot` |
| Freshness | `26h` per required dataset | `dumbdock.backup.max-age`, `storage.backups.<project>/<service>.maxAge` |

Only compose-managed containers are evaluated. A writable bind mount is a **data** mount unless it looks like a config artifact (a known config extension, or a destination under `/etc/`) or is waived with `dumbdock.storage.ignore`. A mount whose source is inside `com.docker.compose.project.working_dir` is reported as *beside the compose file*; a named or anonymous volume is reported as *in a Docker volume*; a database with no matching mount is reported as *in the container layer*.

### Databases and verdicts

The engine is detected from the image repository (`postgres`, `postgis`, `timescaledb`, `mysql`, `percona`, `mariadb`, with Bitnami paths) or declared with `dumbdock.backup.engine`. SQLite is detected from `dumbdock.backup.sqlite` or from `*.db`, `*.sqlite`, `*.sqlite3` files found under the service data directory (bounded depth and entry count). The datadir comes from the engine default, `PGDATA`, or `dumbdock.data.mount`.

| Verdict | Meaning |
|---------|---------|
| `ok` | Newest evidence is within the policy window |
| `stale` | Evidence exists but is older than the window |
| `missing` | The policy requires a backup and none was found |
| `unverified` | No evidence provider is configured, or the dataset is not required — **not** a claim that no backup exists |
| `exempt` | `dumbdock.backup.disabled=true`: visible, never a finding |

### Evidence providers

1. **Filesystem** — a read-only scan of `<backupRoot>/<project>/<service>/backups/`. Only the directory listing and file modification times are read; dump contents are never opened. The scan is bounded (200 entries per directory, a three-second budget), refuses symlinks, and matches logical-dump suffixes (`.sql`, `.sql.gz`, `.dump`, `.tar.gz`, `.sqlite`, `.sqlite.gz`, `.db.gz`, `.bak`). The data root must be mounted into the container at the same path:
   ```yaml
   volumes:
     - /srv/containers:/srv/containers:ro
   ```
2. **Backup containers** — a container labeled `dumbdock.backup-for=<project>/<service>` counts when its last run **exited 0** (read from `State.ExitCode` / `FinishedAt`). A running container is not proof of a completed run.
3. **Push reports** — a backup job POSTs to `/api/backups/report` with `DUMBDOCK_BACKUP_TOKEN`. Reports live in `backup-reports.json` (`0600`) in the state directory.

A provider that is not configured is shown as coverage and keeps datasets `unverified`. A provider that **errors** marks the collection as partial, and partial collection never prunes acknowledgements. dumbdock does not run `docker exec`, read a dump, pull an image, restart a container, or move a file.

### Migrating this host

Existing paths are **reported, not changed**. Moving a project is a maintenance-window operation with sudo, performed by the operator:

```bash
# 1. Stop the project and take a restorable dump first.
docker compose -p mwp down
docker exec mwp-postgres-1 pg_dump -U postgres app | gzip >/tmp/app.sql.gz

# 2. Move the compose project and create the data directory.
sudo install -d -m 0755 /opt/containers /srv/containers/mwp/postgres
sudo mv /root/containers/mwp /opt/containers/mwp

# 3. Copy data out of a named volume (repeat per service).
docker run --rm -v mwp_pgdata:/from -v /srv/containers/mwp/postgres:/to \
  alpine sh -c 'cp -a /from/. /to/'

# 4. Point the compose file at /srv/containers/... and restart.
docker compose -p mwp -f /opt/containers/mwp/docker-compose.yml up -d
```

Verify with the Storage tab (mounts flip to `OK`, compose findings disappear) and with `pg_restore --list /tmp/app.sql.gz` before removing the old volume.

## Backup Reference Job

`scripts/backup-databases.sh` is the reference producer for the evidence the Storage tab verifies. It is host-side tooling: dumbdock never runs, schedules, or reads it.

- Discovers containers by `dumbdock.backup.engine` and validates the compose project/service labels.
- PostgreSQL: `pg_dump` inside the container; MySQL/MariaDB: `mysqldump` or `mariadb-dump` (single transaction, routines, events); SQLite: `sqlite3 ".backup"` plus `docker cp`, because a plain file copy of a live SQLite database is not safe.
- Writes `<dataRoot>/<project>/<service>/backups/<engine>-<service>-<UTC stamp>.sql.gz` with `umask 077` and mode `0600`, rejects an empty dump, and keeps the newest 14 artifacts (`BACKUP_RETENTION`).
- `--dry-run` prints every planned action without writing.
- Reports each outcome to dumbdock when `DUMBDOCK_URL` and `DUMBDOCK_BACKUP_TOKEN` are set; a failed dump reports `ok:false` and exits non-zero.

```bash
# Install (review the script first; it runs as root with docker access).
sudo install -m 0755 scripts/backup-databases.sh /usr/local/bin/dumbdock-backup
sudo install -m 0644 scripts/dumbdock-backup.service /etc/systemd/system/
sudo install -m 0644 scripts/dumbdock-backup.timer /etc/systemd/system/
sudo tee /etc/default/dumbdock-backup >/dev/null <<'EOF'
DUMBDOCK_DATA_ROOT=/srv/containers
BACKUP_RETENTION=14
DUMBDOCK_URL=http://127.0.0.1:8080
DUMBDOCK_BACKUP_TOKEN=replace-me
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now dumbdock-backup.timer

# Verify the artifact, then let dumbdock confirm it.
gzip -t /srv/containers/mwp/postgres/backups/*.sql.gz
pg_restore --list /srv/containers/mwp/postgres/backups/<artifact>.sql.gz | head
sqlite3 <artifact>.sqlite "pragma integrity_check;"
```

The timer runs daily at 02:30 with a randomized delay. A dataset with `dumbdock.backup.disabled=true` is skipped by the monitor but still backed up if it carries the engine label — remove the label to stop dumping it.

## Help Tab

The **Help** tab is always visible in the navigation bar, immediately after **Icons**. It contains static getting-started guides served as plain HTML — there is no data fetch or background refresh, so the page renders instantly.

The first guide is **Configure Traefik API Access**, which covers:

- **Option 1 — Insecure API**: starting Traefik with `--api.insecure=true` so the API/dashboard is served on the `traefik` entrypoint (default port `8080`), with `docker run` and Compose snippets and a security note about keeping that port on a private network. The guide notes that publishing `8080` is optional — on a shared Docker network dumbdock reaches the API at the container's IP, and it shows how to test it with `docker exec traefik wget/curl` (or a throwaway `curlimages/curl` container on the same network).
- **Option 2 — Secured API**: exposing the `api@internal` service through a router (with optional basic-auth middleware) and pointing dumbdock at it via `TRAEFIK_API_URL` or the `dumbdock.traefik.api` label.
- **Authentication**: `TRAEFIK_API_TOKEN` (bearer) or `TRAEFIK_API_USER` + `TRAEFIK_API_PASS` (basic), all applied server-side.
- **Verification**: `curl -s http://<traefik-host>:8080/api/version` and the expected JSON response.
- **Troubleshooting**: the `connection refused` signature, the Traefik tab's "container detected but API is not accessible" banner, wrong-URL fixes, 401/403 auth failures, and normal partial-data cases.

To add a future guide, append a new `.traefik-section` block inside `#page-help` in `index.html` and add an entry to the guide table of contents — no JavaScript or API changes are required.

## Tab Persistence

The active tab is reflected in the URL fragment, so a **refresh (Ctrl+R) keeps you on the same tab** instead of returning to the Dashboard:

| Tab | URL |
|-----|-----|
| Dashboard | `/#dashboard` |
| Networking | `/#networking` |
| Traefik | `/#traefik` |
| Issues | `/#issues` |
| Storage | `/#storage` |
| Icons | `/#icons` |
| Help | `/#help` |

The navigation order is Dashboard, Networking, Traefik, Issues, Storage, Icons, Help. The **By Compose / By Dependency** grouping toggle and **Copy JSON** control exist only on the Dashboard; switching tabs hides them without changing the saved grouping mode.

Clicking a tab updates the fragment, which makes each tab **bookmarkable/shareable** and lets the browser **back/forward buttons** move between visited tabs. On load, the tab named by the fragment is restored immediately (its data is fetched after the auth check resolves, so no requests are made before login).

The existing in-page Help anchors (`#help-traefik-api`, `#help-traefik-verify`) still work: an unrecognized fragment resolves to the tab that contains the matching element, and dumbdock scrolls to it once that tab is visible. Any other unrecognized fragment falls back to the Dashboard.

## Alerts

dumbdock can notify you when new unlabeled containers appear.

### ntfy.sh

```yaml
environment:
  - NTFY_TOPIC=mytopic
  - DUMBDOCK_URL=https://dumbdock.example.com   # optional: link back to dashboard
```

### Gotify

```yaml
environment:
  - GOTIFY_URL=https://gotify.example.com
  - GOTIFY_TOKEN=your-app-token
```

### Alert Cooldown

```yaml
environment:
  - ALERT_COOLDOWN=5m   # minimum time between alert batches (default: 5m)
```

## Authentication

dumbdock supports three authentication modes, selected by the `DUMBDOCK_AUTH_MODE` environment variable or the `authMode` setting in `dumbdock.json` (the environment variable wins):

| Mode | Value | Behavior |
|------|-------|----------|
| No auth | `none` | Dashboard and API are open. |
| HTTP Basic | `http-auth` | Browser-native login prompt; any username is accepted but the password must match `DUMBDOCK_PASSWORD`. |
| Web login | `web-auth` | Login form overlay (username + password + **Remember me**); session cookie `dumbdock_session` (`HttpOnly`, `SameSite=Lax`, `Path=/`). Standard sessions last 8 hours, "Remember me" sessions last 30 days. Sessions live in memory — restarting dumbdock logs everyone out. A logout button appears in the nav bar while logged in. |

When neither `DUMBDOCK_AUTH_MODE` nor `authMode` is set, the mode defaults to `web-auth`. Set it to `none` explicitly to disable auth.

When an auth mode is active but `DUMBDOCK_PASSWORD` is empty, a random password is generated for the run and logged once at startup (e.g. `warning: DUMBDOCK_PASSWORD is empty; generated password for this run: …`). It is held in memory only and changes on every restart.

Unknown mode values (from either the env var or the config file) log a warning and disable auth.

> **HTTPS note:** the session cookie does not set the `Secure` flag (dumbdock itself serves plain HTTP). Terminate TLS in a reverse proxy (e.g. Traefik) in front of dumbdock when exposing it beyond localhost.

Configure via `.env` (see `.env.example`):

```bash
DUMBDOCK_AUTH_MODE=web-auth   # or http-auth for a Basic pop-up; none to disable
DUMBDOCK_PASSWORD=choose-a-strong-password
DUMBDOCK_PORT=8080
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCKER_SOCK` | `/var/run/docker.sock` | Path to the Docker socket |
| `LISTEN_ADDR` | `:8080` | HTTP listen address (explicit override; wins over `DUMBDOCK_PORT`) |
| `DUMBDOCK_PORT` | `8080` | Port the app listens on when `LISTEN_ADDR` is unset, and the default host port in `docker-compose.yml.example` (the container always listens on 8080 there) |
| `POLL_INTERVAL` | `10s` | How often to poll Docker for container changes |
| `DUMBDOCK_CONFIG` | `/config/dumbdock.json` | Path to the JSON override config file |
| `DUMBDOCK_URL` | *(empty)* | Public URL of the dashboard (used in alert links) |
| `NTFY_TOPIC` | *(empty)* | ntfy.sh topic for push notifications |
| `GOTIFY_URL` | *(empty)* | Gotify server URL |
| `GOTIFY_TOKEN` | *(empty)* | Gotify app token |
| `ALERT_COOLDOWN` | `5m` | Minimum time between consecutive alert batches |
| `AUTO_DETECTION` | `true` | Enable automatic icon detection for unlabeled containers. Set to `false` to disable. |
| `AUTODETECT_GROUP_NAME` | `"Auto Detected"` | Group name for auto-detected containers. Only used when `AUTO_DETECTION` is enabled. |
| `AUTO_DETECTION_SERVICE_BLACKLIST` | `"server,db,app"` | Comma-separated list of compose service names to skip when trying service-name-based icon matching (e.g., `"server"`, `"server,db,app"`). Generic names are unreliable for icon matching; the container still goes through other fallbacks. |
| `CONTAINER_BLACKLIST` | *(empty)* | Comma-separated list of container names to hide entirely from the dashboard (e.g., `"dumbdock","traefik"`). Useful for excluding infrastructure containers. |
| `DUMBDOCK_UPDATE_CHECK` | `true` | Check Docker Hub / GHCR for newer digests of each image tag. Set to `false` to disable the check entirely. Also configurable via `"updateCheck"` in `dumbdock.json`. |
| `DUMBDOCK_UPDATE_INTERVAL` | `6h` | How long a successful update check is cached before the image tag is re-checked. |
| `DUMBDOCK_PASSWORD` | *(empty)* | Password for dashboard auth. With `DUMBDOCK_AUTH_MODE=http-auth` it enables HTTP Basic Authentication (any username accepted); with `web-auth` it is the login-form password. When an auth mode is active and this is empty, a random password is generated and logged once for the run. Auto-generated by `./control.sh setup`. |
| `DUMBDOCK_AUTH_MODE` | `web-auth` | Authentication mode: `none`, `http-auth`, or `web-auth` (see [Authentication](#authentication)). Overrides `authMode` in `dumbdock.json`. Defaults to `web-auth`; set to `none` to disable auth. |
| `DUMBDOCK_STATE_DIR` | `/data` | Directory for persisted runtime state — `issues-acknowledged.json` and `backup-reports.json` (mode `0600`). The Compose file mounts this as the `dumbdock-data` named volume so acknowledgements and reports survive a restart. |
| `DUMBDOCK_CONTAINER_ROOT` | `/opt/containers` | Where compose projects are expected to live. A compose file outside `<root>/<project>/` is a Storage finding. Also `storage.containerRoot` in the config file. |
| `DUMBDOCK_DATA_ROOT` | `/srv/containers` | Where persistent data is expected to live (`<root>/<project>/<service>/`). Also the default evidence root. Also `storage.dataRoot`. |
| `DUMBDOCK_BACKUP_ROOT` | value of `DUMBDOCK_DATA_ROOT` | Read-only path scanned for `<project>/<service>/backups/` artifacts. Mount it into the container at the same path. Also `storage.backupRoot`. |
| `DUMBDOCK_BACKUP_MAX_AGE` | `26h` | Default freshness window for a required backup. Overridden per dataset by `dumbdock.backup.max-age` or `storage.backups.<project>/<service>.maxAge`. |
| `DUMBDOCK_BACKUP_SCAN_INTERVAL` | `5m` | How often inspects, the SQLite scan, and evidence collection run. Mounts and labels are still read on every 10s poll. |
| `DUMBDOCK_BACKUP_TOKEN` | *(empty)* | Bearer token that lets a host backup script POST to `/api/backups/report` without a dashboard session. Grants report-write only; redacted in the startup log. |
| `TRAEFIK_API_URL` | *(empty)* | Explicit Traefik API base URL. Overrides auto-discovery. |
| `TRAEFIK_API_TOKEN` | *(empty)* | Bearer token for Traefik API authentication (sent as `Authorization: Bearer <token>`). Also accepted via `traefikAPIToken` in the config file. |
| `TRAEFIK_API_USER` | *(empty)* | Username for Traefik Basic auth (use with `TRAEFIK_API_PASS`). |
| `TRAEFIK_API_PASS` | *(empty)* | Password for Traefik Basic auth (use with `TRAEFIK_API_USER`). |

## Icons

dumbdock ships with two built-in icon sets that are enabled by default:

1. **[selfhst/icons](https://github.com/selfhst/icons)** — served from `https://cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg`. A curated icon map (`icons.json`) is embedded in the binary to map common Docker image names to the correct icon slug. The live index is fetched at startup for up-to-date slug discovery.
2. **[dashboard-icons](https://github.com/homarr-labs/dashboard-icons)** — served from `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/{slug}.svg`. The icon index (`tree.json`) is fetched at startup; no static mapping is needed since icon slugs match image names 1:1.

When a container has no explicit `dumbdock.icon` label, dumbdock derives a slug from the image name (e.g., `portainer/portainer-ce:latest` → `portainer`) and checks each configured icon set in priority order. The first match wins.

If no match is found across any icon set, a best-guess URL from the highest-priority set is returned and a generic placeholder is shown.

### Priority & Resolution Order

1. **Full image path in static mappings** (e.g., `portainer/portainer-ce` → `portainer`)
2. **Last path component in static mappings** (e.g., `portainer-ce` → `portainer`)
3. **Last path component as a known slug** (e.g., `nginx` matches a known icon name)
4. **Best-guess URL** (falls back to the highest-priority icon set)

### Configuring Icon Sets

You can replace the built-in icon sets entirely by adding an `iconSets` array to your `dumbdock.json` config file:

```json
{
  "iconSets": [
    {
      "name": "selfhst",
      "indexUrl": "https://cdn.jsdelivr.net/gh/selfhst/icons@main/index.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/selfhst/icons@main/svg/{slug}.svg",
      "indexFormat": "map",
      "mappings": {
        "actualbudget": "actual-budget",
        "nginx": "nginx"
      },
      "priority": 0
    },
    {
      "name": "dashboard-icons",
      "indexUrl": "https://raw.githubusercontent.com/homarr-labs/dashboard-icons/refs/heads/main/tree.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/{slug}.svg",
      "indexFormat": "tree",
      "indexField": "svg",
      "priority": 1
    },
    {
      "name": "my-custom-set",
      "indexUrl": "/config/my-icons/index.json",
      "cdnUrlTemplate": "https://cdn.jsdelivr.net/gh/my/icons/svg/{slug}.svg",
      "indexFormat": "list",
      "priority": 2
    }
  ]
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | — | A human-readable identifier for the icon set (used in diagnostics). |
| `indexUrl` | string | `""` | URL or local file path to the icon index. Supports `http://`, `https://`, absolute paths (`/path/to/file.json`), and `file://` URIs. Leave empty to skip index fetching. |
| `cdnUrlTemplate` | string | — | CDN URL with a `{slug}` placeholder, e.g. `https://cdn.example.com/icons/{slug}.svg`. |
| `indexFormat` | string | `"map"` | How to parse the index: `"map"` (flat `{"key": "slug"}` — values are slug names), `"list"` (array of slug strings), or `"tree"` (object with an array field of filenames). |
| `indexField` | string | `"svg"` | For `"tree"` format, the field name containing the filename array (e.g. `"svg"`, `"png"`). |
| `mappings` | object | `{}` | Static image-key → icon-slug mappings. These take precedence over index-based slug resolution. Keys are Docker image path components (e.g. `"portainer/portainer-ce"`), values are icon slug names (e.g. `"portainer"`). |
| `priority` | number | `0` | Lower values are checked first. The first matching icon set wins. |

> **Note:** When `iconSets` is present in the config file, the built-in defaults (selfhst + dashboard-icons) are **not** automatically included. If you want both defaults plus custom sets, duplicate the default entries in your config.

### Auto-detection

When `AUTO_DETECTION` is enabled (default), unlabeled containers whose image name matches an entry in `icons.json` are automatically placed in the "Auto Detected" group, keeping them organized and visible on the dashboard. Containers without a matching icon remain in the "Unlabeled" section.

Containers running dumbdock itself (`dumbdock`, `jordantrizz/dumbdock`, or `ghcr.io/jordantrizz/dumbdock`) are automatically detected and shown with the dumbdock icon.

This behavior can be customized or disabled via the `AUTO_DETECTION` / `AUTODETECT_GROUP_NAME` environment variables or the `autoDetection` / `autoDetectedGroup` config file settings. Config file overrides take precedence — containers with an explicit config entry are placed in their configured group regardless of auto-detection.

## Network Warnings

dumbdock surfaces two types of network-related warnings on every container card and in the unlabeled list:

### Public Port Bindings ▲

A red triangle (▲) indicates the container has at least one port bound to a non-localhost IP (e.g., `0.0.0.0`, a public interface). Hovering shows which IPs are affected. Containers with all ports bound to `127.0.0.1` or `::1` show no warning.

**Best practice:** Bind containers to `127.0.0.1` and let a reverse proxy (like Traefik) handle external traffic.

### Networking Tab Banner ⚠

On the **Networking** tab, containers that publish ports on `0.0.0.0` (all interfaces) trigger a warning banner at the top of the page listing the affected containers, and each affected row shows a ⚠ marker. This makes it easy to spot services that are reachable from any network interface rather than just `127.0.0.1` or a specific private IP.

### Traefik Detection 🔗

A link icon (🔗) appears when a container has `traefik.enable=true` in its labels. If the container also defines `traefik.http.routers.<name>.rule=Host(\`hostname\`)`, dumbdock extracts the hostname and displays it as a clickable `https://` link right on the card.

Example labels that trigger Traefik detection:

```yaml
labels:
  traefik.enable: "true"
  traefik.http.routers.myapp.rule: "Host(`myapp.example.com`)"
```

> **Note:** dumbdock assumes `https://` for all extracted URLs since Traefik typically fronts TLS-terminated services.

### API Fields

The `/api/containers` response includes these warning fields per card:

| Field | Type | Description |
|-------|------|-------------|
| `hasPublicBinding` | boolean | True if any port is bound to a non-localhost IP |
| `publicBindingIPs` | string[] | List of non-localhost IPs the container is bound to |
| `traefikEnabled` | boolean | True if `traefik.enable=true` label is present |
| `traefikURLs` | string[] | URLs extracted from Traefik Host rules |
| `updateStatus` | string | Image update state: `up-to-date`, `update-available`, `unknown`, or `error`. Omitted until the first background check completes. |
| `updateAvailable` | boolean | True when a newer digest is served by the registry (only set when `updateStatus` is `update-available`) |
| `currentDigest` | string | Digest of the locally pulled image |
| `latestDigest` | string | Digest currently served by the registry |

## Image Update Checks

dumbdock can flag containers whose running image tag has moved upstream. A background checker resolves the current digest of each image tag from the registry and compares it against the digest of the locally pulled image; a mismatch means a newer image is available.

- **Badges** — cards for containers running an outdated image show an `⬆ update available` badge in the warnings row. Images that cannot be checked show a neutral `? update unknown` badge. The expanded card detail lists the local (`currentDigest`) and registry (`latestDigest`) digests.
- **Supported registries** — Docker Hub (`docker.io`, including bare `library/*` names) and GitHub Container Registry (`ghcr.io`), queried anonymously. Images from any other registry, and locally built images with no `RepoDigests`, are reported as `unknown`.
- **Pinned images** — references pinned by digest (e.g. `nginx@sha256:…`) are intentionally fixed and are not checked.
- **Caching** — each `repo:tag` result is cached for 6 hours (configurable with `DUMBDOCK_UPDATE_INTERVAL`). Checks run in the background (at most 4 concurrent requests) and never block the dashboard poll. Failed checks retry after 15 minutes.
- **Caveats** — the check only detects a tag that has *moved*; a pinned tag such as `nginx:1.24` reads as up to date even if `nginx:1.25` exists upstream. Private registries are not supported (no credentials are ever sent) and Docker Hub's anonymous rate limit applies.

Disable the feature with `DUMBDOCK_UPDATE_CHECK=false` or `"updateCheck": false` in `dumbdock.json`.

Containers with an available update also appear as findings on the [Issues tab](#issues-tab); `unknown` and `error` statuses are intentionally not issues.

## Dashboard Scoreboard

The dashboard shows a clickable scoreboard directly above the legend, summarizing the current container set:

| Tile | Counts containers with |
|------|------------------------|
| **Updates** | `updateAvailable` — a secondary line shows the number with an `unknown` update status |
| **Private Port Binding** | `hasPrivateBinding` |
| **Localhost Port Binding** | `hasLocalBinding` |
| **Traefik Enabled** | `traefikEnabled` |

- Counts cover every container in the `/api/containers` response (labeled + unlabeled), deduplicated by container ID, and refresh on each 10-second poll.
- A container can be counted in more than one tile (for example, a container can have both a private binding and Traefik enabled).
- Clicking a tile filters the dashboard to a flat grid of matching containers; clicking the active tile again, or the **Clear filter** control, restores the normal grouped/compose/dependency view.
- Tiles with a zero count are dimmed. The filter is not persisted across page reloads.

## API

dumbdock exposes these JSON API endpoints:

**`GET /api/containers`**

Returns all container cards, groups, and detection status. In addition to per-card fields, the top-level response includes `hasTraefik: true` when a Traefik container is running.

**`GET /api/traefik`**

Returns Traefik detection and status data:

```json
{
  "found": true,
  "apiUrl": "http://172.17.0.2:8080",
  "error": "",
  "data": {
    "apiUrl": "http://172.17.0.2:8080",
    "authConfigured": false,
    "version": {"version": "v3.0", "codename": "...", "startDate": "...", "goversion": "..."},
    "overview": {
      "http": {"routers": 5, "services": 5, "middlewares": 2},
      "tcp": {"routers": 0, "services": 0, "middlewares": 0}
    },
    "entrypoints": [{"name": "web", "address": ":80"}, ...],
    "httpRouters": [...],
    "httpServices": [...],
    "middlewares": [...],
    "tcpRouters": [...],
    "tcpServices": [...],
    "tlsCerts": [...],
    "endpointErrors": {},
    "notConfigured": [],
    "majorVersion": 3,
    "versionSupported": true
  }
}
```

When no Traefik container is found, `found` is `false` and `data` is `null`.

**`GET /api/issues`**

Returns the cached host-issue snapshot. The endpoint never calls Docker or GitHub; the poll loop builds the snapshot.

```json
{
  "count": 2,
  "items": [
    {
      "id": "<64-char hex>",
      "category": "container-health",
      "severity": "error",
      "title": "Container is unhealthy",
      "detail": "api (api:1) reports Docker health status \"unhealthy\".",
      "source": "container",
      "containerId": "0123456789ab",
      "containerName": "api",
      "firstObservedAt": "2026-11-06T12:00:00Z",
      "lastObservedAt": "2026-11-06T12:00:00Z",
      "acknowledged": false
    }
  ],
  "acknowledged": [],
  "coverage": {
    "collectionComplete": true,
    "dockerVersion": {"checked": true, "lastCheckedAt": "2026-11-06T00:00:00Z"},
    "warnings": []
  },
  "checkedAt": "2026-11-06T12:00:00Z"
}
```

`items` holds active findings and `acknowledged` holds the same record shape with `acknowledged: true` and `acknowledgedAt`. `count` is the number of active findings. The response contains no Docker labels, Traefik credentials, registry tokens, or log text.

**`POST /api/issues/{id}/acknowledge`** and **`DELETE /api/issues/{id}/acknowledge`**

Acknowledge or unacknowledge one finding. Both require the same authentication as the dashboard **and** the non-simple header `X-Dumbdock-Request: issues` (the custom header is a CSRF control that an HTML form cannot set). The body is empty; the success response is the updated `GET /api/issues` payload.

| Status | Meaning |
|--------|---------|
| `200` | Updated snapshot returned |
| `400` | The `{id}` is not a server-issued issue ID |
| `403` | Missing `X-Dumbdock-Request: issues` |
| `404` | Unknown or already-resolved issue ID |
| `500` | The acknowledgement could not be persisted; the published snapshot is unchanged |

```bash
curl -X POST -H 'X-Dumbdock-Request: issues' \
  http://127.0.0.1:8080/api/issues/<id>/acknowledge
```

**`GET /api/storage`**

Returns the cached storage snapshot: one dataset per detected database, every writable mount on a compose-managed container, and the evidence-provider coverage. Like `/api/issues`, the handler only reads the snapshot built by the poll loop.

```json
{
  "datasets": [
    {
      "key": "mwp/postgres",
      "project": "mwp",
      "service": "postgres",
      "containerName": "mwp-postgres-1",
      "containerId": "0123456789ab",
      "engine": "postgres",
      "database": "app",
      "dataKind": "volume",
      "dataDestination": "/var/lib/postgresql/data",
      "dataPath": "mwp_pgdata",
      "dataPathOk": false,
      "dataIssue": "data-named-volume",
      "expectedDataPath": "/srv/containers/mwp/postgres",
      "backupDir": "/srv/containers/mwp/postgres/backups",
      "policyMaxAge": "26h0m0s",
      "policySource": "default",
      "required": true,
      "exempt": false,
      "verdict": "missing",
      "reason": "no backup evidence found",
      "evidence": []
    }
  ],
  "mounts": [
    {
      "project": "mwp", "service": "postgres", "containerName": "mwp-postgres-1",
      "type": "volume", "source": "/var/lib/docker/volumes/mwp_pgdata/_data",
      "destination": "/var/lib/postgresql/data", "rw": true,
      "class": "volume", "expectedDataPath": "/srv/containers/mwp/postgres"
    }
  ],
  "coverage": {
    "collectionComplete": true,
    "containerRoot": "/opt/containers",
    "dataRoot": "/srv/containers",
    "backupRoot": "/srv/containers",
    "providers": [
      {"name": "filesystem", "enabled": true, "configured": true, "lastCheckedAt": "2026-11-07T12:00:00Z"},
      {"name": "backup-containers", "enabled": false, "configured": false},
      {"name": "reports", "enabled": true, "configured": true}
    ]
  },
  "checkedAt": "2026-11-07T12:00:00Z"
}
```

`verdict` is `ok`, `stale`, `missing`, `unverified`, or `exempt`. `unverified` means no evidence provider is configured (or the dataset is not required) — it is never presented as a missing backup. The response contains no file contents, environment values, or label dumps.

**`POST /api/backups/report`**

Accepts one backup report. It requires authentication (a dashboard session **or** `Authorization: Bearer <DUMBDOCK_BACKUP_TOKEN>`) and the non-simple header `X-Dumbdock-Request: backups`. Only targets that currently exist as detected datasets are accepted, and the artifact is a bare file name — never a path.

```json
{
  "target": "mwp/postgres",
  "engine": "postgres",
  "ok": true,
  "at": "2026-11-07T02:30:12Z",
  "artifact": "postgres-postgres-20261107T023012Z.sql.gz",
  "detail": "pg_dump ok"
}
```

| Status | Meaning |
|--------|---------|
| `200` | Report stored |
| `400` | Malformed body, artifact with a path separator/control character, engine mismatch, bad or too-old timestamp |
| `403` | Missing `X-Dumbdock-Request: backups` |
| `404` | `target` is not a currently detected dataset |
| `413` | Body larger than 16 KiB |
| `500` | The report could not be persisted |

A future `at` (more than five minutes ahead of server time) is clamped to now. Reports for a dataset that no longer exists are pruned after 30 days.

```bash
curl -X POST -H 'X-Dumbdock-Request: backups' \
  -H "Authorization: Bearer $DUMBDOCK_BACKUP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target":"mwp/postgres","engine":"postgres","ok":true,"artifact":"app.sql.gz"}' \
  http://127.0.0.1:8080/api/backups/report
```

### Supported Traefik version

dumbdock requires **Traefik v3**. The detected major version is parsed from `/api/version`, returned as `majorVersion` / `versionSupported`, and:

- a **v3** server renders normally with no warning;
- a non-v3 (or unparseable) server shows an **"Unsupported Traefik version"** error banner in the Traefik tab while still rendering any partial data, and the server logs `traefik: unsupported version "…" (only v3 is supported)` **once per state change** (never on every poll).

**`GET /api/containers`**

```json
{
  "cards": [...],
  "unlabeled": [...],
  "groups": ["Web Servers", "Databases", "Other"],
  "grouped": {
    "Web Servers": [...],
    "Databases": [...]
  }
}
```

Each card object includes: `name`, `group`, `icon`, `href`, `description`, `order`, `containerId`, `containerName`, `image`, `status`, `state`, `ports`, `created`, `labels`, `hasLabels`, `hasPublicBinding`, `publicBindingIPs`, `traefikEnabled`, `traefikURLs`, `updateStatus`, `updateAvailable`, `currentDigest`, `latestDigest`.

## Development

```bash
# Build
go build -o dumbdock .

# Run locally (requires Docker socket access)
./dumbdock

# Rebuild and restart via docker-compose
./rebuild.sh
```

### Project Structure

```
.
├── main.go          # Entry point, HTTP server, polling loop
├── auth.go          # Auth modes (http-auth / web-auth), session store, login/logout
├── .env.example     # Documented runtime config (copied to .env by control.sh setup)
├── docker.go        # Docker API client (socket-based HTTP)
├── labels.go        # Label parsing and container card model
├── config.go        # JSON config file loading and override logic
├── alerts.go        # ntfy.sh and Gotify alert notifications
├── icons.go         # Icon set abstraction, index fetching, and resolution
├── icons.json       # Default image-name → icon-slug mappings for selfhst set
├── traefik.go       # Traefik container detection, Docker inspect, API client
├── issues.go        # Host issue model, collectors, acknowledgement store and HTTP handlers
├── issues_test.go   # Unit tests for issue collectors, persistence, handlers
├── docker_version.go # Cached latest Moby release lookup for the Docker Engine check
├── docker_version_test.go
├── warnings.go      # Port binding and Traefik label parsing
├── updates.go       # Image update checks: reference parsing, registry clients, cache
├── updates_test.go  # Unit tests for the update checker
├── index.html       # Embedded single-page UI
├── Dockerfile       # Multi-stage build (golang → alpine:3.20)
├── docker-compose.yml.example
└── rebuild.sh       # Helper to rebuild and restart
```

> This project uses AI-assisted development. See [AI-Assisted Development](#ai-assisted-development).

## AI-Assisted Development

This project was developed with the assistance of AI coding tools. AI was used for code generation, debugging, documentation, and architecture decisions across the entire codebase — including the Go backend, frontend dashboard, Docker tooling, and security analysis.

**All AI-generated code has been reviewed, tested, and validated by a human** before being merged. The project includes:

- A documented [security review](security.md) identifying and triaging potential vulnerabilities
- Manual verification of AI-generated logic against Docker API behavior
- Review of all AI-authored documentation for accuracy

For AI agents and tools that may interact with this repository, see [AGENTS.md](AGENTS.md).

## License

MIT
