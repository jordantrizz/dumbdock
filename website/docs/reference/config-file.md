---
title: Config file
description: The dumbdock.json override file — settings, per-container overrides, precedence, and how to mount it.
---

Overrides can live in a JSON config file instead of Docker labels, which is
useful for containers you cannot or do not want to re-label.

```json
{
  "autoDetection": true,
  "autoDetectedGroup": "Auto Detected",
  "authMode": "web-auth",
  "containers": {
    "my-container-name": {
      "name": "My App",
      "group": "Web Servers",
      "icon": "https://example.com/icon.png",
      "href": "https://myapp.example.com",
      "description": "Does something useful",
      "order": 1
    }
  }
}
```

## Settings

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `autoDetection` | boolean | `true` | Enable automatic icon detection for unlabeled containers. With a match, the container goes to `autoDetectedGroup` instead of "Unlabeled". |
| `autoDetectedGroup` | string | `"Auto Detected"` | Group name for auto-detected containers. |
| `autoDetectionServiceBlacklist` | array of strings | `["server", "db", "app"]` | Compose service names skipped when matching by service name. Generic names are unreliable; the container still goes through the other fallbacks (OCI title, known slugs). |
| `containerBlacklist` | array of strings | `[]` | Container names hidden entirely from the dashboard, e.g. `["dumbdock", "traefik"]`. |
| `authMode` | string | `web-auth` | `none`, `http-auth`, or `web-auth`. `DUMBDOCK_AUTH_MODE` overrides it. |
| `containers` | object | `{}` | Per-container overrides keyed by container name. |
| `iconSets` | array | see [Icons](../guides/icons.md) | Icon providers. When present, replaces the built-in defaults entirely. |

The file also accepts `updateCheck` (boolean) and the `storage.*` keys
(`containerRoot`, `dataRoot`, `backupRoot`, `backups.<project>/<service>.maxAge`)
and `traefikAPIToken`.

## Precedence

Config-file overrides are applied first, then auto-detection. If a container has
both an explicit config entry and a matching auto-detectable icon, the config
entry wins.

The environment wins over the file where both exist — `DUMBDOCK_AUTH_MODE` over
`authMode`, for example.

## Mount it

```yaml
volumes:
  - ./dumbdock.json:/config/dumbdock.json:ro
```

A missing or unparseable file is not fatal: dumbdock logs a warning, falls back to
an empty config, and keeps running.

## Related

- [Startup logging](./startup-logging.md) — how a loaded or broken config is
  reported.
- [Environment variables](./environment-variables.md) — the settings that only
  exist as variables.
- [Labels reference](./labels.md) — the label equivalent of `containers`.
