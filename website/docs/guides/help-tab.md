---
title: Help tab
description: The always-visible Help tab and its static Traefik API guide, including how to add a future guide.
---

The **Help** tab is always visible in the navigation bar, immediately after
**Icons**. It contains static getting-started guides served as plain HTML — there
is no data fetch and no background refresh, so the page renders instantly.

## Configure Traefik API access

The first guide covers:

- **Option 1 — insecure API.** Start Traefik with `--api.insecure=true` so the
  API and dashboard are served on the `traefik` entrypoint (default port `8080`),
  with `docker run` and Compose snippets and a security note about keeping that
  port on a private network. Publishing `8080` is optional: on a shared Docker
  network dumbdock reaches the API at the container's IP. The guide shows how to
  test it with `docker exec` plus `wget`/`curl`, or a throwaway
  `curlimages/curl` container on the same network.
- **Option 2 — secured API.** Expose the `api@internal` service through a router,
  optionally with a basic-auth middleware, and point dumbdock at it with
  `TRAEFIK_API_URL` or the `dumbdock.traefik.api` label.
- **Authentication.** `TRAEFIK_API_TOKEN` for a bearer token, or
  `TRAEFIK_API_USER` plus `TRAEFIK_API_PASS` for basic auth. Both are applied
  server-side.
- **Verification.** `curl -s http://<traefik-host>:8080/api/version` and the JSON
  it should return.
- **Troubleshooting.** The `connection refused` signature, the "container
  detected but API is not accessible" banner, wrong-URL fixes, 401/403 auth
  failures, and ordinary partial-data cases.

## Adding a guide

Append a new `.traefik-section` block inside `#page-help` in `index.html` and add
an entry to the guide table of contents. No JavaScript or API change is needed.

## Related

- [Traefik dashboard](./traefik.md) — the tab this guide configures.
- [Tab persistence](./tab-persistence.md) — how in-page anchors survive a
  refresh.
