#!/usr/bin/env sh
set -eu
export APP_ENV="${APP_ENV:-development}"
export APP_ADDR="${APP_ADDR:-:8080}"
export DATA_PATH="${DATA_PATH:-./data/store.db}"
export SECRET_KEY="${SECRET_KEY:-local-dev-secret-change-me}"
go run ./cmd/server
