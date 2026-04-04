#!/usr/bin/env sh
set -eu
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="${2:-./backups}"
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
  docker compose exec -T app openbid-sqlite-backup "$OUT_PATH"
else
  DATA_PATH="${DATA_PATH:-./data/store.db}"
  DATA_PATH="$DATA_PATH" BACKUP_DIR="$(dirname "$OUT_PATH")" go run ./cmd/sqlite_backup "$OUT_PATH"
fi
backup_ok=1
trap - EXIT INT TERM

echo "Created backup: $OUT_PATH"
