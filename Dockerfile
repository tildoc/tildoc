# syntax=docker/dockerfile:1
# Targets:
#   server  – REST API, WebDAV, S3, SFTP (+ web UI for single-container use)
#   worker  – background processing incl. poppler + OCR (ocrmypdf/tesseract)
#   nginx   – TLS termination, serves the web UI, proxies API/WebDAV/S3

FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tildoc ./cmd/tildoc

FROM debian:trixie-slim AS base
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --user-group --home-dir /data --no-create-home tildoc \
 && mkdir -p /data && chown tildoc:tildoc /data
COPY --from=build /out/tildoc /usr/local/bin/tildoc
ENV TILDOC_DATA_DIR=/data
VOLUME /data
ENTRYPOINT ["tildoc"]

FROM base AS worker
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ocrmypdf tesseract-ocr-deu tesseract-ocr-eng \
 && rm -rf /var/lib/apt/lists/*
USER tildoc
CMD ["worker"]

FROM nginx:1.29-alpine AS nginx
RUN apk add --no-cache openssl
COPY deploy/nginx/tildoc.conf.template /etc/nginx/templates/default.conf.template
COPY deploy/nginx/10-tildoc-cert.sh /docker-entrypoint.d/10-tildoc-cert.sh
COPY --from=web /web/build /usr/share/nginx/html
RUN chmod +x /docker-entrypoint.d/10-tildoc-cert.sh && rm -f /etc/nginx/conf.d/default.conf
EXPOSE 443 9443

FROM base AS server
COPY --from=web /web/build /srv/ui
ENV TILDOC_UI_DIR=/srv/ui
USER tildoc
EXPOSE 8080 9000 2222
CMD ["serve"]
