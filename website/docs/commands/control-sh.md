---
title: control.sh
description: The single operations script for the Compose stack — setup, lifecycle, status, logs, tunnels and reset.
---

`./control.sh` manages the Docker Compose stack. It auto-detects the layout, the
compose files, the main service and the build args, so it needs no configuration
file of its own. It is the generic, auto-detecting control script from the
`lmt-skills-docker-control-sh` skill plus one dumbdock-specific port hook.

## Commands

| Command | Description |
|---------|-------------|
| `./control.sh create [dev\|prod]` | First-time setup: `.env`, secrets, start the stack |
| `./control.sh setup [--force] [dev\|prod]` | Create or refresh `.env`, start the stack |
| `./control.sh start` | Start the stack |
| `./control.sh stop` | Stop and remove containers |
| `./control.sh restart` | Restart containers without a rebuild |
| `./control.sh rebuild` | `git pull`, rebuild images, recreate containers |
| `./control.sh status` | Show container and version status |
| `./control.sh logs [service]` | Tail logs, all services by default |
| `./control.sh tunnel quick` | Expose the stack with a temporary `trycloudflare.com` URL |
| `./control.sh tunnel setup` | Provision a named Cloudflare Tunnel once via the API |
| `./control.sh tunnel named` | Run the provisioned named tunnel |
| `./control.sh tunnel url` | Print the current tunnel URL |
| `./control.sh tunnel status` | Show tunnel process or container state |
| `./control.sh tunnel stop` | Stop this project's tunnel |
| `./control.sh reset [--yes]` | **Destructive:** wipe volumes and data, then run a fresh setup |
| `./control.sh help` | Show help |

`create` and `setup` do the same work; `create` reads as the first run.
`setup` creates `docker-compose.yml` from the example when the real file is
missing, and creates `.env` from `.env.example`, filling empty secret-looking
keys (`DUMBDOCK_PASSWORD`, `DUMBDOCK_BACKUP_TOKEN`, `GOTIFY_TOKEN`, and so on)
with generated values. `rebuild` replaces the former `rebuild.sh`, which has
been removed.

## Layouts

The script supports two layouts and detects which one it is in from how it was
invoked.

### Single directory

The checkout is the install root. Code, `.env`, compose files and the Dockerfile
sit together:

```bash
git clone https://github.com/jordantrizz/dumbdock.git dumbdock
cd dumbdock
./control.sh create
```

### Split install

Configuration and runtime state live in an install root, and the application
checkout sits beside it. The checkout can be re-cloned without losing `.env`,
the compose files or the Docker volume:

```bash
mkdir -p /opt/containers/dumbdock
cd /opt/containers/dumbdock
git clone https://github.com/jordantrizz/dumbdock.git dumbdock-git
ln -s dumbdock-git/control.sh control.sh
./control.sh create
```

```text
/opt/containers/dumbdock/
├── .env                           # hand-crafted configuration and secrets
├── .gitignore                     # excludes dumbdock-git/ and runtime state
├── docker-compose.yml             # local file or a symlink into the checkout
├── docker-compose.override.yml    # per-host overrides (never replaced)
├── control.sh -> dumbdock-git/control.sh
└── dumbdock-git/                  # application checkout — untracked here
```

In this layout Compose runs from the install root, while the Dockerfile, source
and build inputs come from the checkout through
`build.context: ${CONTROL_CODE_DIR:-.}`. The script sets `CONTROL_CODE_DIR` in
the install root's `.env`, adds one anchored `/<checkout>/` rule to its
`.gitignore`, and never records the checkout as a gitlink. A local compose file
or override is host-owned and is never replaced; keep host-specific changes in
`docker-compose.override.yml`. `./control.sh tunnel named` creates that override
from `docker-compose.override.yml.example` the first time it runs and leaves an
existing file untouched.

## Tunnels

`tunnel quick` needs only the host `cloudflared` binary and no Cloudflare
account. It writes the temporary URL to `.tunnel-url`.

`tunnel setup` provisions a named tunnel from the Cloudflare API and writes
`TUNNEL_TOKEN` to `.env`; `tunnel named` then creates
`docker-compose.override.yml` from the add-on example if it is missing and
starts the profile-gated `cloudflared` service. It never overwrites an override
you already have. The required values are documented in `.env.example`:

| Variable | Purpose |
|----------|---------|
| `CF_API_TOKEN` | API token with Tunnel Edit, DNS Edit and Zone Read |
| `CF_ACCOUNT_ID` | Cloudflare account id |
| `CF_ZONE_ID` | Zone id that holds the hostname |
| `CF_TUNNEL_HOSTNAME` | Public hostname (required) |
| `CF_TUNNEL_NAME` | Tunnel name; defaults to the install root's name |
| `CF_TUNNEL_ORIGIN` | Ingress origin; defaults to `http://dumbdock:8080` |

The origin is the in-network Compose service name, not a host-side `localhost`
port, because the tunnel runs as a sibling container.

## Ports

`DUMBDOCK_PORT` selects the **published host port only**; the app always listens
on `8080` inside the container. `control.sh` bumps `DUMBDOCK_PORT` in `.env` when
the configured host port is taken, which only works because the container port
stays fixed.

## Related

- [Installation](../getting-started/installation.md) — the first run.
- [Container healthcheck](../getting-started/healthcheck.md) — what `status`
  reports.
