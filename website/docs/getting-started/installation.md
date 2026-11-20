---
title: Installation
description: Run dumbdock with Docker Compose, docker run, or straight from source, and understand the published port.
---

dumbdock is published as a container image at
`ghcr.io/jordantrizz/dumbdock` and builds from a single Go module.

## Docker Compose (recommended)

`control.sh` does the whole first run: it creates `docker-compose.yml` from the
example when it is missing, creates `.env` from `.env.example`, builds the image,
starts the stack, and waits for readiness.

```bash
./control.sh create
```

`create` is the first-run spelling of `setup`; both commands do the same work.
A split install keeps `.env` and the compose files in a separate install root
and symlinks `control.sh` at a checkout — see
[control.sh](../commands/control-sh.md#split-install).

To do it by hand instead:

```bash
cp docker-compose.yml.example docker-compose.yml
# Optional: edit docker-compose.yml to configure alerts or mount a config file
docker compose up -d
```

Then open `http://localhost:8080`.

### Optional: Cloudflare Tunnel (compose add-on)

The base stack has no tunnel. A named Cloudflare Tunnel ships as an optional
compose add-on in `docker-compose.override.yml.example`. Docker Compose merges
that file into `docker-compose.yml` automatically when it is named
`docker-compose.override.yml`, so no `-f` flag is needed:

```bash
# Either let control.sh create it on the first `tunnel named` run, or copy it:
cp docker-compose.override.yml.example docker-compose.override.yml
```

The tunnel stays off while you use `./control.sh start`: the shipped add-on is
profile-gated, and only `./control.sh tunnel named` starts it — after
`./control.sh tunnel setup` has written `TUNNEL_TOKEN` to `.env`. Compose never
overwrites an existing override file, so any additions you make there survive
upgrades. If your override exists but has no `cloudflared` service, `tunnel
named` stops with an instruction instead of editing your file.

See [control.sh](../commands/control-sh.md#tunnels) for the tunnel commands and
the variables they read.

### The published port

`DUMBDOCK_PORT` in `.env` selects the **published host port only**:

```yaml
ports:
  - "127.0.0.1:${DUMBDOCK_PORT:-8080}:8080"
```

The app always listens on port `8080` inside the container, so the mapping is
always `...:8080` on the right-hand side. Never pass `DUMBDOCK_PORT` into the
container environment — the app would then listen on a different port than the
one the host publishes, and the browser would get "connection refused". Use
`LISTEN_ADDR` to change the in-container listen address instead.

`control.sh`'s `ensure_free_dumbdock_port` bumps `DUMBDOCK_PORT` in `.env` to a
free host port when the configured one is taken; the container port stays fixed,
which is why that works.

## docker run

```bash
docker run -d \
  --name dumbdock \
  -p 127.0.0.1:8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /srv/containers:/srv/containers:ro \
  -e POLL_INTERVAL=10s \
  ghcr.io/jordantrizz/dumbdock:latest
```

The `/srv/containers` mount is optional, but the Storage tab needs it to verify
backup artifacts. Without it the tab still lists every database and its data
path, and marks each backup *unverified* rather than claiming it is missing.

## From source

```bash
go build -o dumbdock .
./dumbdock
```

The version is embedded from `VERSION` and exposed at `/api/version`. Docker
builds also inject a build number from `git rev-parse --short HEAD`; local builds
default to build `0`.

## After installing

- [Your first dashboard](./first-dashboard.md) — label one container.
- [Container healthcheck](./healthcheck.md) — what `healthy` means here.
- [Authentication](../guides/authentication.md) — set a password before exposing
  the port.
