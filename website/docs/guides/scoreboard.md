---
title: Dashboard scoreboard
description: The six clickable summary tiles above the legend and what each one counts.
---

The dashboard shows a clickable scoreboard directly above the legend,
summarising the current container set.

| Tile | Counts containers with |
|------|------------------------|
| **All** | every container; also the reset control |
| **Updates** | `updateAvailable`; a secondary line shows how many have an `unknown` update status |
| **Public Port Binding** | `hasPublicBinding` |
| **Private Port Binding** | `hasPrivateBinding` |
| **Localhost Port Binding** | `hasLocalBinding` |
| **Traefik Enabled** | `traefikEnabled` |

- Counts cover every container in the `/api/containers` response — labelled and
  unlabeled — deduplicated by container ID, and refresh on each 10-second poll.
- A container can be counted in more than one tile: it can have both a private
  binding and Traefik enabled.
- **Public** means a published port on a non-loopback, non-private address.
  Binding a container to `0.0.0.0` counts as public, as does an address dumbdock
  cannot parse. The same condition is reported as an **Exposed ports** finding on
  the Issues tab; the tile is only a count and a filter and never acknowledges
  anything.
- **All** is not a filter of its own. It is highlighted whenever no tile filter is
  active, and clicking it — or **Clear filter** — returns the dashboard to its
  normal grouped view.
- Clicking any other tile filters the dashboard to a flat grid of matching
  containers. Clicking the active tile again restores the grouped view.
- Tiles with a zero count are dimmed. The filter is not persisted across page
  reloads, and **All** is highlighted again after a reload.

## Related

- [Image update checks](./image-update-checks.md) — what the Updates tile counts.
- [Networking and network warnings](./networking.md) — the fields behind the
  port tiles.
- [Issues](./issues.md) — the findings the same port fields produce.
