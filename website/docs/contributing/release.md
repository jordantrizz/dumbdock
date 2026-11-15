---
title: Releases
description: VERSION is the single source of the semantic version, tags assign it, and the build number comes from git.
---

## Version sources

Three values appear in a release:

| Value | Source |
|-------|--------|
| Semantic version | the `VERSION` file at the repo root, embedded at compile time |
| Build number | `git rev-parse --short HEAD`, injected with `-X main.buildNumber=…` |
| Build stamp | a separate `version` ldflag (git SHA plus timestamp), used for the ETag |

Local `go build` defaults the build number to `0`. Docker builds inject the short
SHA. The long-lived `version` value is independent of the semantic version and
exists only for cache busting.

## Cutting a release

1. Land the work on `main` with passing checks.
2. Squash and tag with the release helper, which formats the folded commit list
   and takes the version from the last reachable tag:

   ```bash
   scripts/git-squash-release.sh --bump minor --yes
   ```

3. Confirm the tagged build reports itself:

   ```bash
   curl -s http://127.0.0.1:8080/api/version
   ```

The version is assigned by the release, never by a feature branch — a plan or a
phase never writes `VERSION`. The startup log line and the dashboard footer both
show `v<semver> (build <sha>)`.

## Expected output

`GET /api/version` returns JSON such as:

```json
{"version":"0.0.6","build":"4c1d641"}
```

## Related

- [Testing](./testing.md) — the checks before a tag.
- [Development](./development.md) — building with ldflags.
