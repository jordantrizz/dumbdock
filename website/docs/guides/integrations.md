---
title: Integrations
description: The Integrations tab — how dumbdock detects Traefik, Watchtower, and PruneMate, and how to force, pin, hide, or point an integration at an API.
---

The **Integrations** tab lists every container tool dumbdock can observe:
**Traefik**, **Watchtower**, and **PruneMate**. One card per integration shows
whether it was detected, which container it came from, the API endpoint and
where that endpoint came from, when it was last checked, its key figures, and any
coverage notes. Traefik keeps its own richer tab; the other tools are described
entirely by their card.

The tab is **read-only**. dumbdock reads these tools; it never triggers a
Watchtower update, never starts a PruneMate prune, and never runs commands inside
another container.

## What is detected

Detection matches the **image repository exactly**, never a substring. A
lookalike image such as `thomseddon/traefik-forward-auth` or a local
`mywatchtower` is never adopted.

| Integration | Image repository | What dumbdock reads |
|-------------|------------------|---------------------|
| `traefik` | `traefik` (including `library/traefik` on any registry) | The Traefik API: routers, services, middlewares, entrypoints, TLS certificates (see [Traefik dashboard](traefik.md)) |
| `watchtower` | `containrrr/watchtower` | `/v1/metrics`: scan counters, last-scan scanned/updated/failed, monitored containers |
| `prunemate` | `anoniemerd/prunemate` | `/api/stats`, falling back to `/stats`: prune runs, reclaimed space, last run |

Only running containers are considered. When several containers match, the
lowest container name wins, so the same instance is chosen on every poll. A
container started from a digest or an image ID still matches: dumbdock resolves
the ID to the repository tag, or to the name the container was created with,
before comparing.

## Overriding detection

Override the detection instead of changing an image. Three layers are consulted,
and the first one that sets a field wins:

| Layer | Example |
|-------|---------|
| Config file (`integrations.<id>` in `dumbdock.json`) | `"integrations": {"watchtower": {"enabled": true, "url": "http://watchtower:8080"}}` |
| Container label | `dumbdock.integration.prunemate.enabled=false` |
| Environment variable | `DUMBDOCK_INTEGRATION_WATCHTOWER_URL=http://watchtower:8080` |

The id is the integration name, `traefik`, `watchtower`, or `prunemate`. The
environment name upper-cases it.

| Field | Effect |
|-------|--------|
| `enabled` | `false` hides the integration completely: no polling, no findings, and a "Hidden by configuration" card. Use it when a matching container must not be monitored. |
| `container` | Pins one container by name and treats it as this integration **even when its image does not match** — a custom build, a renamed fork, or two instances on one host. A pin that matches nothing reports "Not detected"; it never falls back to another container. |
| `url` | Polls that endpoint with no container needed, for another host or a proxy. Only `http` and `https` are accepted; credentials inside the URL are rejected (use `token` or `username`/`password`), and link-local addresses are refused. A refused URL shows a warning on the card and is never called. |
| `token` | Bearer token for the API. Sent in the `Authorization` header only — never in a URL, a log line, a payload, or a finding. |
| `username` / `password` | Basic-auth credentials for the API, sent as a header. |

```yaml
services:
  watchtower:
    labels:
      - "dumbdock.integration.watchtower.container=custom-watchtower"
  prunemate:
    labels:
      - "dumbdock.integration.prunemate.enabled=false"
```

```json
{
  "integrations": {
    "watchtower": {"url": "http://watchtower:8080", "token": "your-token"},
    "prunemate": {"url": "http://prunemate:8080", "username": "admin", "password": "your-password"}
  }
}
```

Traefik keeps its original names as aliases, consulted only where the generic
layers leave a gap: the `dumbdock.traefik.api` label, `TRAEFIK_API_URL`,
`TRAEFIK_API_TOKEN`, `TRAEFIK_API_USER`, `TRAEFIK_API_PASS`, and `traefikAPIToken`
in the config file.

## Watchtower

Watchtower exposes a metrics endpoint, and that is the only surface dumbdock
reads:

- enable it with `--http-api-metrics` (or `WATCHTOWER_HTTP_API_METRICS=true`)
  together with `WATCHTOWER_HTTP_API_TOKEN`;
- the card shows scans in total, scans skipped, and the last scan's scanned,
  updated, and failed counts, plus how many containers Watchtower is watching;
- the metrics API is experimental and reports counters since Watchtower started,
  so there is no last-scan timestamp to display;
- monitored containers are counted from the
  `com.centurylinklabs.watchtower.enable=true` label, narrowed by
  `com.centurylinklabs.watchtower.scope` when a scope is set. Without label
  filtering the card reports "Running containers" rather than claiming they are
  watched;
- with `--http-api-update` but no metrics, the card explains that metrics are
  needed and raises no finding.

## PruneMate

PruneMate publishes `/api/stats` for dashboard widgets, so dumbdock needs no
credentials; a reverse proxy that adds auth can be handled with `username` and
`password`. The card shows completed prune runs, space reclaimed, the last run,
and the per-resource deletion counts. PruneMate's own schedule and prune options
live in its `/config` volume and are not visible to dumbdock.

A PruneMate that has never completed a prune, or whose last prune is older than
35 days, is reported as a warning.

## Findings and coverage

Two issue categories cover the framework:

| Category | Severity | Finding |
|----------|----------|---------|
| Integration API | `error` | Unreachable, unauthorized, or disabled after three failed attempts |
| Integration status | `warning` | A vendor-reported problem: failed Watchtower updates, or a PruneMate that has never pruned or has gone stale |

Traefik keeps its four dedicated categories, so existing acknowledgements stay
valid.

Each integration has its own three-attempt startup gate. While a gate is
disabled, the card names the reason and asks for a dumbdock restart, and exactly
one stable row appears in [Issues](issues.md). A disabled gate counts as complete
coverage, so acknowledgements in other categories keep pruning. A poll with no
detected container spends no attempt, so a tool that starts after dumbdock still
gets its full budget.

## Limits

- Read-only: dumbdock never triggers an update, a prune, or any state change in
  the tools it reads.
- Host paths are meaningless for a remote Docker daemon; the integration polling
  assumes the local socket.
- Watchtower's metrics are experimental and carry no timestamps; PruneMate's
  schedule and options are not exposed by its API.

## Related

- [Traefik dashboard](traefik.md) — the richer Traefik tab and its API setup.
- [Issues tab](issues.md) — what the finding rows mean.
- [API reference](../reference/api.md) — `GET /api/integrations`.
- [Environment variables](../reference/environment-variables.md),
  [config file](../reference/config-file.md), and
  [labels](../reference/labels.md) — the override layers.
