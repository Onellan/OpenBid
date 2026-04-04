#!/usr/bin/env sh
set -eu

if [ "${ALERT_WEBHOOK_URL:-}" = "" ]; then
  exit 0
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required to send alert webhooks" >&2
  exit 1
fi

STATUS="${1:?Provide alert status}"
CODE="${2:?Provide alert code}"
NAME="${3:?Provide alert name}"
SEVERITY="${4:?Provide alert severity}"
SUMMARY="${5:?Provide alert summary}"
DETAILS="${6:-}"
HOSTNAME_VALUE="$(hostname 2>/dev/null || printf '%s' unknown)"
OBSERVED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | awk 'BEGIN{first=1} {if (!first) printf "\\n"; printf "%s", $0; first=0}'
}

PAYLOAD=$(cat <<EOF
{"app":"openbid","status":"$(json_escape "$STATUS")","code":"$(json_escape "$CODE")","name":"$(json_escape "$NAME")","severity":"$(json_escape "$SEVERITY")","summary":"$(json_escape "$SUMMARY")","details":"$(json_escape "$DETAILS")","observed_at":"$OBSERVED_AT","host":"$(json_escape "$HOSTNAME_VALUE")"}
EOF
)

curl -fsS -X POST \
  -H "Content-Type: application/json" \
  --data "$PAYLOAD" \
  "$ALERT_WEBHOOK_URL" >/dev/null
