---
title: What dumbdock is
description: A single binary that reads the Docker socket and renders a label-driven dashboard, plus Networking, Traefik, Issues and Storage views.
---

dumbdock is a single Go binary (plus one embedded HTML page) that reads the
Docker socket and renders a clean overview of your running containers. Nothing
is stored in a database and nothing is injected into your containers: the
dashboard is driven entirely by Docker labels, an optional JSON config file, and
the Docker and Traefik APIs.

## What it shows

- **A labelled container grid** — `dumbdock.*` labels set a card's name, group,
  icon, link, description and sort order. Unlabelled containers land in an
  expandable "Unlabeled" section with copy-paste-ready label examples.
- **Compose and dependency grouping** — the dashboard toggle cycles by label
  group, by Compose project, or by `depends_on` dependency.
- **Automatic icons** — image names are resolved against the
  [selfhst/icons](https://github.com/selfhst/icons) and
  [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) sets, with a
  generic placeholder when nothing matches.
- **Networking** — every container's networks, in-network IP and open ports,
  with warnings for ports published on `0.0.0.0`.
- **Traefik** — a status dashboard for routers, services, middlewares,
  entrypoints and TLS certificates, read from Traefik v3's API.
- **Issues** — one row per provable problem: public port bindings, image
  updates, unhealthy containers, Traefik failures, an outdated Docker Engine,
  misplaced data, and stale or missing backups. Findings can be acknowledged
  until they resolve.
- **Storage and backups** — per project and per database: where the compose file
  lives, where the data lives, and how fresh the last verified backup is.
- **Alerts** — a notification through ntfy.sh or Gotify when a new unlabelled
  container appears.
- **Authentication** — `none`, `http-auth` (Basic) or `web-auth` (login form,
  the default), with an 8-hour session or 30-day "remember me".

## Read-only by design

dumbdock reads the Docker and Traefik APIs and reports what it can prove. It
never pulls an image, restarts a container, changes a port binding, upgrades
Docker, edits Traefik, reads a container log, or opens a database dump.

The one thing it writes is its own state file: acknowledging an issue (or
receiving a backup report) updates `issues-acknowledged.json` or
`backup-reports.json` in `DUMBDOCK_STATE_DIR` — never the host.

## Next

- [Install dumbdock](./installation.md) — Compose, `docker run`, or from source.
- [Your first dashboard](./first-dashboard.md) — label one container end to end.
- [Traefik dashboard](../guides/traefik.md) — what the Traefik tab needs.
- [Issues tab](../guides/issues.md) — what counts as a finding.
