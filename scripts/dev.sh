#!/usr/bin/env bash
# Local development: PostgreSQL in Docker (data in ./.data/pg), tildoc "all"
# with the built web UI. Configuration from deploy/.env if present.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -z "${TILDOC_DEV_NO_ENV:-}" ] && [ -f deploy/.env ] && set -a && . deploy/.env && set +a
PGPORT=${DEV_PGPORT:-55434}
scripts/dev-db.sh
export TILDOC_DATABASE_URL=${TILDOC_DATABASE_URL:-postgres://postgres:dev@127.0.0.1:$PGPORT/tildoc?sslmode=disable}
if [ -z "${TILDOC_MASTER_KEY:-}" ]; then
  [ -f .data/master.key ] || head -c 32 /dev/urandom | base64 >.data/master.key
  export TILDOC_MASTER_KEY=$(cat .data/master.key)
fi
export TILDOC_DATA_DIR=${TILDOC_DATA_DIR:-$PWD/.data}
export TILDOC_UI_DIR=${TILDOC_UI_DIR:-$PWD/web/build}
export TILDOC_COOKIE_SECURE=${TILDOC_COOKIE_SECURE:-false}
export TILDOC_PUBLIC_URL=${TILDOC_PUBLIC_URL:-http://localhost:8080}
export TILDOC_S3_PUBLIC_URL=${TILDOC_S3_PUBLIC_URL:-http://localhost:9000}
V=$(git describe --tags --long --always --dirty 2>/dev/null || echo dev)
exec go run -ldflags "-X github.com/tildoc/tildoc/internal/version.Version=$V" ./cmd/tildoc all
