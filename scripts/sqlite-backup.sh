#!/usr/bin/env sh
set -eu
DB_PATH="${1:-./data/store.db}"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="${2:-./backups}"
mkdir -p "$OUT_DIR"
cp "$DB_PATH" "$OUT_DIR/store-$STAMP.db"
echo "Created backup: $OUT_DIR/store-$STAMP.db"
