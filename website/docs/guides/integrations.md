---
title: Integrations
description: The Integrations tab — how dumbdock detects Traefik, Watchtower, and PruneMate, how to force, pin, hide, or point an integration at an API, and how to fix an endpoint dumbdock cannot reach.
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

## Troubleshooting

### An endpoint that answers on the host but not in dumbdock

Each Compose project gets its own bridge network, and Docker drops traffic between
them. A Watchtower or PruneMate container in its own project is therefore
unreachable from a dumbdock container in another one — even on the same host, and
even when `curl` from the host shell returns data normally. The host has a leg on
every bridge; a container only has its own. A host-side `curl` proves the tool is
healthy, not that dumbdock can reach it.

Without a `url` override, dumbdock builds the endpoint from the container itself:

1. the container's IP on its **first Docker network**, sorted by network name, on
   the vendor port (`8080` for Watchtower and PruneMate);
2. if the container has no network IP at all, the first published port on
   `127.0.0.1`.

The first step never checks that dumbdock is on that network, and the second is
only correct when dumbdock runs with `network_mode: host` — inside a container,
`127.0.0.1` is dumbdock itself. The card shows the endpoint it resolved with the
source `auto`, and the startup log echoes the same:

```text
integration prunemate: auto (url (not set), token (not set), password (not set))
```

### Reading the message

| Message | Meaning | Fix |
|---|---|---|
| `context deadline exceeded (Client.Timeout exceeded while awaiting headers)` | No shared network: the packets are dropped and the 8 s timeout runs out | Join a shared network and set `url` |
| `dial tcp: lookup <name> … no such host` | A container name was used without a shared network | Same as above |
| `connection refused` | The address answers, but nothing listens on that port | Check the port, and that the tool binds `0.0.0.0` inside its container |
| `returned status 401` or `403` | Reachable; credentials missing or wrong | Set `token`, or `username` and `password` |

A timeout and a refusal are different faults. A refusal means the network is fine
and the port or the binding is wrong; a timeout means the network is not fine.

### Joining one network, and pinning the URL

Both containers must be on the same user-defined network, and the endpoint should
be set explicitly. Give dumbdock a shared network and the override — any network
both projects join works, often the one your reverse proxy already uses:

```yaml
services:
  dumbdock:
    networks:
      - proxy
    environment:
      - DUMBDOCK_INTEGRATION_PRUNEMATE_URL=http://prunemate:8080

networks:
  proxy:
    external: true
```

Then attach the tool to that same network. Keeping its own project network is
harmless once the override is set:

```yaml
services:
  prunemate:
    networks:
      - default
      - proxy

networks:
  proxy:
    external: true
```

Docker's embedded DNS resolves `prunemate` by container name only on a shared
network, which is why the name is used instead of an IP that changes on every
recreate.

**Set `url` even after joining.** The automatic endpoint still takes the first
network name alphabetically, so a tool that keeps its own project network can
still be addressed on the bridge dumbdock cannot use. Dropping the tool's own
network is the alternative; an explicit `url` survives either layout.

### The restart comes last

The three-attempt gate is process state. After the third failed attempt the
integration is disabled for the life of the process and no configuration change
revives it — and a restart made before the network is fixed spends the budget
again within about a minute (an 8 s timeout, then the 30 s minimum between polls).
So, in this order:

1. make the network change and recreate the tool;
2. prove the name resolves **from inside dumbdock**;
3. recreate dumbdock last.

The runtime image is Alpine, so `wget` and `nc` are available in it:

```bash
docker exec dumbdock nc -z -w 3 prunemate 8080; echo "nc exit=$?"
docker exec dumbdock wget -T 3 -qO- http://prunemate:8080/api/stats
```

Then ask dumbdock what it resolved instead of guessing (the port is your
`DUMBDOCK_PORT`):

```bash
curl -s "http://127.0.0.1:8080/api/integrations/prunemate" \
  | jq '{url, urlSource, disabled, error}'
```

A healthy card reports the endpoint with `urlSource` `config`, `label`, or `env`,
`disabled` false, and no `error`. The finding row disappears on the first
successful poll, and its acknowledgement is pruned with it because collection is
complete again.

### When only the host can reach the tool

If the two projects must not share a network, publish the port on an address a
container can route to and use the host-gateway alias. A `127.0.0.1` publish does
not work: only the host's own loopback answers it.

```yaml
services:
  dumbdock:
    extra_hosts:
      - "host.docker.internal:host-gateway"
    environment:
      - DUMBDOCK_INTEGRATION_PRUNEMATE_URL=http://host.docker.internal:8080
```

The published form must be `8080:8080` or `<bridge gateway>:8080:8080`, not a
`127.0.0.1` binding. This is the fallback route: it adds a host round-trip and
depends on the published port, both of which the shared network avoids.

Traefik resolves its own API URL and probes fallback candidates; see
[Traefik dashboard](traefik.md#boot-attempt-gate) for that chain.

## Limits

- A tool in another Compose project is unreachable until both containers share a
  Docker network and `url` is set; auto-detection never crosses a bridge. See
  [Troubleshooting](#troubleshooting).
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
