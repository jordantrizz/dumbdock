---
title: Traefik dashboard
description: What the Traefik tab shows, how dumbdock resolves the API URL, and how to enable and authenticate the API.
---

The **Traefik** tab is always visible in the navigation bar. When a running
Traefik container is detected (an image name containing "traefik"), the tab
shows a full status dashboard fetched from the Traefik API. With no Traefik
container it shows install guidance including a `docker run` example; with a
container detected but the API unreachable it shows the general error and the
resolved API URL.

dumbdock requires **Traefik v3**. Other major versions display an "Unsupported
Traefik version" error rather than a dashboard.

## Auto-discovery

dumbdock resolves the API URL through a fallback chain:

1. **`dumbdock.traefik.api` label** — if the Traefik container carries it, the
   value is used as-is.
2. **`TRAEFIK_API_URL` environment variable** — set on the dumbdock container,
   not on Traefik.
3. **Docker network IP** — the container's IP on its first Docker network, port
   `:8080`. A custom entrypoint port
   (`--entrypoints.traefik.address=:PORT`) is used instead.
4. **Published port** — the first published TCP port on `127.0.0.1`, preferring
   `8080`.

**Fallback probing:** when every endpoint fails at the primary URL, dumbdock
probes candidates in order — published `8080/tcp`, the container IP on `8081`
(Traefik's default insecure API entrypoint), then any other published TCP port —
and adopts the first that answers `/api/version`. Probing uses a 2 s timeout,
sends no credentials, and runs at most once per poll.

## Data displayed

Once the URL resolves, dumbdock fetches these endpoints concurrently:

| Endpoint | Data shown |
|----------|------------|
| `/api/version` | Version, codename, start date, Go version |
| `/api/overview` | Counts of HTTP/TCP routers, services, middlewares |
| `/api/entrypoints` | Entrypoint names and addresses |
| `/api/http/routers` | Name, status, rule, service, entrypoints, middlewares, TLS |
| `/api/http/services` | Name, status, type, per-server health (UP/DOWN) |
| `/api/http/middlewares` | Name, type, status |
| `/api/tcp/routers` | Name, status, rule, service, entrypoints, TLS |
| `/api/tcp/services` | Name, status, type, per-server health |
| `/api/tls/certificates` | Name, domains, subject, expiry, stores |

Endpoint errors are reported gracefully. A 404 — for example no TLS certificates
configured — renders as "Not configured" and is listed under `notConfigured` in
`GET /api/traefik` rather than `endpointErrors`, so it never triggers the partial
error banner or the unreachable state.

dumbdock tolerates both Traefik v2 and v3 API shapes: `/api/overview` counts are
accepted as bare integers (v2) or as objects such as
`{"total": N, "warnings": 0, "errors": 0}` (v3).

## Enable the API

dumbdock reads Traefik's read-only API, which must be enabled and reachable. The
usual cause of `connection refused` in the logs is an API that is not exposed:

- **Insecure entrypoint** — start Traefik with `--api.insecure=true` so the
  dashboard and API are served on the `traefik` entrypoint (port `8080`).
- **Secured API** — expose the `api@internal` service through a router,
  optionally with auth, and point dumbdock at it with `TRAEFIK_API_URL` or the
  `dumbdock.traefik.api` label.

When the API is entirely unreachable, dumbdock logs the failure once per
error-state change (not on every poll), logs a single `traefik: API recovered`
line when it returns, and the tab shows a "container detected but API is not
accessible" banner with the resolved URL and the underlying error.

## API authentication

Two modes are supported:

- **Bearer token** — `TRAEFIK_API_TOKEN` (environment) or `traefikAPIToken`
  (config file).
- **Basic auth** — `TRAEFIK_API_USER` and `TRAEFIK_API_PASS`.

The auth header is added server-side only; credentials never reach the browser.

## Related

- [Issues tab](./issues.md) — Traefik API and entity failures become findings.
- [Help tab](./help-tab.md) — the in-page guide for issuing the API.
