---
title: Container healthcheck
description: How the Compose healthcheck proves dumbdock is listening, and why it is a raw TCP connect rather than an HTTP request.
---

The `dumbdock` service defines a Compose `healthcheck` that runs inside the
container and checks that the app is listening on its port with a raw TCP
connect:

```yaml
healthcheck:
  test: ["CMD", "nc", "-z", "-w", "2", "127.0.0.1", "8080"]
  interval: 30s
  timeout: 5s
  retries: 3
  start_period: 10s
```

## Why it is built this way

- The probe targets `127.0.0.1:8080` — the fixed in-container listen port. The
  published host port set by `DUMBDOCK_PORT` is irrelevant inside the container,
  and a loopback address avoids any DNS lookup.
- It is a pure **TCP port check** (`nc -z`): it never issues an HTTP request, so
  it is independent of every route, its status code, and the auth mode. The
  healthcheck works the same with `DUMBDOCK_AUTH_MODE=none`, `http-auth`, and
  `web-auth`.
- The `nc` probe needs a real userland, which is why the runtime image is built
  `FROM alpine:3.20` rather than `scratch`. The image also installs CA
  certificates, which the registry update checks need.
- The healthcheck is Compose-only; there is no `HEALTHCHECK` in the
  `Dockerfile`.

## Check the status

```bash
docker compose ps
docker inspect --format '{{.State.Health.Status}}' dumbdock
```

`docker compose ps` shows `healthy` or `unhealthy`; the inspect command prints
the raw status, including `starting` during the `start_period`.

## Related

- [Installation](./installation.md) — the Compose file that carries the probe.
- [Issues tab](../guides/issues.md) — `health=unhealthy` is a finding there;
  `starting` and a missing healthcheck are not.
