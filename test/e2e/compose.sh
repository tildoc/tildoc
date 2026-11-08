#!/usr/bin/env bash
# Full-stack test of the docker compose deployment: nginx (TLS) -> server,
# worker with OCR, PostgreSQL. Real clients over HTTPS/SFTP.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
P=tildoc-e2e
HTTPS=18443 S3P=19443 SFTPP=12223
ENVF="$WORK/.env"
DC=(docker compose -p $P -f "$ROOT/deploy/docker-compose.yml" --env-file "$ENVF")

cleanup() {
  [ "${KEEP:-}" = "" ] && "${DC[@]}" down -v >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
step() { printf '\n\033[1;34m== %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*"; "${DC[@]}" logs --tail 40 server worker nginx; exit 1; }

cat >"$ENVF" <<ENV
TILDOC_MASTER_KEY=$(head -c 32 /dev/urandom | base64)
POSTGRES_PASSWORD=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
TILDOC_SERVER_NAME=localhost
TILDOC_PUBLIC_URL=https://localhost:$HTTPS
TILDOC_S3_PUBLIC_URL=https://localhost:$S3P
TILDOC_SFTP_PUBLIC_ADDR=localhost:$SFTPP
TILDOC_HTTPS_PORT=$HTTPS
TILDOC_S3_PORT=$S3P
TILDOC_SFTP_PORT=$SFTPP
ENV

step "compose up"
"${DC[@]}" up -d --build --wait --wait-timeout 180 >/dev/null || fail "compose up"
B="https://localhost:$HTTPS"
# nginx has no healthcheck; it listens only after generating its certificate
for i in $(seq 1 60); do curl -skf "$B/api/health" 2>/dev/null | grep -q ok && break; sleep 0.5; done
curl -skf "$B/api/health" | grep -q ok || fail "health via nginx"

step "UI via nginx (SPA fallback + CSP)"
curl -skf "$B/f/whatever" | grep -q 'content-security-policy' || fail "SPA fallback/CSP"
curl -skI "$B/" | grep -qi 'strict-transport-security' || fail "HSTS"

step "pdf.js assets served with module/wasm MIME types, CSP allows worker + wasm"
WJS=$("${DC[@]}" exec -T nginx sh -c 'ls /usr/share/nginx/html/_app/immutable/assets/ | grep pdf.worker' | tr -d '\r')
[ -n "$WJS" ] || fail "pdf.js worker missing from the build"
curl -skI "$B/_app/immutable/assets/$WJS" | grep -qi '^content-type: text/javascript' || fail "worker .mjs MIME"
curl -skI "$B/vendor/pdfjs/wasm/openjpeg.wasm" | grep -qi '^content-type: application/wasm' || fail "wasm MIME"
curl -skI "$B/vendor/fonts/atkinson-hyperlegible-next-latin-wght-normal.woff2" | grep -qi '^content-type: font/woff2' || fail "font"
curl -skf "$B/" | grep -q "wasm-unsafe-eval" || fail "CSP lacks wasm-unsafe-eval"

step "setup + access key"
curl -skf -X POST "$B/api/setup" -H 'Content-Type: application/json' -d '{"username":"admin","password":"supersecret"}' -c "$WORK/c" >/dev/null || fail setup
TOKEN=$(curl -skf -X POST "$B/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"admin","password":"supersecret"}' | sed -E 's/.*"token":"([^"]+)".*/\1/')
KJ=$(curl -skf -X POST "$B/api/me/access-keys" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"label":"e2e"}')
AK=$(echo "$KJ" | sed -E 's/.*"id":"([^"]+)".*/\1/'); SK=$(echo "$KJ" | sed -E 's/.*"secret":"([^"]+)".*/\1/')

(cd "$ROOT" && go run ./test/e2e/mkpdf "$WORK/pdf")
chmod -R a+rwX "$WORK"

step "aws-cli over TLS (nginx -> S3)"
AWS=(docker run --rm --network host -v "$WORK:/w" -e AWS_ACCESS_KEY_ID="$AK" -e AWS_SECRET_ACCESS_KEY="$SK"
     -e AWS_DEFAULT_REGION=us-east-1 amazon/aws-cli --endpoint-url "https://localhost:$S3P" --no-verify-ssl)
"${AWS[@]}" s3 cp /w/pdf/scanned.pdf "s3://admin/Belege/2026/scan 01.pdf" 2>/dev/null >/dev/null || fail "aws put via nginx"
"${AWS[@]}" s3 ls --recursive s3://admin/ 2>/dev/null | grep -q "Belege/2026/scan 01.pdf" || fail "aws ls via nginx"

step "rclone WebDAV over TLS (Host/Destination through nginx)"
RC=(docker run --rm --network host --user "$(id -u):$(id -g)" -v "$WORK:/w" -e RCLONE_CONFIG=/w/rclone.conf rclone/rclone)
cat >"$WORK/rclone.conf" <<CONF
[dav]
type = webdav
url = https://localhost:$HTTPS/dav
vendor = other
user = admin
pass = $(docker run --rm rclone/rclone obscure "$SK")

[sftp]
type = sftp
host = localhost
port = $SFTPP
user = admin
pass = $(docker run --rm rclone/rclone obscure "$SK")
shell_type = none
known_hosts_file = none
CONF
"${RC[@]}" --no-check-certificate copy /w/pdf "dav:admin/Verträge" || fail "dav copy"
"${RC[@]}" --no-check-certificate moveto "dav:admin/Verträge/digital.pdf" "dav:admin/Verträge/Mietvertrag.pdf" || fail "dav move"
"${RC[@]}" --no-check-certificate lsf "dav:admin/Verträge" | grep -q "Mietvertrag.pdf" || fail "dav list"

step "rclone SFTP"
"${RC[@]}" lsf "sftp:admin/Verträge" | grep -q "Mietvertrag.pdf" || fail "sftp list"

step "dedup across protocols (scanned.pdf via S3 and WebDAV = one document)"
curl -skf "$B/api/search?q=scanned.pdf" -H "Authorization: Bearer $TOKEN" | grep -q '"link_count":2' || fail "expected dedup"

step "worker OCR (scanned PDF becomes searchable)"
for i in $(seq 1 90); do
  R=$(curl -skf "$B/api/search?q=RECEIPT" -H "Authorization: Bearer $TOKEN")
  echo "$R" | grep -q '"total":[1-9]' && break
  sleep 2
done
echo "$R" | grep -q '"total":[1-9]' || fail "OCR text not searchable"
DOC=$(echo "$R" | sed -E 's/.*"document_id":"([^"]+)".*/\1/')
curl -skf "$B/api/documents/$DOC" -H "Authorization: Bearer $TOKEN" | grep -q '"has_searchable":true' || fail "no searchable PDF"
curl -skf -o "$WORK/searchable.pdf" "$B/api/documents/$DOC/content?variant=searchable" -H "Authorization: Bearer $TOKEN" || fail "download searchable"
head -c 5 "$WORK/searchable.pdf" | grep -q '%PDF' || fail "searchable is not a PDF"
curl -skI "$B/api/documents/$DOC/content?inline=1" -H "Authorization: Bearer $TOKEN" | grep -qi '^content-security-policy: sandbox' || fail "inline PDF not sandboxed"
curl -skf "$B/api/search?q=RECEIPT" -H "Authorization: Bearer $TOKEN" | grep -q '"page":1' || fail "search hit page"
curl -skf "$B/api/search?q=Altona" -H "Authorization: Bearer $TOKEN" | grep -q '"total":1' || fail "digital PDF text"

printf '\n\033[1;32mCOMPOSE E2E PASSED\033[0m\n'
