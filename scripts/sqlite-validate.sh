#!/usr/bin/env sh
set -eu
DB_PATH="${1:-./data/store.db}"
if [ ! -f "$DB_PATH" ]; then
  echo "Database not found: $DB_PATH" >&2
  exit 1
fi
echo "SQLite file present: $DB_PATH"
echo "Run app-level runtime validation with:"
echo '  go test ./internal/store -run TestSQLiteMigrationAndRuntimeValidation'
