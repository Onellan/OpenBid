#!/usr/bin/env sh
set -eu
grep -RIn "store.json\|NewJSONStore\|json_to_sqlite\|JSON store\|file-backed store" . || true
