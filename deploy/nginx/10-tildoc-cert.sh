#!/bin/sh
# Generates a self-signed certificate on first start if none is mounted.
# Replace /etc/nginx/certs/{fullchain,privkey}.pem with real certificates
# (e.g. Let's Encrypt) for production.
set -eu
dir=/etc/nginx/certs
if [ ! -s "$dir/fullchain.pem" ] || [ ! -s "$dir/privkey.pem" ]; then
  mkdir -p "$dir"
  name="${TILDOC_SERVER_NAME:-localhost}"
  echo "tildoc: generating self-signed certificate for $name"
  openssl req -x509 -newkey rsa:3072 -sha256 -days 825 -nodes \
    -keyout "$dir/privkey.pem" -out "$dir/fullchain.pem" \
    -subj "/CN=$name" -addext "subjectAltName=DNS:$name,DNS:${TILDOC_OFFICE_SERVER_NAME:-office.localhost},DNS:localhost,IP:127.0.0.1" 2>/dev/null
  chmod 600 "$dir/privkey.pem"
fi
