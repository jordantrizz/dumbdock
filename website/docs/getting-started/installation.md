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
./control.sh setup
```

To do it by hand instead:

```bash
cp docker-compose.yml.example docker-compose.yml
# Optional: edit docker-compose.yml to configure alerts or mount a config file
docker compose up -d
```

Then open `http://localhost:8080`.

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
