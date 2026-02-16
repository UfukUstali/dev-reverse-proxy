#!/usr/bin/env bash
set -e

SERVER="${SERVER:-http://localhost:8080}"
ID="${ID:-myapp}"

# pick random free port between 3000–3100 if not provided
if [ -z "${PORT:-}" ]; then
  for _ in $(seq 1 50); do
    CANDIDATE=$((3000 + RANDOM % 101))
    if ! ss -ltn 2>/dev/null | awk '{print $4}' | grep -q ":$CANDIDATE$"; then
      PORT="$CANDIDATE"
      break
    fi
  done

  if [ -z "${PORT:-}" ]; then
    echo "Failed to find free port in range 3000–3100"
    exit 1
  fi
fi

export PORT

if [ "$#" -eq 0 ]; then
  echo "No command provided"
  exit 1
fi

curl -sf -X POST "$SERVER/register" \
  -H "Content-Type: application/json" \
  -d "{\"id\":\"$ID\",\"port\":$PORT}" >/dev/null

heartbeat() {
  while :; do
    sleep 10
    curl -s -X POST "$SERVER/heartbeat?id=$ID" >/dev/null
  done
}

heartbeat &
HB_PID=$!

cleanup() {
  kill "$HB_PID" 2>/dev/null || true
  wait "$HB_PID" 2>/dev/null || true
}
trap cleanup EXIT

exec "$@"
