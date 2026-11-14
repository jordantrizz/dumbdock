---
title: Image update checks
description: How dumbdock detects a tag whose digest moved upstream, which registries it supports, and the caching rules.
---

dumbdock can flag containers whose running image tag has moved upstream. A
background checker resolves the current digest of each image tag from the registry
and compares it with the digest of the locally pulled image; a mismatch means a
newer image is available.

- **Badges** — an outdated container shows `⬆ update available` in its warnings
  row. Images that cannot be checked show a neutral `? update unknown`. The
  expanded card detail lists the local (`currentDigest`) and registry
  (`latestDigest`) digests.
- **Supported registries** — Docker Hub (`docker.io`, including bare `library/*`
  names) and GitHub Container Registry (`ghcr.io`), queried anonymously. Images
  from any other registry, and locally built images with no `RepoDigests`, are
  reported as `unknown`.
- **Pinned images** — references pinned by digest (`nginx@sha256:…`) are
  intentionally fixed and are not checked.
- **Caching** — each `repo:tag` result is cached for 6 hours
  (`DUMBDOCK_UPDATE_INTERVAL`). Checks run in the background with at most 4
  concurrent requests and never block the dashboard poll. A failed check retries
  after 15 minutes.
- **Caveats** — only a tag that has *moved* is detected, so a pinned tag such as
  `nginx:1.24` reads as up to date even when `nginx:1.25` exists upstream.
  Private registries are unsupported (no credentials are ever sent) and Docker
  Hub's anonymous rate limit applies.

Disable the feature with `DUMBDOCK_UPDATE_CHECK=false` or `"updateCheck": false`
in `dumbdock.json`.

Containers with an available update also appear on the
[Issues tab](./issues.md); `unknown` and `error` statuses are deliberately not
issues.

## Related

- [Networking and network warnings](./networking.md) — the card fields.
- [Dashboard scoreboard](./scoreboard.md) — the Updates tile.
