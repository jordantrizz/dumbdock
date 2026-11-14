---
title: Issues tab
description: Every problem dumbdock can prove, what is deliberately not a finding, and how acknowledging works.
---

The **Issues** tab sits between **Traefik** and **Storage** and lists every
actionable problem dumbdock can prove from the Docker and Traefik APIs. The
navigation button carries a count badge for unacknowledged findings, refreshed on
every poll after authentication.

## What is checked

| Category | Severity | Finding |
|----------|----------|---------|
| Container health | `error` | Docker reports `health=unhealthy` (read from the daemon's health filter, never inferred from the status text) |
| Port exposure | `warning` | A published port is bound to a non-localhost address such as `0.0.0.0` or a public IP |
| Image updates | `warning` | The background registry check found a newer digest for the running tag |
| Traefik API | `error` / `warning` | Every Traefik endpoint failed, or one endpoint returned an error |
| Traefik version | `warning` | The detected major version is not 3 |
| Traefik entities | `error` / `warning` | A router, service or middleware is `error`; or `disabled` / `warning` |
| Traefik backends | `error` | A load-balancer server reports `DOWN` |
| Docker Engine | `warning` | The daemon is older than the latest stable Moby release (advisory) |
| Storage paths | `warning` / `error` | Compose file outside the root, data beside the compose file, data in a Docker volume (warning); database data in the container layer (error) |
| Database backups | `warning` / `error` | Newest verified backup older than policy (warning); no backup although required (error) |

## What is deliberately not a finding

- Containers hidden by `CONTAINER_BLACKLIST` or `containerBlacklist`.
- Stopped, paused, exited and restarting containers — only currently listed
  containers are evaluated.
- Containers without a healthcheck, and containers whose health is `starting`.
- Image update states `unknown` and `error` (shown on cards, not as issues).
- Ports bound to `127.0.0.1`, `::1`, or a private address.
- A missing Traefik container (that is install guidance on the Traefik tab).
- Traefik endpoints that return 404 or "not configured", such as no TLS
  certificates.
- Raw container logs — dumbdock does not read log output for this feature.
- A dataset whose backup is `unverified` because no evidence provider is
  configured, and any dataset waived with `dumbdock.backup.disabled=true`.
- Config-file mounts (matched by extension, or a destination under `/etc/`) and
  mounts waived with `dumbdock.storage.ignore`.

## Counts and ordering

One row is one finding, and the badge equals the number of **active**
(unacknowledged) rows. A container can produce more than one row: an unhealthy
container whose image also has an update is two findings. Rows are ordered by
severity (`error` first), then category, then title.

## Acknowledging a finding

Each active row has an **Acknowledge** button. Acknowledging removes the finding
from the count and moves it to the **Acknowledged** section, where
**Unacknowledge** restores it.

- Acknowledgements are stored server-side, so every browser sees the same state,
  and they survive a restart as long as the `/data` volume is kept.
- An acknowledgement is dropped once a **complete** collection confirms the
  finding is gone. The next recurrence is active again.
- If a collection pass is incomplete — the Docker health list is unavailable, or
  a Traefik endpoint failed — dumbdock keeps existing acknowledgements and labels
  the page **partial** rather than assuming the finding resolved.

## Docker Engine version comparison

dumbdock reads the daemon version from `GET /version` and, at most once every 24
hours, the latest stable `moby/moby` GitHub release. An older daemon produces a
warning.

The comparison is advisory: distribution packages and managed container hosts
may intentionally pin an older Docker Engine, so verify before upgrading. When
the comparison is unavailable it is reported as coverage metadata, never as a
finding.

## Coverage and refresh

The snapshot is rebuilt in the background poll; the page reads a cached snapshot
from `/api/issues`, so it never blocks on Docker or GitHub. The badge refreshes
every 10 seconds, and the page re-renders only while it is the visible tab. The
**Collection** line and any warning banner state whether all inputs were checked.

> **Read-only:** the Issues tab never pulls images, restarts containers, changes
> port bindings, upgrades Docker, or edits Traefik. Acknowledging a finding
> changes only dumbdock's own state file.

## Related

- [Storage tab](./storage.md) — the storage and backup findings in detail.
- [Image update checks](./image-update-checks.md) — how the update badge is
  decided.
- [Traefik dashboard](./traefik.md) — the API the findings come from.
