#!/usr/bin/env sh
set -eu
BACKUP_PATH="${1:?Provide backup db path}"
if [ -d ./runtime/data ]; then
  DEFAULT_DB_PATH="./runtime/data/store.db"
else
  DEFAULT_DB_PATH="./data/store.db"
fi
DB_PATH="${2:-$DEFAULT_DB_PATH}"
mkdir -p "$(dirname "$DB_PATH")"

if [ -f docker-compose.yml ] && command -v docker >/dev/null 2>&1; then
  docker compose stop proxy app worker
fi

cp "$BACKUP_PATH" "$DB_PATH"
rm -f "$DB_PATH-wal" "$DB_PATH-shm"

if [ -f docker-compose.yml ] && command -v docker >/dev/null 2>&1; then
  docker compose run --rm --no-deps app openbid-sqlite-check
  docker compose up -d
fi

echo "Restored backup to: $DB_PATH"
