---
title: API reference
description: Every JSON endpoint dumbdock exposes, the fields it returns, and the write endpoints for issue acknowledgements and backup reports.
---

dumbdock exposes these JSON endpoints. Read endpoints answer from the snapshot
built by the poll loop; none of them calls Docker or GitHub on request.

## GET /api/containers

Returns all container cards, groups, and detection status. The top-level response
includes `hasTraefik: true` when a Traefik container is running.

```json
{
  "cards": [],
  "unlabeled": [],
  "groups": ["Web Servers", "Databases", "Other"],
  "grouped": {
    "Web Servers": [],
    "Databases": []
  }
}
```

Each card includes `name`, `group`, `icon`, `href`, `description`, `order`,
`containerId`, `containerName`, `image`, `status`, `state`, `ports`, `created`,
`labels`, `hasLabels`, `hasPublicBinding`, `publicBindingIPs`, `traefikEnabled`,
`traefikURLs`, `updateStatus`, `updateAvailable`, `currentDigest` and
`latestDigest`.

## GET /api/traefik

Returns Traefik detection and status data:

```json
{
  "found": true,
  "apiUrl": "http://172.17.0.2:8080",
  "error": "",
  "disabled": false,
  "disabledReason": "",
  "attempts": 0,
  "attemptLimit": 3,
  "data": {
    "apiUrl": "http://172.17.0.2:8080",
    "authConfigured": false,
    "version": {},
    "overview": {},
    "entrypoints": [],
    "httpRouters": [],
    "httpServices": [],
    "middlewares": [],
    "tcpRouters": [],
    "tcpServices": [],
    "tlsCerts": [],
    "endpointErrors": {},
    "notConfigured": [],
    "majorVersion": 3,
    "versionSupported": true
  }
}
```

When no Traefik container is found, `found` is `false` and `data` is `null`. When
the boot attempt gate is disabled, `disabled` is `true`, `disabledReason` holds
the last failure, `attempts` and `attemptLimit` report the spent budget (for
example `3` and `3`), and `data` stays `null` until the process restarts.

## GET /api/integrations

Every registered integration, in registry order, with a summary. This is a cache
read of the last poll: it never calls Docker or a vendor API.

```json
{
  "integrations": [
    {
      "id": "watchtower",
      "displayName": "Watchtower",
      "description": "Reads Watchtower's metrics API: scan counters, last-scan results, and monitored containers.",
      "detected": true,
      "enabled": true,
      "forced": false,
      "containerName": "watchtower",
      "image": "containrrr/watchtower:1.7",
      "url": "http://10.0.5.2:8080",
      "urlSource": "auto",
      "authConfigured": true,
      "disabled": false,
      "attempts": 0,
      "attemptLimit": 3,
      "stats": [{"label": "Scans total", "value": "1"}],
      "sections": [{"title": "Configuration", "columns": ["Setting", "Value"], "rows": [["HTTP API (metrics)", "yes"]]}],
      "lastChecked": "2026-11-16T14:32:51Z"
    }
  ],
  "summary": {"registered": 3, "detected": 2, "disabled": 0, "hidden": 1}
}
```

`urlSource` is the layer that supplied the endpoint — `config`, `label`, `env`,
or `auto`. `hidden` marks an integration switched off by an override.
Credentials and vendor payloads are never serialised; `authConfigured` only
reports that one is in use.

## GET /api/integrations/{id}

One integration's state, or `404` with `{"error": "unknown integration: …"}` for
an unregistered id.

## GET /api/issues

Returns the cached host-issue snapshot.

```json
{
  "count": 2,
  "items": [
    {
      "id": "<64-char hex>",
      "category": "container-health",
      "severity": "error",
      "title": "Container is unhealthy",
      "detail": "api (api:1) reports Docker health status \"unhealthy\".",
      "source": "container",
      "containerId": "0123456789ab",
      "containerName": "api",
      "firstObservedAt": "2026-11-06T12:00:00Z",
      "lastObservedAt": "2026-11-06T12:00:00Z",
      "acknowledged": false
    }
  ],
  "acknowledged": [],
  "coverage": {
    "collectionComplete": true,
    "dockerVersion": {"checked": true, "lastCheckedAt": "2026-11-06T00:00:00Z"},
    "warnings": []
  },
  "checkedAt": "2026-11-06T12:00:00Z"
}
```

`items` holds active findings; `acknowledged` holds the same record shape with
`acknowledged: true` and `acknowledgedAt`. `count` is the number of active
findings. The response contains no Docker labels, Traefik credentials, registry
tokens, or log text.

## POST and DELETE `/api/issues/{id}/acknowledge`

Acknowledge or unacknowledge one finding. Both require the same authentication as
the dashboard **and** the non-simple header `X-Dumbdock-Request: issues` — the
custom header is a CSRF control an HTML form cannot set. The body is empty; the
success response is the updated `GET /api/issues` payload.

| Status | Meaning |
|--------|---------|
| `200` | Updated snapshot returned |
| `400` | The `id` is not a server-issued issue ID |
| `403` | Missing `X-Dumbdock-Request: issues` |
| `404` | Unknown or already-resolved issue ID |
| `500` | The acknowledgement could not be persisted; the published snapshot is unchanged |

```bash
curl -X POST -H 'X-Dumbdock-Request: issues' \
  http://127.0.0.1:8080/api/issues/<id>/acknowledge
```

## GET /api/storage

Returns the cached storage snapshot: one dataset per detected database, every
writable mount on a compose-managed container, and the evidence-provider
coverage.

```json
{
  "datasets": [
    {
      "key": "mwp/postgres",
      "project": "mwp",
      "service": "postgres",
      "containerName": "mwp-postgres-1",
      "containerId": "0123456789ab",
      "engine": "postgres",
      "database": "app",
      "dataKind": "volume",
      "dataDestination": "/var/lib/postgresql/data",
      "dataPath": "mwp_pgdata",
      "dataPathOk": false,
      "dataIssue": "data-named-volume",
      "expectedDataPath": "/srv/containers/mwp/postgres",
      "backupDir": "/srv/containers/mwp/postgres/backups",
      "policyMaxAge": "26h0m0s",
      "policySource": "default",
      "required": true,
      "exempt": false,
      "verdict": "missing",
      "reason": "no backup evidence found",
      "evidence": []
    }
  ],
  "mounts": [],
  "coverage": {
    "collectionComplete": true,
    "containerRoot": "/opt/containers",
    "dataRoot": "/srv/containers",
    "backupRoot": "/srv/containers",
    "providers": [
      {"name": "filesystem", "enabled": true, "configured": true, "lastCheckedAt": "2026-11-07T12:00:00Z"},
      {"name": "backup-containers", "enabled": false, "configured": false},
      {"name": "reports", "enabled": true, "configured": true}
    ]
  },
  "checkedAt": "2026-11-07T12:00:00Z"
}
```

`verdict` is `ok`, `stale`, `missing`, `unverified`, or `exempt`. `unverified`
means no evidence provider is configured, or the dataset is not required — it is
never presented as a missing backup. The response contains no file contents,
environment values, or label dumps.

## POST /api/backups/report

Accepts one backup report. It requires authentication — a dashboard session
**or** `Authorization: Bearer <DUMBDOCK_BACKUP_TOKEN>` — and the non-simple header
`X-Dumbdock-Request: backups`. Only targets that currently exist as detected
datasets are accepted, and the artifact is a bare file name, never a path.

```json
{
  "target": "mwp/postgres",
  "engine": "postgres",
  "ok": true,
  "at": "2026-11-07T02:30:12Z",
  "artifact": "postgres-postgres-20261107T023012Z.sql.gz",
  "detail": "pg_dump ok"
}
```

| Status | Meaning |
|--------|---------|
| `200` | Report stored |
| `400` | Malformed body, artifact with a path separator or control character, engine mismatch, bad or too-old timestamp |
| `403` | Missing `X-Dumbdock-Request: backups` |
| `404` | `target` is not a currently detected dataset |
| `413` | Body larger than 16 KiB |
| `500` | The report could not be persisted |

A future `at` (more than five minutes ahead of server time) is clamped to now.
Reports for a dataset that no longer exists are pruned after 30 days.

```bash
curl -X POST -H 'X-Dumbdock-Request: backups' \
  -H "Authorization: Bearer $DUMBDOCK_BACKUP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target":"mwp/postgres","engine":"postgres","ok":true,"artifact":"app.sql.gz"}' \
  http://127.0.0.1:8080/api/backups/report
```

## GET /api/networking

Returns container identity, state, network attachments and ports — no labels.

## GET /api/version

Returns `{"version":"<semver>","build":"<sha>"}`. Public in every auth mode, so a
proxy or a monitoring check can ask which build is running.

## Supported Traefik version

dumbdock requires **Traefik v3**. The detected major version is parsed from
`/api/version`, returned as `majorVersion` and `versionSupported`, and:

- a **v3** server renders normally with no warning;
- a non-v3 or unparseable server shows an **"Unsupported Traefik version"** error
  banner in the Traefik tab while still rendering partial data, and the server
  logs `traefik: unsupported version "…" (only v3 is supported)` once per state
  change, never on every poll.

## Related

- [Issues tab](../guides/issues.md) — what the issue rows mean.
- [Storage tab](../guides/storage.md) — the dataset and mount fields.
- [Traefik dashboard](../guides/traefik.md) — the gate and its disabled state.
- [Integrations](../guides/integrations.md) — what each field means on the card.
