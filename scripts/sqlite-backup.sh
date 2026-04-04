#!/usr/bin/env sh
set -eu
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="${2:-./backups}"
mkdir -p "$OUT_DIR"
OUT_PATH="${1:-$OUT_DIR/store-$STAMP.db}"

if command -v docker >/dev/null 2>&1 && [ -f docker-compose.yml ]; then
  docker compose exec -T app openbid-sqlite-backup "$OUT_PATH"
else
  DATA_PATH="${DATA_PATH:-./data/store.db}"
  DATA_PATH="$DATA_PATH" BACKUP_DIR="$(dirname "$OUT_PATH")" go run ./cmd/sqlite_backup "$OUT_PATH"
fi

echo "Created backup: $OUT_PATH"
