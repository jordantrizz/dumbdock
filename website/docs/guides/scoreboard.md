---
title: Dashboard scoreboard
description: The four clickable summary tiles above the legend and what each one counts.
---

The dashboard shows a clickable scoreboard directly above the legend,
summarising the current container set.

| Tile | Counts containers with |
|------|------------------------|
| **Updates** | `updateAvailable`; a secondary line shows how many have an `unknown` update status |
| **Private Port Binding** | `hasPrivateBinding` |
| **Localhost Port Binding** | `hasLocalBinding` |
| **Traefik Enabled** | `traefikEnabled` |

- Counts cover every container in the `/api/containers` response — labelled and
  unlabeled — deduplicated by container ID, and refresh on each 10-second poll.
- A container can be counted in more than one tile: it can have both a private
  binding and Traefik enabled.
- Clicking a tile filters the dashboard to a flat grid of matching containers.
  Clicking the active tile again, or using **Clear filter**, restores the normal
  grouped view.
- Tiles with a zero count are dimmed. The filter is not persisted across page
  reloads.

## Related

- [Image update checks](./image-update-checks.md) — what the Updates tile counts.
- [Networking and network warnings](./networking.md) — the fields behind the
  port tiles.
