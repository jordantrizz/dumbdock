---
title: Testing
description: The Go suites, the race run, the shell and docs checks, and what each one proves.
---

## Go tests

```bash
go test ./...
go vet ./...
```

Every package with logic has a `*_test.go` beside it: issue collectors and
persistence, the storage scanner and evidence providers, registry parsing and the
update cache, Traefik payload handling, config and startup validation, networking,
and the label parser.

The race detector needs a C toolchain, which the Alpine Go image does not carry by
default:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine \
  sh -c 'apk add --no-cache gcc musl-dev && go test -race ./...'
```

## Shell

```bash
bash -n control.sh
bash -n scripts/backup-databases.sh
```

These parse the scripts without running them. Neither touches Docker.

## Documentation

```bash
cd website && npm ci && npm run build
bash scripts/check-docs-site.sh --site website --repo-root .
```

The build fails on a broken link, a broken anchor, or a broken markdown link,
because all three are configured to `throw`. The gate then asserts the rest of the
docs contract: pinned and locked dependencies, front matter on every page, one
resolving sidebar, relative links, the README budget, balanced generated markers,
and a Pages workflow that can authenticate.

## Test accounting

Planned work tracks which checks ran in `plans/tests.md`, with `pgf test run`,
`pgf test pending` and `pgf test close`. A check that never ran is recorded as
skipped with a reason rather than silently dropped.

## Related

- [Development](./development.md) — build and layout.
- [Releases](./release.md) — what to run before tagging.
