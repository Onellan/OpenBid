#!/usr/bin/env sh
set -eu
if [ -d ./runtime/data ]; then
  DEFAULT_DB_PATH="./runtime/data/store.db"
else
  DEFAULT_DB_PATH="./data/store.db"
fi
DB_PATH="${1:-$DEFAULT_DB_PATH}"
if [ ! -f "$DB_PATH" ]; then
  echo "Database not found: $DB_PATH" >&2
  exit 1
fi

if command -v docker >/dev/null 2>&1 && [ -f docker-compose.yml ]; then
  docker compose run --rm --no-deps app openbid-sqlite-check
else
  DATA_PATH="$DB_PATH" go run ./cmd/sqlite_check
fi
