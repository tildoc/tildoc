#!/usr/bin/env bash
# Runs the UI smoke test against a fresh dev instance (scripts/dev.sh setup).
# Stops a server on :8080, recreates the dev database, starts tildoc, runs the test.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
for pid in $(ss -ltnp 2>/dev/null | grep -E ':8080 ' | grep -oE 'pid=[0-9]+' | cut -d= -f2); do kill "$pid"; done
sleep 1
(cd web && npm run build >/dev/null)
docker exec tildoc-dev-pg psql -U postgres -c "DROP DATABASE IF EXISTS tildoc WITH (FORCE)" -c "CREATE DATABASE tildoc" >/dev/null
rm -rf data/store
scripts/dev.sh >data/dev.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null; for pid in $(ss -ltnp 2>/dev/null | grep -E ":8080 " | grep -oE "pid=[0-9]+" | cut -d= -f2); do kill $pid; done' EXIT
for i in $(seq 1 120); do curl -sf localhost:8080/api/health >/dev/null && break; sleep 0.5; done
cd test/ui && [ -d node_modules ] || npm install --no-audit --no-fund >/dev/null
node smoke.mjs
