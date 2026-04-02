#!/usr/bin/env sh
set -eu
BACKUP_PATH="${1:?Provide backup db path}"
DB_PATH="${2:-./data/store.db}"
mkdir -p "$(dirname "$DB_PATH")"
cp "$BACKUP_PATH" "$DB_PATH"
echo "Restored backup to: $DB_PATH"
