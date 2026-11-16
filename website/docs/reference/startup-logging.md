---
title: Startup logging
description: The redacted startup config block, advisory validation warnings, and which values are never printed.
---

On startup dumbdock validates its effective configuration and logs a redacted
summary of what it will use.

Validation is **advisory**: an invalid setting produces a
`warning: config: <field>: …` line and the process continues with defaults, so a
typo never takes the dashboard down. A missing or unparseable `dumbdock.json` is
handled the same way — the server falls back to an empty config and keeps running.

## What the block contains

The `startup config:` block lists the version and build, config-file path and
status, listen address, Docker socket, poll interval, auth mode, update-check
settings, auto-detection settings, blacklists, icon sets, alert targets, state
directory, storage roots, Traefik API settings, and one line per integration.

Secret-bearing values are never printed. They render as `(set)` / `(not set)`:

| Redacted value | Source |
|----------------|--------|
| Auth password | `DUMBDOCK_PASSWORD` |
| Gotify token | `GOTIFY_TOKEN` |
| Traefik API token | `TRAEFIK_API_TOKEN` or `traefikAPIToken` |
| Traefik API pass | `TRAEFIK_API_PASS` |
| Backup report token | `DUMBDOCK_BACKUP_TOKEN` |
| Integration token or password | the `integrations` block, a label, or `DUMBDOCK_INTEGRATION_<ID>_TOKEN` / `_PASS` |

The one deliberate exception is the random password generated for the run when an
auth mode is active and `DUMBDOCK_PASSWORD` is empty. It is logged once so the
operator can sign in, is held in memory only, and changes on every restart.

## Example

```text
dumbdock v0.0.6 (build 4c1d641)
startup config:
  version: 0.0.6 (build 4c1d641)
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
  integrations: prunemate, traefik, watchtower
  integration prunemate: disabled (url (not set), token (not set), password (not set))
  integration traefik: auto (url (not set), token (not set), password (not set))
  integration watchtower: forced (url http://watchtower:8080, token (set), password (not set))
```

Each integration line reports its mode — `auto`, `forced` when an override
supplies the endpoint, or `disabled` — plus its endpoint and the *presence* of a
token or password.

## Related

- [Environment variables](./environment-variables.md) — every setting the block
  reports.
- [Config file](./config-file.md) — the file whose load status is printed.
- [Integrations](../guides/integrations.md) — what `auto`, `forced`, and
  `disabled` mean.
