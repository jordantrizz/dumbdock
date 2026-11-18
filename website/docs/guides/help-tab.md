---
title: Help tab
description: The always-visible Help tab — the in-app reference for every issue type, plus the static Traefik API, Watchtower, and integration-override guides.
---

The **Help** tab is always visible in the navigation bar, immediately after
**Icons**. It is static HTML served with the dashboard: there is no data fetch,
no background refresh, and no interpolation of container or host data, so the
page renders instantly and has no XSS surface.

## Issue reference

Every issue type the **Issues** page can show has one section here, addressed as
`#help-issue-<category>`. The tab opens with a table that names each type, its
severity, and a one-line meaning, then one section per type with four parts:

- **What it means** — the severity and what the finding represents.
- **Why it appears** — the source dumbdock reads and the trigger, taken from the
  collector rather than guessed.
- **How to resolve it** — the exact commands, labels, or settings when they
  exist, and the tab or page that shows more.
- **Exempt and out of scope** — the label or configuration that silences it, and
  what dumbdock deliberately does not report.

An overview section explains how the page works — severity, one row per finding,
the badge count, acknowledgement and retention, the 10-second poll, and what a
partial collection means.

## Linking from the Issues page

Each Issues row carries a small info control. It opens a popover with the issue
type's one-sentence summary and a **Read the full explanation** link, which
switches to the Help tab and scrolls to that type's section. The anchor alone
also resolves to the Help tab on load, refresh, and back/forward, because the
fragment is mapped to the tab that contains it.

## Guides

The tab currently carries three static guides:

- **Configure Traefik API access** — the two ways to enable the API and how to
  verify it.
- **Enable the Watchtower HTTP API** — the metrics flag, the token, and what the
  card can and cannot show.
- **Override integration detection** — the config, label, and environment layers
  for forcing, pinning, hiding, or re-pointing an integration.

## Configure Traefik API access

The guide after the issue reference covers:

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

## Adding an issue type or a guide

Adding an issue type is a two-part change: declare it in the issue catalog (label,
one-sentence summary, possible severities, and its `help-issue-<category>`
anchor), then add the matching section here in the same change. The section
carries the four parts above, a row in the summary table, and a contents entry;
a test fails when a category, its anchor, or its two links disagree. A new
standalone guide is a `.traefik-section` block plus a **Guides** contents entry —
no JavaScript or API change.

## Related

- [Issues tab](./issues.md) — what each row is and how acknowledgement works.
- [Traefik dashboard](./traefik.md) — the tab the API guide configures.
- [Tab persistence](./tab-persistence.md) — how in-page anchors survive a
  refresh.
