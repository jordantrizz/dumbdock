---
title: Storage tab
description: The host conventions for compose files and data, how databases are detected, and what each backup verdict means.
---

The **Storage** tab sits between **Issues** and **Icons**. It answers two
questions per compose project: *is the compose file and its data where the host
convention says it should be?* and *when was each database last backed up?*

Only compose-managed containers are evaluated; a standalone `docker run`
container is out of scope.

## Conventions

| Rule | Default | Override |
|------|---------|----------|
| Compose file | `/opt/containers/<project>/docker-compose.yml` (any path under `project/`) | `DUMBDOCK_CONTAINER_ROOT` / `storage.containerRoot` |
| Persistent data | `/srv/containers/<project>/<service>/` | `DUMBDOCK_DATA_ROOT` / `storage.dataRoot` |
| Backup artifacts | `/srv/containers/<project>/<service>/backups/` | `DUMBDOCK_BACKUP_ROOT` / `storage.backupRoot` |
| Freshness | `26h` per required dataset | `dumbdock.backup.max-age`, `storage.backups.<project>/<service>.maxAge` |

A writable bind mount is a **data** mount unless it looks like a config artifact
(a known config extension, or a destination under `/etc/`) or is waived with
`dumbdock.storage.ignore`. Beyond that:

- A mount whose source is inside `com.docker.compose.project.working_dir` is
  reported as *beside the compose file*.
- A named or anonymous volume is reported as *in a Docker volume*.
- A database with no matching mount is reported as *in the container layer*.

## Databases and verdicts

The engine is detected from the image repository — `postgres`, `postgis`,
`timescaledb`, `mysql`, `percona`, `mariadb`, with Bitnami paths — or declared
with `dumbdock.backup.engine`. SQLite is detected from `dumbdock.backup.sqlite`
or from `*.db`, `*.sqlite`, `*.sqlite3` files under the service data directory,
within a bounded depth and entry count. The datadir comes from the engine
default, `PGDATA`, or `dumbdock.data.mount`.

| Verdict | Meaning |
|---------|---------|
| `ok` | Newest evidence is within the policy window |
| `stale` | Evidence exists but is older than the window |
| `missing` | The policy requires a backup and none was found |
| `unverified` | No evidence provider is configured, or the dataset is not required — **not** a claim that no backup exists |
| `exempt` | `dumbdock.backup.disabled=true`: visible, never a finding |

## Evidence providers

1. **Filesystem** — a read-only scan of
   `<backupRoot>/<project>/<service>/backups/`. Only the directory listing and
   modification times are read; dump contents are never opened. The scan is
   bounded (200 entries per directory, a three-second budget), refuses symlinks,
   and matches `.sql`, `.sql.gz`, `.dump`, `.tar.gz`, `.sqlite`, `.sqlite.gz`,
   `.db.gz`, `.bak`. Mount the data root at the same path inside the container:

   ```yaml
   volumes:
     - /srv/containers:/srv/containers:ro
   ```

2. **Backup containers** — a container labelled
   `dumbdock.backup-for=<project>/<service>` counts when its last run **exited
   0** (read from `State.ExitCode` and `FinishedAt`). A running container is not
   proof of a completed run.

3. **Push reports** — a backup job POSTs to `/api/backups/report` with
   `DUMBDOCK_BACKUP_TOKEN`. Reports live in `backup-reports.json` (mode `0600`)
   in the state directory.

An unconfigured provider is shown as coverage and keeps datasets `unverified`. A
provider that **errors** marks the collection partial, and partial collection
never prunes acknowledgements.

dumbdock does not run `docker exec`, read a dump, pull an image, restart a
container, or move a file.

## Migrating a host

Existing paths are **reported, not changed**. Moving a project is a
maintenance-window operation with `sudo`, performed by the operator:

```bash
# 1. Stop the project and take a restorable dump first.
docker compose -p mwp down
docker exec mwp-postgres-1 pg_dump -U postgres app | gzip >/tmp/app.sql.gz

# 2. Move the compose project and create the data directory.
sudo install -d -m 0755 /opt/containers /srv/containers/mwp/postgres
sudo mv /root/containers/mwp /opt/containers/mwp

# 3. Copy data out of a named volume (repeat per service).
docker run --rm -v mwp_pgdata:/from -v /srv/containers/mwp/postgres:/to \
  alpine sh -c 'cp -a /from/. /to/'

# 4. Point the compose file at /srv/containers/... and restart.
docker compose -p mwp -f /opt/containers/mwp/docker-compose.yml up -d
```

Verify with the Storage tab — mounts flip to OK and the compose findings
disappear — and with `pg_restore --list /tmp/app.sql.gz` before removing the old
volume.

## Related

- [Backup reference job](./backup-job.md) — the producer for this evidence.
- [Issues tab](./issues.md) — storage and backup findings in the issue list.
