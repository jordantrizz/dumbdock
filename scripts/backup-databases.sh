#!/usr/bin/env bash
#
# backup-databases.sh — reference logical-dump backup job for containers
# managed by dumbdock's conventions.
#
# It discovers database containers by label, writes a logical dump into
#   ${DUMBDOCK_DATA_ROOT:-/srv/containers}/<project>/<service>/backups/
# and optionally reports the outcome to dumbdock so the Storage tab can show it.
#
# This script is host-side tooling. dumbdock never runs it, never reads dump
# contents, and never schedules it.
#
# Labels read from a container:
#   dumbdock.backup.engine   postgres | mysql | mariadb | sqlite
#   dumbdock.backup.sqlite   comma-separated in-container sqlite file paths
#
# Environment:
#   DUMBDOCK_DATA_ROOT      data root (default /srv/containers)
#   BACKUP_RETENTION        artifacts kept per dataset (default 14)
#   DUMBDOCK_URL            dumbdock base URL for reports (optional)
#   DUMBDOCK_BACKUP_TOKEN   report token (optional, requires DUMBDOCK_URL)
#
# Usage: backup-databases.sh [--dry-run]

set -Eeuo pipefail

umask 077

DATA_ROOT="${DUMBDOCK_DATA_ROOT:-/srv/containers}"
RETENTION="${BACKUP_RETENTION:-14}"
DUMBDOCK_URL="${DUMBDOCK_URL:-}"
DUMBDOCK_BACKUP_TOKEN="${DUMBDOCK_BACKUP_TOKEN:-}"
DRY_RUN=0

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
die() { log "error: $*"; exit 1; }

command -v docker >/dev/null 2>&1 || die "docker is not on PATH"
command -v gzip >/dev/null 2>&1 || die "gzip is not on PATH"

[[ "$RETENTION" =~ ^[0-9]+$ ]] || die "BACKUP_RETENTION must be a number"
(( RETENTION > 0 )) || die "BACKUP_RETENTION must be positive"

# container_label returns a label value, or an empty string.
container_label() {
  local id="$1" key="$2"
  docker inspect --format "{{ index .Config.Labels \"$key\" }}" "$id" 2>/dev/null || true
}

# report posts the outcome to dumbdock when a URL and token are configured.
# A reporting failure is logged but never fails the backup.
report() {
  local target="$1" engine="$2" ok="$3" at="$4" artifact="$5"
  [[ -n "$DUMBDOCK_URL" && -n "$DUMBDOCK_BACKUP_TOKEN" ]] || return 0
  if ! command -v curl >/dev/null 2>&1; then
    log "warning: curl is not on PATH; cannot report $target"
    return 0
  fi
  if (( DRY_RUN == 1 )); then
    log "dry-run: would report $target ok=$ok"
    return 0
  fi
  local body
  body=$(printf '{"target":"%s","engine":"%s","ok":%s,"at":"%s","artifact":"%s"}' \
    "$target" "$engine" "$ok" "$at" "$artifact")
  if ! curl -fsS -m 10 -o /dev/null \
    -H 'Content-Type: application/json' \
    -H 'X-Dumbdock-Request: backups' \
    -H "Authorization: Bearer $DUMBDOCK_BACKUP_TOKEN" \
    --data "$body" \
    "${DUMBDOCK_URL%/}/api/backups/report"; then
    log "warning: could not report $target to dumbdock"
  fi
}

# prune keeps the newest $RETENTION artifacts in a directory.
prune_dir() {
  local dir="$1"
  local -a files=()
  (( DRY_RUN == 1 )) && { log "dry-run: would prune $dir to $RETENTION artifacts"; return 0; }
  mapfile -t files < <(find "$dir" -maxdepth 1 -type f -name '*.gz' -printf '%T@ %p\n' 2>/dev/null | sort -rn | awk 'NR>'"$RETENTION"' {print $2}')
  local f
  for f in "${files[@]}"; do
    [[ -n "$f" ]] || continue
    rm -f -- "$f"
    log "pruned $f"
  done
}

# dump_postgres writes a pg_dump artifact for one container.
dump_postgres() {
  local id="$1" project="$2" service="$3" outdir="$4" stamp="$5"
  local db user tmp artifact
  db=$(container_label "$id" "dumbdock.backup.database")
  [[ -n "$db" ]] || db=$(docker exec "$id" sh -c 'printf %s "${POSTGRES_DB:-postgres}"')
  user=$(docker exec "$id" sh -c 'printf %s "${POSTGRES_USER:-postgres}"')

  tmp="$outdir/.dump-$$.sql"
  artifact="postgres-${service}-${stamp}.sql.gz"
  if (( DRY_RUN == 1 )); then
    log "dry-run: would pg_dump $project/$service database $db -> $outdir/$artifact"
    return 0
  fi
  if ! docker exec "$id" sh -c 'exec pg_dump -U "$1" --no-owner --no-acl "$2"' _ "$user" "$db" >"$tmp"; then
    rm -f -- "$tmp"
    log "error: pg_dump failed for $project/$service"
    return 1
  fi
  if [[ ! -s "$tmp" ]]; then
    rm -f -- "$tmp"
    log "error: pg_dump produced an empty dump for $project/$service"
    return 1
  fi
  gzip -c "$tmp" >"$outdir/$artifact"
  rm -f -- "$tmp"
  chmod 600 "$outdir/$artifact"
  log "wrote $outdir/$artifact"
  printf '%s' "$artifact"
}

# dump_mysql writes a mysqldump/mariadb-dump artifact for one container.
dump_mysql() {
  local id="$1" project="$2" service="$3" outdir="$4" stamp="$5" engine="$6"
  local db tmp artifact dumper
  db=$(container_label "$id" "dumbdock.backup.database")
  [[ -n "$db" ]] || db=$(docker exec "$id" sh -c 'printf %s "${MYSQL_DATABASE:-}"')

  dumper=mysqldump
  if [[ "$engine" == "mariadb" ]]; then
    if docker exec "$id" sh -c 'command -v mariadb-dump >/dev/null 2>&1'; then
      dumper=mariadb-dump
    fi
  fi

  tmp="$outdir/.dump-$$.sql"
  artifact="${engine}-${service}-${stamp}.sql.gz"
  if (( DRY_RUN == 1 )); then
    log "dry-run: would ${dumper} $project/$service database ${db:-all} -> $outdir/$artifact"
    return 0
  fi
  local args=(-uroot --single-transaction --quick --routines --events)
  if [[ -n "$db" ]]; then
    args+=(--databases "$db")
  else
    args+=(--all-databases)
  fi
  if ! docker exec "$id" sh -c 'exec "$1" -p"$MYSQL_ROOT_PASSWORD" "${@:2}"' _ "$dumper" "${args[@]}" >"$tmp"; then
    rm -f -- "$tmp"
    log "error: $dumper failed for $project/$service"
    return 1
  fi
  if [[ ! -s "$tmp" ]]; then
    rm -f -- "$tmp"
    log "error: $dumper produced an empty dump for $project/$service"
    return 1
  fi
  gzip -c "$tmp" >"$outdir/$artifact"
  rm -f -- "$tmp"
  chmod 600 "$outdir/$artifact"
  log "wrote $outdir/$artifact"
  printf '%s' "$artifact"
}

# dump_sqlite writes a consistent .backup copy of each labeled sqlite file.
dump_sqlite() {
  local id="$1" project="$2" service="$3" outdir="$4" stamp="$5" paths="$6"
  local path base tmp_src tmp_dst artifact
  local status=0
  local last=""
  IFS=',' read -r -a list <<<"$paths"
  for path in "${list[@]}"; do
    path="$(printf '%s' "$path" | xargs)"
    [[ -n "$path" ]] || continue
    base="$(basename "$path")"
    artifact="sqlite-${base}-${stamp}.gz"
    if (( DRY_RUN == 1 )); then
      log "dry-run: would sqlite3 .backup $project/$service:$path -> $outdir/$artifact"
      continue
    fi
    tmp_src="/tmp/dumbdock-backup-$$.sqlite"
    tmp_dst="$outdir/.dump-$$.sqlite"
    if ! docker exec "$id" sh -c 'command -v sqlite3 >/dev/null 2>&1'; then
      log "error: sqlite3 is not available in $project/$service"
      status=1
      continue
    fi
    if ! docker exec "$id" sqlite3 "$path" ".backup '$tmp_src'"; then
      log "error: sqlite .backup failed for $project/$service:$path"
      status=1
      continue
    fi
    if ! docker cp "$id:$tmp_src" "$tmp_dst" >/dev/null; then
      docker exec "$id" rm -f "$tmp_src" >/dev/null 2>&1 || true
      log "error: could not copy $project/$service:$tmp_src out of the container"
      status=1
      continue
    fi
    docker exec "$id" rm -f "$tmp_src" >/dev/null 2>&1 || true
    if [[ ! -s "$tmp_dst" ]]; then
      rm -f -- "$tmp_dst"
      log "error: sqlite backup is empty for $project/$service:$path"
      status=1
      continue
    fi
    gzip -c "$tmp_dst" >"$outdir/$artifact"
    rm -f -- "$tmp_dst"
    chmod 600 "$outdir/$artifact"
    log "wrote $outdir/$artifact"
    last="$artifact"
  done
  (( status == 0 )) || return 1
  printf '%s' "$last"
}

overall=0
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
reported_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

mapfile -t ids < <(docker ps --filter 'label=dumbdock.backup.engine' --format '{{.ID}}')
if (( ${#ids[@]} == 0 )); then
  log "no containers carry dumbdock.backup.engine; nothing to do"
  exit 0
fi

for id in "${ids[@]}"; do
  engine="$(container_label "$id" dumbdock.backup.engine | tr '[:upper:]' '[:lower:]')"
  project="$(container_label "$id" com.docker.compose.project)"
  service="$(container_label "$id" com.docker.compose.service)"
  name="$(docker inspect --format '{{.Name}}' "$id" 2>/dev/null | sed 's#^/##')"

  if [[ -z "$project" || -z "$service" ]]; then
    log "skip $name: not compose-managed (missing project/service labels)"
    continue
  fi
  if [[ ! "$project" =~ ^[A-Za-z0-9._-]+$ || ! "$service" =~ ^[A-Za-z0-9._-]+$ ]]; then
    log "skip $name: project/service labels contain unsupported characters"
    overall=1
    continue
  fi

  outdir="$DATA_ROOT/$project/$service/backups"
  if (( DRY_RUN == 0 )); then
    mkdir -p "$outdir"
  fi

  artifact=""
  ok=true
  case "$engine" in
    postgres|postgresql) artifact="$(dump_postgres "$id" "$project" "$service" "$outdir" "$stamp")" || ok=false ;;
    mysql) artifact="$(dump_mysql "$id" "$project" "$service" "$outdir" "$stamp" mysql)" || ok=false ;;
    mariadb) artifact="$(dump_mysql "$id" "$project" "$service" "$outdir" "$stamp" mariadb)" || ok=false ;;
    sqlite)
      paths="$(container_label "$id" dumbdock.backup.sqlite)"
      if [[ -z "$paths" ]]; then
        log "error: $name has no dumbdock.backup.sqlite label"
        ok=false
      else
        artifact="$(dump_sqlite "$id" "$project" "$service" "$outdir" "$stamp" "$paths")" || ok=false
      fi
      ;;
    *) log "error: unknown engine '$engine' on $name"; ok=false ;;
  esac

  if [[ "$ok" == true ]]; then
    prune_dir "$outdir"
    report "$project/$service" "$engine" true "$reported_at" "$artifact"
  else
    overall=1
    report "$project/$service" "$engine" false "$reported_at" ""
  fi
done

exit "$overall"
