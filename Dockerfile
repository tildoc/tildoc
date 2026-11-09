# syntax=docker/dockerfile:1
# Targets:
#   server  – REST API, WebDAV, S3, SFTP (+ web UI for single-container use)
#   worker  – background processing incl. poppler + OCR (ocrmypdf/tesseract)
#   nginx   – TLS termination, serves the web UI, proxies API/WebDAV/S3
#   test    – Go toolchain + the worker's document tools (make test-docker)

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

# Tesseract from source: Debian ships 5.5.0; the release tarball is pinned
# by SHA-256. Installed to /usr/local, which comes first in PATH, so
# ocrmypdf uses it; Debian's language data (tesseract-ocr-deu/eng) is used.
FROM debian:trixie-slim AS tesseract
ARG TESSERACT_VERSION=5.5.4
ARG TESSERACT_SHA256=a8dbf8b8c8ee427ff6ad1d99116502feaf59440af1adb893caf60a6576826cf4
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl build-essential autoconf automake libtool pkg-config \
      libleptonica-dev libarchive-dev \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL -o /tmp/tesseract.tar.gz "https://github.com/tesseract-ocr/tesseract/archive/refs/tags/${TESSERACT_VERSION}.tar.gz" \
 && echo "${TESSERACT_SHA256}  /tmp/tesseract.tar.gz" | sha256sum -c - \
 && tar -xzf /tmp/tesseract.tar.gz -C /tmp \
 && cd "/tmp/tesseract-${TESSERACT_VERSION}" \
 && ./autogen.sh >/dev/null \
 && ./configure --prefix=/usr/local --disable-graphics --disable-doc --without-curl >/dev/null \
 && make -j"$(nproc)" >/dev/null \
 && make install >/dev/null \
 && rm -rf /tmp/tesseract* /usr/local/share/tessdata/*.traineddata

FROM debian:trixie-slim AS base
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 10001 --user-group --home-dir /data --no-create-home tildoc \
 && mkdir -p /data /backups && chown tildoc:tildoc /data /backups && chmod 700 /backups
COPY --from=build /out/tildoc /usr/local/bin/tildoc
ENV TILDOC_DATA_DIR=/data TILDOC_BACKUP_DIR=/backups
VOLUME /data /backups
ENTRYPOINT ["tildoc"]

FROM base AS worker
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ocrmypdf tesseract-ocr-deu tesseract-ocr-eng \
      libleptonica6 libarchive13t64 libgomp1 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=tesseract /usr/local/ /usr/local/
RUN ldconfig && tesseract --version | head -1
ENV TESSDATA_PREFIX=/usr/share/tesseract-ocr/5/tessdata
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

FROM golang:1.27-trixie AS test
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ocrmypdf tesseract-ocr-deu tesseract-ocr-eng \
      libleptonica6 libarchive13t64 libgomp1 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=tesseract /usr/local/ /usr/local/
RUN ldconfig
ENV TESSDATA_PREFIX=/usr/share/tesseract-ocr/5/tessdata
WORKDIR /src
