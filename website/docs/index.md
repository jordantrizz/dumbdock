---
title: dumbdock
description: A simple, no-frills web dashboard for your running Docker containers, organised with labels.
slug: /
---

dumbdock is a single binary that reads the Docker socket and renders a clean,
label-driven dashboard of your running containers — with a networking view, a
Traefik view, an issues list, and storage and backup checks.

It never mutates the host: it reads the Docker and Traefik APIs and shows you
what it can prove.

## Start here

- [What dumbdock is](./getting-started/overview.md) — the dashboard, the tabs,
  and what it deliberately does not do.
- [Installation](./getting-started/installation.md) — Compose, `docker run`, or
  from source.
- [Your first dashboard](./getting-started/first-dashboard.md) — run it, sign in,
  and label one container end to end.

## The tabs

| Tab | What it answers | Guide |
| --- | --- | --- |
| Dashboard | Which containers are running, and how are they grouped? | [Scoreboard](./guides/scoreboard.md) |
| Networking | What is on which network, and what is publicly bound? | [Networking](./guides/networking.md) |
| Traefik | What does my reverse proxy think is configured? | [Traefik](./guides/traefik.md) |
| Issues | What can be proven broken right now? | [Issues](./guides/issues.md) |
| Storage | Where do data and backups actually live? | [Storage](./guides/storage.md) |
| Icons | Where did that icon come from? | [Icons](./guides/icons.md) |
| Help | How do I wire Traefik's API? | [Help](./guides/help-tab.md) |
