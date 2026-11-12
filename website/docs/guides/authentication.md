---
title: Authentication
description: Choose none, HTTP Basic, or the web login form, and understand the session lifetime and the random-password fallback.
---

dumbdock has three authentication modes, selected by the `DUMBDOCK_AUTH_MODE`
environment variable or the `authMode` setting in `dumbdock.json`. The
environment variable wins.

| Mode | Value | Behaviour |
|------|-------|-----------|
| No auth | `none` | Dashboard and API are open. |
| HTTP Basic | `http-auth` | Browser-native login prompt. Any username is accepted; the password must match `DUMBDOCK_PASSWORD`. |
| Web login | `web-auth` | Login form overlay with username, password and **Remember me**. Session cookie `dumbdock_session` is `HttpOnly`, `SameSite=Lax`, `Path=/`. Standard sessions last 8 hours, "Remember me" sessions 30 days. Sessions live in memory, so a restart logs everyone out. A logout button appears in the nav bar while signed in. |

When neither `DUMBDOCK_AUTH_MODE` nor `authMode` is set, the mode defaults to
`web-auth`. Set `none` explicitly to disable auth.

## Random password on an empty setting

When an auth mode is active but `DUMBDOCK_PASSWORD` is empty, a random password is
generated for the run and logged once at startup. It is held in memory only and
changes on every restart, so set the variable to keep a stable password.

Unknown mode values — from the environment or the config file — log a warning and
**disable** auth.

## Configure

```bash
DUMBDOCK_AUTH_MODE=web-auth   # or http-auth for a Basic prompt; none to disable
DUMBDOCK_PASSWORD=choose-a-strong-password
DUMBDOCK_PORT=8080
```

> **HTTPS note:** the session cookie does not set the `Secure` flag because
> dumbdock serves plain HTTP. Terminate TLS in a reverse proxy such as Traefik
> when exposing the dashboard beyond localhost.

## Related

- [Installation](../getting-started/installation.md) — the published port and its
  loopback default.
