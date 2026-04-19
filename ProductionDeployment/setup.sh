#!/usr/bin/env sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
cd "$SCRIPT_DIR"

runtime_dirs="
runtime
runtime/data
runtime/backups
runtime/secrets
"

info() {
  printf '%s\n' "$1"
}

fail() {
  printf 'ERROR: %s\n' "$1" >&2
  exit 1
}

random_base64() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 48
    return
  fi
  if [ -r /dev/urandom ]; then
    od -An -N48 -tx1 /dev/urandom | tr -d ' \n'
    printf '\n'
    return
  fi
  fail "cannot generate secrets: openssl is unavailable and /dev/urandom is not readable"
}

default_admin_password() {
  printf '%s\n' 'OpenBid!2026-YK4j3z39CEfu0kbFHcEzM8yI'
}

ensure_nonempty_file() {
  path="$1"
  label="$2"
  if [ -f "$path" ] && [ ! -s "$path" ]; then
    fail "$label exists but is empty: $path"
  fi
}

info "Preparing OpenBid production runtime directories..."

for dir in $runtime_dirs; do
  mkdir -p "$dir" || fail "failed to create $dir"
done

for keep in runtime/.gitkeep runtime/data/.gitkeep runtime/backups/.gitkeep runtime/secrets/.gitkeep; do
  if [ ! -e "$keep" ]; then
    : > "$keep" || fail "failed to create $keep"
  fi
done

if command -v chmod >/dev/null 2>&1; then
  chmod 755 runtime || fail "failed to set runtime directory permissions"
  chmod 777 runtime/data runtime/backups || fail "failed to set runtime/data and runtime/backups directory permissions"
  chmod 700 runtime/secrets || fail "failed to set runtime/secrets permissions"
fi

# Pre-create database file with permissive permissions so both container app and host e2e_seed can access it
db_file="runtime/data/store.db"
if [ ! -f "$db_file" ]; then
  : > "$db_file" || fail "failed to create $db_file"
  if command -v chmod >/dev/null 2>&1; then
    chmod 666 "$db_file" || fail "failed to set $db_file permissions"
  fi
fi

secret_key_file="runtime/secrets/openbid_secret_key"
admin_password_file="runtime/secrets/openbid_bootstrap_admin_password"

ensure_nonempty_file "$secret_key_file" "OpenBid secret key"
ensure_nonempty_file "$admin_password_file" "Bootstrap admin password"

if [ ! -f "$secret_key_file" ]; then
  random_base64 > "$secret_key_file" || fail "failed to write $secret_key_file"
  info "Created $secret_key_file"
else
  info "Kept existing $secret_key_file"
fi

if [ ! -f "$admin_password_file" ]; then
  default_admin_password > "$admin_password_file" || fail "failed to write $admin_password_file"
  info "Created $admin_password_file"
else
  info "Kept existing $admin_password_file"
fi

if command -v chmod >/dev/null 2>&1; then
  chmod 444 "$secret_key_file" "$admin_password_file" || fail "failed to set secret file permissions"
fi

if [ ! -f .env ]; then
  info "No .env found. Docker Compose will use safe production defaults; copy .env.example to .env when you need overrides."
else
  info "Found .env"
fi

info "Runtime is ready under ProductionDeployment/runtime."
info "Start from source: docker compose up -d --build"
info "Start with GHCR: docker compose -f docker-compose.ghcr.yml up -d"
