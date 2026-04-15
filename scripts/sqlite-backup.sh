#!/usr/bin/env sh
set -eu
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
STAMP="$(date +%Y%m%d-%H%M%S)"
if [ -d ./runtime/backups ]; then
  DEFAULT_OUT_DIR="./runtime/backups"
else
  DEFAULT_OUT_DIR="./backups"
fi
OUT_DIR="${2:-$DEFAULT_OUT_DIR}"
mkdir -p "$OUT_DIR"
OUT_PATH="${1:-$OUT_DIR/store-$STAMP.db}"
backup_ok=0

cleanup() {
  if [ "$backup_ok" -eq 1 ]; then
    return
  fi
  "$SCRIPT_DIR/post-alert.sh" firing backup_command_failed "Backups" danger "SQLite backup command failed." "Output path: $OUT_PATH" || true
}

trap cleanup EXIT INT TERM

if command -v docker >/dev/null 2>&1 && [ -f docker-compose.yml ]; then
  CONTAINER_OUT_PATH="$OUT_PATH"
  case "$OUT_PATH" in
    ./runtime/backups/*)
      CONTAINER_OUT_PATH="/app/backups/${OUT_PATH#./runtime/backups/}"
      ;;
    runtime/backups/*)
      CONTAINER_OUT_PATH="/app/backups/${OUT_PATH#runtime/backups/}"
      ;;
    ./backups/*)
      CONTAINER_OUT_PATH="/app/backups/${OUT_PATH#./backups/}"
      ;;
    backups/*)
      CONTAINER_OUT_PATH="/app/backups/${OUT_PATH#backups/}"
      ;;
  esac
  docker compose exec -T app openbid-sqlite-backup "$CONTAINER_OUT_PATH"
else
  DATA_PATH="${DATA_PATH:-./data/store.db}"
  DATA_PATH="$DATA_PATH" BACKUP_DIR="$(dirname "$OUT_PATH")" go run ./cmd/sqlite_backup "$OUT_PATH"
fi
backup_ok=1
trap - EXIT INT TERM

echo "Created backup: $OUT_PATH"
