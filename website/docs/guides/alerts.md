---
title: Alerts
description: Send a notification through ntfy.sh or Gotify when a new unlabeled container appears, with a configurable cooldown.
---

dumbdock can notify you when new unlabeled containers appear. Alerts are opt-in:
with no target configured, nothing is sent.

## ntfy.sh

```yaml
environment:
  - NTFY_TOPIC=mytopic
  - DUMBDOCK_URL=https://dumbdock.example.com   # optional: link back to the dashboard
```

## Gotify

```yaml
environment:
  - GOTIFY_URL=https://gotify.example.com
  - GOTIFY_TOKEN=your-app-token
```

## Alert cooldown

```yaml
environment:
  - ALERT_COOLDOWN=5m   # minimum time between alert batches (default: 5m)
```

The cooldown batches several new containers into one notification instead of one
per container.

## Related

- [Authentication](./authentication.md) — set a password before sending links to
  a publicly reachable dashboard.
