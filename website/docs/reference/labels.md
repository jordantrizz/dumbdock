---
title: Labels reference
description: Every dumbdock label — card metadata, storage and backup hints, and the labels dumbdock reads but does not own.
---

dumbdock discovers container metadata through Docker labels with the `dumbdock.`
prefix. Labels are optional: an unlabelled container still appears, in the
unlabeled section.

## Card labels

```yaml
services:
  myapp:
    image: nginx
    labels:
      dumbdock.name: "My Nginx"
      dumbdock.group: "Web Servers"
      dumbdock.icon: "https://example.com/nginx-icon.png"
      dumbdock.href: "https://myapp.example.com"
      dumbdock.description: "Serves the main website"
      dumbdock.order: "1"
```

| Label | Description |
|-------|-------------|
| `dumbdock.name` | Display name for the card |
| `dumbdock.group` | Group name to organise cards under |
| `dumbdock.icon` | URL to an icon image (SVG or PNG recommended) |
| `dumbdock.href` | Link — clicking the card name opens this URL |
| `dumbdock.description` | Short description shown below the name |
| `dumbdock.order` | Sort order within the group (lower = first) |

> **Note:** with no icon specified, dumbdock resolves one from the image name and
> places a match in the "Auto Detected" group. See [Icons](../guides/icons.md).

## Storage and backup labels

The [Storage tab](../guides/storage.md) reads these; none is required for the
dashboard to work.

| Label | Applies to | Effect |
|-------|-----------|--------|
| `dumbdock.backup.engine` | any container | Declares the database family: `postgres`, `mysql`, `mariadb`, or `sqlite`. Overrides image detection. |
| `dumbdock.backup.sqlite` | SQLite workload | Comma-separated **in-container** paths, e.g. `/data/app.db`. Also makes an otherwise generic image count as a SQLite dataset. |
| `dumbdock.backup.database` | database container | Database name shown on the Storage tab and used by the reference backup job. Only a name — never a password. |
| `dumbdock.backup.max-age` | dataset | Freshness window as a Go duration (`26h`, `48h`, `90m`). Overrides the config file and the default. |
| `dumbdock.backup.required` | dataset | `false` keeps a dataset visible but never raises a *missing backup* finding. |
| `dumbdock.backup.disabled` | dataset | `true` waives the backup requirement (`exempt`): visible, never a finding. |
| `dumbdock.backup-for` | backup container | Declares that this container backs up `project/service`. A clean exit-0 run is evidence; a running container is not. |
| `dumbdock.data.mount` | any container | `source:destination` (comma-separated) when the datadir is not the engine default. |
| `dumbdock.storage.ignore` | any container | Comma-separated mount destinations, or `all`, to waive data-path findings for deliberate mounts. |

```yaml
services:
  postgres:
    image: postgres:16
    labels:
      dumbdock.backup.database: app
      dumbdock.backup.max-age: 26h
    volumes:
      - /srv/containers/mwp/postgres:/var/lib/postgresql/data

  notes:
    image: alpine:3.20
    labels:
      dumbdock.backup.engine: sqlite
      dumbdock.backup.sqlite: /data/notes.db
    volumes:
      - /srv/containers/notes/app:/data
```

## Traefik labels

| Label | Effect |
|-------|--------|
| `dumbdock.traefik.api` | Explicit Traefik API base URL for auto-discovery |

dumbdock also reads labels it does not own: `com.docker.compose.*` for project
and dependency grouping, `traefik.enable` and
`traefik.http.routers.<name>.rule` for the link badge and the extracted URL.

## Related

- [Config file](./config-file.md) — the same overrides without re-labelling.
- [Storage tab](../guides/storage.md) — what the backup labels change.
- [Networking and network warnings](../guides/networking.md) — the Traefik badge.
