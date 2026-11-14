---
title: Backup reference job
description: The host-side script that produces the dumps the Storage tab verifies, including install, retention, and how to check an artifact.
---

`scripts/backup-databases.sh` is the reference producer for the evidence the
Storage tab verifies. It is host-side tooling: dumbdock never runs, schedules, or
reads it.

## What it does

- Discovers containers by `dumbdock.backup.engine` and validates the compose
  project/service labels.
- PostgreSQL: `pg_dump` inside the container. MySQL/MariaDB: `mysqldump` or
  `mariadb-dump` (single transaction, routines, events). SQLite:
  `sqlite3 ".backup"` plus `docker cp`, because a plain file copy of a live
  SQLite database is not safe.
- Writes `<dataRoot>/<project>/<service>/backups/<engine>-<service>-<UTC stamp>.sql.gz`
  with `umask 077` and mode `0600`, rejects an empty dump, and keeps the newest
  14 artifacts (`BACKUP_RETENTION`).
- `--dry-run` prints every planned action without writing.
- Reports each outcome to dumbdock when `DUMBDOCK_URL` and
  `DUMBDOCK_BACKUP_TOKEN` are set. A failed dump reports `ok:false` and exits
  non-zero.

## Install

Review the script first; it runs as root with Docker access.

```bash
sudo install -m 0755 scripts/backup-databases.sh /usr/local/bin/dumbdock-backup
sudo install -m 0644 scripts/dumbdock-backup.service /etc/systemd/system/
sudo install -m 0644 scripts/dumbdock-backup.timer /etc/systemd/system/
sudo tee /etc/default/dumbdock-backup >/dev/null <<'EOF'
DUMBDOCK_DATA_ROOT=/srv/containers
BACKUP_RETENTION=14
DUMBDOCK_URL=http://127.0.0.1:8080
DUMBDOCK_BACKUP_TOKEN=replace-me
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now dumbdock-backup.timer
```

The timer runs daily at 02:30 with a randomized delay.

A dataset with `dumbdock.backup.disabled=true` is skipped by the monitor but is
still backed up if it carries the engine label — remove the label to stop dumping
it.

## Verify an artifact

```bash
gzip -t /srv/containers/mwp/postgres/backups/*.sql.gz
pg_restore --list /srv/containers/mwp/postgres/backups/<artifact>.sql.gz | head
sqlite3 <artifact>.sqlite "pragma integrity_check;"
```

The Storage tab only reads freshness metadata: a non-empty artifact from a broken
`pg_dump` still counts as evidence, so content verification stays with you.

## Related

- [Storage tab](./storage.md) — the verdicts and evidence providers.
- [Issues tab](./issues.md) — where stale and missing backups surface.
