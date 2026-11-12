---
title: Tab persistence
description: Every tab is addressable by URL fragment, so refresh, bookmarks and back/forward keep your place.
---

The active tab is reflected in the URL fragment, so a refresh (Ctrl+R) keeps you
on the same tab instead of returning to the Dashboard.

| Tab | URL |
|-----|-----|
| Dashboard | `/#dashboard` |
| Networking | `/#networking` |
| Traefik | `/#traefik` |
| Issues | `/#issues` |
| Storage | `/#storage` |
| Icons | `/#icons` |
| Help | `/#help` |

The navigation order is Dashboard, Networking, Traefik, Issues, Storage, Icons,
Help.

The **By Compose / By Dependency** grouping toggle and the **Copy JSON** control
exist only on the Dashboard. Switching tabs hides them without changing the saved
grouping mode.

## Bookmarks and history

Clicking a tab updates the fragment, which makes each tab bookmarkable and
shareable and lets the browser's back and forward buttons move between visited
tabs. On load, the tab named by the fragment is restored immediately — but its
data is fetched only after the auth check resolves, so no request is made before
login.

## In-page anchors

The Help tab's anchors (`#help-traefik-api`, `#help-traefik-verify`) still work:
an unrecognised fragment resolves to the tab containing the matching element, and
dumbdock scrolls to it once that tab is visible. Any other unrecognised fragment
falls back to the Dashboard.

## Related

- [Help tab](./help-tab.md) — the page whose anchors use this mechanism.
