---
title: Development
description: Build dumbdock, run it against a local Docker socket, and find your way around the source tree.
---

dumbdock is a single Go module. Go itself is not required on the host if you
build in the official image.

## Build and run

```bash
go build -o dumbdock .
./dumbdock
```

Running locally needs access to the Docker socket. To rebuild and restart the
Compose stack instead, use `./control.sh rebuild`.

If Go is not installed, build inside the pinned image:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.22-alpine sh -c 'go build ./... && go vet ./... && go test ./...'
```

## Project structure

```text
.
├── main.go            # entry point, HTTP server, polling loop
├── auth.go            # auth modes, session store, login/logout
├── config.go          # JSON config loading and override logic
├── docker.go          # Docker API client over the socket
├── labels.go          # label parsing and the container card model
├── networking.go      # networks and ports per container
├── warnings.go        # port-binding and Traefik label parsing
├── icons.go           # icon sets, index fetching, resolution
├── icons.json         # default image-name to icon-slug mappings
├── alerts.go          # ntfy.sh and Gotify notifications
├── updates.go         # image update checks and registry clients
├── docker_version.go  # cached latest Moby release lookup
├── traefik.go         # Traefik detection, inspect, API client, boot gate
├── issues.go          # issue model, collectors, acknowledgement store
├── storage.go         # storage conventions, mounts, datasets
├── storage_evidence.go# filesystem, backup-container and report evidence
├── startup.go         # config validation and the startup log block
├── index.html         # embedded single-page UI
├── control.sh         # Compose stack operations
├── scripts/           # reference backup job, docs gate
├── website/           # this documentation site (Docusaurus)
├── Dockerfile         # multi-stage build: golang to alpine:3.20
└── docker-compose.yml.example
```

Tests sit beside their subject as `*_test.go`. `VERSION` is the single source of
the semantic version; `AGENTS.md` records the engineering contracts per feature.

## Documentation site

The site you are reading lives in `website/`, with its own `package.json` and
lockfile so `go build` and `npm ci` never collide:

```bash
cd website
npm ci
npm run build     # static output in build/
npm run start     # local preview
npm run gen:docs  # regenerate the changelog page and the README index table
```

Every push to `main` that touches `website/`, `README.md` or `security.md`
deploys through `.github/workflows/docs.yml`. **One repository setting is
manual:** Settings → Pages → Source must be **GitHub Actions**, otherwise
`actions/deploy-pages` fails with "Get Pages site failed".

Before opening a docs change, run the publishable gate — its exit status is the
verdict:

```bash
bash scripts/check-docs-site.sh --site website --repo-root .
```

The contributor contract — nav order, one explicit sidebar, required front
matter, generated markers, the README budget, and the gate — is recorded in
[`AGENTS.md`](https://github.com/jordantrizz/dumbdock/blob/main/AGENTS.md).

## Constraints worth knowing

- Builds are expensive on a small host. Prefer a single foreground build over
  parallel ones, and never build while another job is running.
- The dashboard never mutates the host: read the Docker and Traefik APIs, and
  write only dumbdock's own state files.

## Related

- [Testing](./testing.md) — the suites and checks to run.
- [Releases](./release.md) — versioning and tags.
