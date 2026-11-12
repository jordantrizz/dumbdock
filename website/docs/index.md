---
title: dumbdock
description: A simple, no-frills web dashboard for your running Docker containers, organised with labels.
slug: /
---

dumbdock is a single binary that reads the Docker socket and renders a clean,
label-driven dashboard of your running containers — with a networking view, a
Traefik view, an issues list, and storage/backup checks.

It never mutates the host: it reads the Docker and Traefik APIs and shows you
what it can prove.

## Start here

- [Your first dashboard](./guides/start.md) — run dumbdock, sign in, and label
  one container end to end.

## At a glance

| Concern | Where it lives |
| --- | --- |
| Organising containers | labels or a config file |
| Exposed ports and networks | Networking tab and network warnings |
| Reverse-proxy state | Traefik tab |
| Problems worth fixing | Issues tab |
| Where data and backups live | Storage tab |
