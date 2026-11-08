#!/usr/bin/env bash
# Local development: PostgreSQL in Docker (data in ./data/pg), tildoc "all"
# with the built web UI. Configuration from deploy/.env if present.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p data/pg
[ -f deploy/.env ] && set -a && . deploy/.env && set +a
PGPORT=${DEV_PGPORT:-55434}
if ! docker inspect -f '{{.State.Running}}' tildoc-dev-pg 2>/dev/null | grep -q true; then
  docker rm -f tildoc-dev-pg >/dev/null 2>&1 || true
  docker run -d --name tildoc-dev-pg -p 127.0.0.1:$PGPORT:5432 -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=tildoc \
    -v "$PWD/data/pg:/var/lib/postgresql" postgres:18-alpine >/dev/null
  for i in $(seq 1 60); do docker exec tildoc-dev-pg pg_isready -U postgres -d tildoc >/dev/null 2>&1 && break; sleep 0.5; done
  sleep 1
fi
export TILDOC_DATABASE_URL=${TILDOC_DATABASE_URL:-postgres://postgres:dev@127.0.0.1:$PGPORT/tildoc?sslmode=disable}
if [ -z "${TILDOC_MASTER_KEY:-}" ]; then
  [ -f data/master.key ] || head -c 32 /dev/urandom | base64 >data/master.key
  export TILDOC_MASTER_KEY=$(cat data/master.key)
fi
export TILDOC_DATA_DIR=${TILDOC_DATA_DIR:-$PWD/data}
export TILDOC_UI_DIR=${TILDOC_UI_DIR:-$PWD/web/build}
export TILDOC_COOKIE_SECURE=${TILDOC_COOKIE_SECURE:-false}
export TILDOC_PUBLIC_URL=${TILDOC_PUBLIC_URL:-http://localhost:8080}
export TILDOC_S3_PUBLIC_URL=${TILDOC_S3_PUBLIC_URL:-http://localhost:9000}
exec go run ./cmd/tildoc all
