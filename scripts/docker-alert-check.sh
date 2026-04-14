#!/usr/bin/env sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
if [ -d ./runtime ]; then
  DEFAULT_ALERT_STATE_DIR="./runtime/ops-state"
else
  DEFAULT_ALERT_STATE_DIR=".ops-state"
fi
STATE_DIR="${ALERT_STATE_DIR:-$DEFAULT_ALERT_STATE_DIR}"
STATE_FILE="$STATE_DIR/docker-alert-state.txt"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
ENV_FILE="${ENV_FILE:-}"

mkdir -p "$STATE_DIR"

run_compose() {
  if [ -n "$ENV_FILE" ]; then
    docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"
    return
  fi
  docker compose -f "$COMPOSE_FILE" "$@"
}

issues=""
ids="$(run_compose ps -q 2>/dev/null || true)"

if [ -z "$ids" ]; then
  issues="No compose containers were found for $COMPOSE_FILE"
else
  for id in $ids; do
    line="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.service" }}|{{ .State.Status }}|{{ if .State.Health }}{{ .State.Health.Status }}{{ else }}none{{ end }}' "$id" 2>/dev/null || true)"
    if [ -z "$line" ]; then
      continue
    fi
    service=$(printf '%s' "$line" | cut -d'|' -f1)
    status=$(printf '%s' "$line" | cut -d'|' -f2)
    health=$(printf '%s' "$line" | cut -d'|' -f3)
    if [ "$status" != "running" ]; then
      issues="${issues}${issues:+
}$service state=$status health=$health"
      continue
    fi
    if [ "$health" != "none" ] && [ "$health" != "healthy" ] && [ "$health" != "starting" ]; then
      issues="${issues}${issues:+
}$service state=$status health=$health"
    fi
  done
fi

previous=""
if [ -f "$STATE_FILE" ]; then
  previous="$(cat "$STATE_FILE")"
fi

if [ -n "$issues" ]; then
  printf '%s\n' "$issues" > "$STATE_FILE"
  if [ "$issues" != "$previous" ]; then
    "$SCRIPT_DIR/post-alert.sh" firing containers_unhealthy "Containers" critical "One or more OpenBid containers are unhealthy." "$issues" || true
  fi
  printf '%s\n' "$issues"
  exit 1
fi

if [ -n "$previous" ]; then
  rm -f "$STATE_FILE"
  "$SCRIPT_DIR/post-alert.sh" resolved containers_unhealthy "Containers" info "OpenBid containers are healthy again." "$previous" || true
fi

printf '%s\n' "All compose containers are healthy."
