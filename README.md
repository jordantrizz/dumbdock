# dumbdock

A simple, no-frills web dashboard for your Docker containers. dumbdock shows a clean overview of your running containers, grouped and organized with custom labels — and alerts you when new unlabeled containers appear.

![screenshot](https://img.shields.io/badge/status-stable-brightgreen)
![go](https://img.shields.io/badge/go-1.22-blue)
![docker](https://img.shields.io/badge/docker-ready-2496ED)

## Features

- **Automatic discovery** — polls the Docker socket and lists all running containers.
- **Label-driven organization** — use `dumbdock.*` labels on your containers to set names, groups, icons, links, descriptions, and sort order.
- **Smart icon resolution** — automatically detects icons for unlabeled containers from [selfhst/icons](https://github.com/selfhst/icons), [dashboard-icons](https://github.com/homarr-labs/dashboard-icons), or any configured icon set based on the image name. Matched containers get their own group (default: "Auto Detected") so they don't clutter the "Unlabeled" section. Falls back to a generic placeholder when no specific match is found.
- **Dumbdock branding** — a built-in SVG icon (`/dumbdock.svg`) serves as favicon, nav bar logo, and auto-detection icon for dumbdock containers themselves.
- **Grouped card layout** — labeled containers appear in organized groups with responsive cards showing icon, name, description, link, and status.
- **Unlabeled container section** — containers without `dumbdock.*` labels appear in an expandable list that shows current labels, container info, and copy-paste-ready examples for labeling via docker-compose or `dumbdock.json`.
- **Config file overrides** — optionally define names, groups, icons, etc. in a JSON config file instead of (or in addition to) Docker labels.
- **Alert notifications** — get notified via [ntfy.sh](https://ntfy.sh) or [Gotify](https://gotify.net) when new unlabeled containers are detected, with configurable cooldown.
- **Password protection** — optional HTTP Basic Authentication via the `DUMBDOCK_PASSWORD` environment variable. When set, all dashboard and API access requires the password (any username accepted).
- **Network Warnings** — identifies containers with ports exposed on non-localhost IPs (▲) and surfaces [Traefik](https://traefik.io) proxy configuration (🔗) with clickable URLs extracted from `traefik.http.routers.*.rule` labels.
- **Tiny footprint** — multi-stage Docker build produces a ~10 MB static binary running from `scratch`.
- **Cache-aware HTTP headers** — serves the dashboard HTML with `ETag` and `Cache-Control: no-cache` headers, and API responses with `Cache-Control: no-cache`. The ETag is derived from a build-time version string (Git SHA + timestamp) injected via ldflags, enabling browsers to revalidate efficiently with `304 Not Modified`. Version defaults to `"dev"` for local builds; Docker builds automatically get a version tag.

## Quick Start

### Using Docker Compose (recommended)

```bash
# Copy the example compose file
cp docker-compose.yml.example docker-compose.yml

# (Optional) Edit docker-compose.yml to configure alerts or mount a config file

# Start
docker compose up -d
```

Then open **http://localhost:8080**.

### Using Docker directly

```bash
docker run -d \
  --name dumbdock \
  -p 127.0.0.1:8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -e POLL_INTERVAL=10s \
  ghcr.io/jordantrizz/dumbdock:latest
```

### From source

```bash
go build -o dumbdock .
./dumbdock
```

The embedded version (from `VERSION`) and an API endpoint (`/api/version`) let you check which build is running. For Docker builds a build number based on `git rev-list --count HEAD` is also injected — local builds default to build `"0"`.

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
| `containers` | object | `{}` | Per-container overrides keyed by container name. |
| `iconSets` | array | *see [Icons](#icons)* | Configure icon providers. Each entry defines an icon set name, index URL, CDN URL template, format, and optional mappings. When present, replaces the built-in defaults entirely. |

> **Precedence:** Config file overrides are applied first, then auto-detection. If a container has both an explicit config entry and a matching auto-detectable icon, the config entry wins.

Mount the config file:

```yaml
volumes:
  - ./dumbdock.json:/config/dumbdock.json:ro
```

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

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DOCKER_SOCK` | `/var/run/docker.sock` | Path to the Docker socket |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
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
| `DUMBDOCK_PASSWORD` | *(empty)* | Enables HTTP Basic Authentication on the dashboard and API. When set, any username is accepted but the password must match this value. Leave empty for no auth. |

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

## API

dumbdock exposes a single JSON API endpoint:

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

Each card object includes: `name`, `group`, `icon`, `href`, `description`, `order`, `containerId`, `containerName`, `image`, `status`, `state`, `ports`, `created`, `labels`, `hasLabels`, `hasPublicBinding`, `publicBindingIPs`, `traefikEnabled`, `traefikURLs`.

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
├── docker.go        # Docker API client (socket-based HTTP)
├── labels.go        # Label parsing and container card model
├── config.go        # JSON config file loading and override logic
├── alerts.go        # ntfy.sh and Gotify alert notifications
├── icons.go         # Icon set abstraction, index fetching, and resolution
├── icons.json       # Default image-name → icon-slug mappings for selfhst set
├── index.html       # Embedded single-page UI
├── Dockerfile       # Multi-stage build (golang → scratch)
├── docker-compose.yml.example
└── rebuild.sh       # Helper to rebuild and restart
```

## License

MIT
