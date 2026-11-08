#!/usr/bin/env bash
# Starts the development PostgreSQL container (data in ./.data/pg) if needed.
set -euo pipefail
cd "$(dirname "$0")/.."
PGPORT=${DEV_PGPORT:-55434}
mkdir -p .data/pg
if ! docker inspect -f '{{.State.Running}}' tildoc-dev-pg 2>/dev/null | grep -q true; then
  docker rm -f tildoc-dev-pg >/dev/null 2>&1 || true
  docker run -d --name tildoc-dev-pg -p 127.0.0.1:$PGPORT:5432 -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=tildoc \
    -v "$PWD/.data/pg:/var/lib/postgresql" postgres:18-alpine >/dev/null
fi
for i in $(seq 1 60); do
  docker exec tildoc-dev-pg pg_isready -U postgres -d tildoc -h 127.0.0.1 >/dev/null 2>&1 && exit 0
  sleep 0.5
done
echo "postgres did not become ready" >&2
exit 1
