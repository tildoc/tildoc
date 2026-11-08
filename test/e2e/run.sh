#!/usr/bin/env bash
# End-to-end test with real clients (aws-cli, rclone s3/webdav/sftp) in
# Docker against a locally built tildoc ("all" mode) and a throwaway
# PostgreSQL container. Usage: test/e2e/run.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="$(mktemp -d)"
PG=tildoc-e2e-pg
PGPORT=55433
HTTP=127.0.0.1:18080
S3=127.0.0.1:19000
SFTP=127.0.0.1:12222
PASS=e2e-password-1
PID=""

cleanup() {
  [ -n "$PID" ] && kill "$PID" 2>/dev/null && wait "$PID" 2>/dev/null || true
  docker rm -f "$PG" >/dev/null 2>&1 || true
  if [ "${KEEP:-}" = "" ]; then rm -rf "$WORK"; else echo "work dir: $WORK"; fi
}
trap cleanup EXIT

step() { printf '\n\033[1;34m== %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*"; [ -f "$WORK/server.log" ] && tail -40 "$WORK/server.log"; exit 1; }

step "build"
(cd "$ROOT" && go build -o "$WORK/tildoc" ./cmd/tildoc)

step "postgres"
docker rm -f "$PG" >/dev/null 2>&1 || true
docker run -d --rm --name "$PG" -p 127.0.0.1:$PGPORT:5432 -e POSTGRES_PASSWORD=e2e -e POSTGRES_DB=tildoc \
  --tmpfs /var/lib/postgresql:rw postgres:18-alpine >/dev/null
for i in $(seq 1 60); do docker exec "$PG" pg_isready -U postgres -d tildoc >/dev/null 2>&1 && break; sleep 0.5; done
sleep 1

export TILDOC_DATABASE_URL="postgres://postgres:e2e@127.0.0.1:$PGPORT/tildoc?sslmode=disable"
export TILDOC_MASTER_KEY="$(head -c 32 /dev/urandom | base64)"
export TILDOC_DATA_DIR="$WORK/data"
export TILDOC_HTTP_ADDR=$HTTP TILDOC_S3_ADDR=$S3 TILDOC_SFTP_ADDR=$SFTP
export TILDOC_COOKIE_SECURE=false TILDOC_LOG_LEVEL=debug

step "start tildoc"
echo "$PASS" | "$WORK/tildoc" user create -username alice -admin >/dev/null
"$WORK/tildoc" all >"$WORK/server.log" 2>&1 &
PID=$!
for i in $(seq 1 50); do curl -sf "http://$HTTP/api/health" >/dev/null && break; sleep 0.2; done
curl -sf "http://$HTTP/api/health" >/dev/null || fail "server did not start"

step "login + access key"
TOKEN=$(curl -sf -X POST "http://$HTTP/api/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"alice\",\"password\":\"$PASS\"}" | sed -E 's/.*"token":"([^"]+)".*/\1/')
KEYJSON=$(curl -sf -X POST "http://$HTTP/api/me/access-keys" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"label":"e2e"}')
AK=$(echo "$KEYJSON" | sed -E 's/.*"id":"([^"]+)".*/\1/')
SK=$(echo "$KEYJSON" | sed -E 's/.*"secret":"([^"]+)".*/\1/')
[ ${#AK} -eq 20 ] && [ ${#SK} -eq 40 ] || fail "access key: $KEYJSON"

mkdir -p "$WORK/src/Scans/2024" "$WORK/src/Notes"
head -c 12000000 /dev/urandom >"$WORK/src/Scans/2024/big.bin"     # multipart for aws-cli (>8MB)
echo "hello from aws" >"$WORK/src/Notes/hello.txt"
printf 'Übergrößenträger – ünïcödé\n' >"$WORK/src/Notes/ümlaut name.txt"
cp "$WORK/src/Notes/hello.txt" "$WORK/src/Notes/duplicate.txt"
chmod -R a+rwX "$WORK/src"

AWS=(docker run --rm --network host -v "$WORK:/w" -e AWS_ACCESS_KEY_ID="$AK" -e AWS_SECRET_ACCESS_KEY="$SK"
     -e AWS_DEFAULT_REGION=us-east-1 amazon/aws-cli --endpoint-url "http://$S3")

step "aws-cli: ls / sync / cp / mv / rm / presign"
"${AWS[@]}" s3 ls | grep -q alice || fail "aws s3 ls"
"${AWS[@]}" s3 sync /w/src s3://alice/aws >/dev/null || fail "aws sync"
"${AWS[@]}" s3 ls --recursive s3://alice/aws/ | tee "$WORK/ls.txt"
grep -q "aws/Scans/2024/big.bin" "$WORK/ls.txt" || fail "multipart object missing"
grep -q "ümlaut name.txt" "$WORK/ls.txt" || fail "unicode key missing"
"${AWS[@]}" s3 cp s3://alice/aws/Scans/2024/big.bin /w/big.down >/dev/null || fail "aws download"
cmp "$WORK/src/Scans/2024/big.bin" "$WORK/big.down" || fail "multipart content differs"
"${AWS[@]}" s3 mv s3://alice/aws/Notes/hello.txt s3://alice/aws/Notes/renamed.txt >/dev/null || fail "aws mv"
"${AWS[@]}" s3 cp s3://alice/aws/Notes/renamed.txt - | grep -q "hello from aws" || fail "aws cat after mv"
"${AWS[@]}" s3api put-object --bucket alice --key aws/api.txt --body /w/src/Notes/hello.txt --checksum-algorithm CRC32 >/dev/null || fail "put-object crc32"
"${AWS[@]}" s3api head-object --bucket alice --key aws/api.txt >/dev/null || fail "head-object"
URL=$("${AWS[@]}" s3 presign s3://alice/aws/api.txt --expires-in 300)
curl -sf "$URL" | grep -q "hello from aws" || fail "presigned GET"
"${AWS[@]}" s3 rm s3://alice/aws/api.txt >/dev/null || fail "aws rm"
"${AWS[@]}" s3 sync /w/src s3://alice/aws --delete >/dev/null || fail "aws sync 2"

step "dedup check (duplicate.txt and hello.txt share one document)"
SEARCH=$(curl -sf "http://$HTTP/api/search?q=hello" -H "Authorization: Bearer $TOKEN")
echo "$SEARCH" | grep -q '"link_count":2' || fail "expected deduplicated document with 2 links: $SEARCH"

step "aws-cli: object tagging maps to tildoc tags"
"${AWS[@]}" s3api put-object-tagging --bucket alice --key aws/Notes/duplicate.txt \
  --tagging '{"TagSet":[{"Key":"Steuer 2025","Value":""},{"Key":"Belege","Value":""}]}' || fail "put-object-tagging"
"${AWS[@]}" s3api get-object-tagging --bucket alice --key aws/Notes/duplicate.txt | tee "$WORK/tags.json"
grep -q '"Steuer 2025"' "$WORK/tags.json" && grep -q '"Belege"' "$WORK/tags.json" || fail "get-object-tagging"
curl -sf "http://$HTTP/api/tags" -H "Authorization: Bearer $TOKEN" | grep -q '"name":"Steuer 2025","color":"","count":1' \
  || fail "tag not visible in the API"

RC=(docker run --rm --network host --user "$(id -u):$(id -g)" -v "$WORK:/w" -e RCLONE_CONFIG=/w/rclone.conf rclone/rclone)
cat >"$WORK/rclone.conf" <<EOF
[s3]
type = s3
provider = Other
access_key_id = $AK
secret_access_key = $SK
endpoint = http://$S3
region = us-east-1
directory_markers = true

[dav]
type = webdav
url = http://$HTTP/dav
vendor = other
user = alice
pass = $(docker run --rm rclone/rclone obscure "$SK")

[sftp]
type = sftp
host = ${SFTP%:*}
port = ${SFTP#*:}
user = alice
pass = $(docker run --rm rclone/rclone obscure "$SK")
shell_type = none
known_hosts_file = none
EOF

for R in s3 dav sftp; do
  step "rclone $R: copy / check / moveto / mkdir / rmdir / purge"
  "${RC[@]}" copy /w/src "$R:alice/rc-$R" || fail "rclone $R copy"
  "${RC[@]}" check /w/src "$R:alice/rc-$R" --one-way || fail "rclone $R check"
  "${RC[@]}" moveto "$R:alice/rc-$R/Notes/hello.txt" "$R:alice/rc-$R/Notes/moved.txt" || fail "rclone $R moveto"
  "${RC[@]}" cat "$R:alice/rc-$R/Notes/moved.txt" | grep -q "hello from aws" || fail "rclone $R cat"
  "${RC[@]}" mkdir "$R:alice/rc-$R/EmptyDir" || fail "rclone $R mkdir"
  "${RC[@]}" lsf --dirs-only "$R:alice/rc-$R" | grep -q "EmptyDir/" || fail "rclone $R empty dir not listed"
  "${RC[@]}" rmdir "$R:alice/rc-$R/EmptyDir" || fail "rclone $R rmdir"
  "${RC[@]}" lsf --dirs-only "$R:alice/rc-$R" | grep -q "EmptyDir/" && fail "rclone $R rmdir had no effect"
  # overwrite -> new version
  echo "second version, longer" >"$WORK/v2.txt"
  "${RC[@]}" copyto /w/v2.txt "$R:alice/rc-$R/Notes/moved.txt" || fail "rclone $R overwrite"
  "${RC[@]}" cat "$R:alice/rc-$R/Notes/moved.txt" | grep -q "second version" || fail "rclone $R overwrite content"
  "${RC[@]}" purge "$R:alice/rc-$R" || fail "rclone $R purge"
done

step "versions kept on overwrite (sftp)"
"${RC[@]}" copyto /w/src/Notes/hello.txt sftp:alice/ver.txt
"${RC[@]}" copyto /w/v2.txt sftp:alice/ver.txt
ROOTF=$(curl -sf "http://$HTTP/api/spaces" -H "Authorization: Bearer $TOKEN" | sed -E 's/.*"root_folder_id":"([^"]+)".*/\1/')
curl -sf "http://$HTTP/api/folders/$ROOTF" -H "Authorization: Bearer $TOKEN" | grep -q '"name":"ver.txt"[^}]*"version":2' \
  || fail "expected version 2 for ver.txt"

step "collections are read-only folders in WebDAV and SFTP"
DOC=$(curl -sf "http://$HTTP/api/search?q=ver.txt" -H "Authorization: Bearer $TOKEN" | sed -E 's/.*"document_id":"([^"]+)".*/\1/')
COLL=$(curl -sf -X POST "http://$HTTP/api/collections" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Steuer 2025"}' | sed -E 's/.*"id":"([^"]+)".*/\1/')
curl -sf -X POST "http://$HTTP/api/collections/$COLL/items" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"document_ids\":[\"$DOC\"]}" >/dev/null || fail "add to collection"
for R in dav sftp; do
  "${RC[@]}" lsf "$R:" | grep -q "@collections/" || fail "rclone $R: /@collections missing"
  "${RC[@]}" lsf "$R:@collections/Steuer 2025" | grep -qx "ver.txt" || fail "rclone $R: collection listing"
  "${RC[@]}" cat "$R:@collections/Steuer 2025/ver.txt" | grep -q "second version" || fail "rclone $R: collection read"
  "${RC[@]}" copyto /w/v2.txt "$R:@collections/Steuer 2025/new.txt" 2>/dev/null && fail "rclone $R: wrote into a collection"
done

step "worker processed text files"
for i in $(seq 1 30); do
  curl -sf "http://$HTTP/api/search?q=Übergrößenträger" -H "Authorization: Bearer $TOKEN" | grep -q '"total":[1-9]' && break
  sleep 0.5
done
curl -sf "http://$HTTP/api/search?q=Übergrößenträger" -H "Authorization: Bearer $TOKEN" | grep -q '"total":[1-9]' \
  || fail "full text search did not find extracted text"

step "export / import / backup via CLI and REST"
"$WORK/tildoc" export -user alice -space alice -format zip -o "$WORK/alice.zip" >/dev/null || fail "cli zip export"
python3 -m zipfile -t "$WORK/alice.zip" >/dev/null || fail "zip archive is not valid"
python3 -m zipfile -l "$WORK/alice.zip" | grep -q "alice/ver.txt" || fail "zip lacks the shown structure"
if command -v unzip >/dev/null; then unzip -tq "$WORK/alice.zip" >/dev/null || fail "unzip test"; fi
curl -sf -o "$WORK/rest.tildoc" "http://$HTTP/api/spaces/alice/export?format=bundle" -H "Authorization: Bearer $TOKEN" \
  || curl -sf -o "$WORK/rest.tildoc" "http://$HTTP/api/spaces/$(curl -sf "http://$HTTP/api/spaces" -H "Authorization: Bearer $TOKEN" | sed -E 's/.*"id":"([^"]+)","slug":"alice".*/\1/')/export?format=bundle" -H "Authorization: Bearer $TOKEN" \
  || fail "rest bundle export"
tar -tf "$WORK/rest.tildoc" >"$WORK/bundle.list" || fail "bundle is not a tar archive"
[ "$(sed -n 1p "$WORK/bundle.list")" = "manifest.json" ] || fail "bundle must start with manifest.json"
"$WORK/tildoc" import -user alice -space-name "Kopie" "$WORK/rest.tildoc" | tee "$WORK/import.json"
grep -q '"blobs_new": 0' "$WORK/import.json" || fail "re-import on the same server stored content again"
TR=$(curl -sf -X POST "http://$HTTP/api/transfers?kind=import_bundle&space_name=Rest" -H "Authorization: Bearer $TOKEN" \
  --data-binary "@$WORK/rest.tildoc" | sed -E 's/.*"id":"([^"]+)".*/\1/')
for i in $(seq 1 60); do curl -sf "http://$HTTP/api/transfers/$TR" -H "Authorization: Bearer $TOKEN" | grep -q '"status":"done"' && break; sleep 0.5; done
curl -sf "http://$HTTP/api/transfers/$TR" -H "Authorization: Bearer $TOKEN" | grep -q '"status":"done"' || fail "import transfer"
TILDOC_BACKUP_DIR="$WORK/backups" "$WORK/tildoc" backup full >"$WORK/full.path" || fail "backup full"
echo "after the full backup" | "$WORK/tildoc" user create -username dora >/dev/null
TILDOC_BACKUP_DIR="$WORK/backups" "$WORK/tildoc" backup differential >"$WORK/diff.path" || fail "backup differential"
"$WORK/tildoc" backup verify "$(cat "$WORK/full.path")" "$(cat "$WORK/diff.path")" | grep -q '"users": 2' || fail "backup verify"

grep -q "panic" "$WORK/server.log" && fail "server log contains panic"
printf '\n\033[1;32mE2E PASSED\033[0m\n'
