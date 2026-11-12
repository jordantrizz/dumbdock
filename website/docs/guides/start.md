---
title: Your first dashboard
description: Run dumbdock with Docker Compose, sign in, and label one container so it appears in a group.
---

This walkthrough takes a running Docker host to a labelled dashboard in four
steps.

## 1. Start dumbdock

`control.sh` creates `docker-compose.yml` from the example if it is missing,
creates `.env`, builds the image, starts the stack, and waits for readiness:

```bash
./control.sh setup
```

The dashboard listens on port `8080` of the host by default. If you prefer to do
it by hand, copy `docker-compose.yml.example` to `docker-compose.yml` and run
`docker compose up -d`.

## 2. Sign in

Open `http://localhost:8080`.

The default auth mode is `web-auth`, so dumbdock asks for a password. When
`DUMBDOCK_PASSWORD` is unset, a random password is generated for the run and
logged once at startup:

```bash
docker compose logs dumbdock | grep -i password
```

## 3. Label one container

Add labels to a service in `docker-compose.yml` and restart it:

```yaml
services:
  whoami:
    image: traefik/whoami
    labels:
      dumbdock.name: "Who Am I"
      dumbdock.group: "Tools"
      dumbdock.icon: "traefik"
```

## 4. Confirm the grouping

Reload the dashboard. The container moves out of the unlabeled list and into the
**Tools** group, with the name and icon you set.

Anything still unlabeled stays in the unlabeled section, which shows
copy-paste-ready label examples for each container.

## Next

Installation options (Docker Compose, `docker run`, and building from source) and
the full `dumbdock.*` label reference are covered in the Getting started and
Reference sections.
