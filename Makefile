# tildoc developer tasks
GO      ?= go
BIN     := bin/tildoc
VERSION := $(shell git describe --tags --long --always --dirty 2>/dev/null || echo dev)
TESTPG  := postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable

.PHONY: build web test test-docker e2e vet clean

build:
	$(GO) build -trimpath -ldflags "-X github.com/tildoc/tildoc/internal/version.Version=$(VERSION)" -o $(BIN) ./cmd/tildoc

web:
	cd web && npm ci && npm run build

vet:
	$(GO) vet ./...

# unit + integration tests (starts a PostgreSQL container via Docker)
test:
	TILDOC_TEST_REQUIRE_DB=1 $(GO) test -count=1 ./...

# all tests incl. OCR inside a container with poppler/ocrmypdf/tesseract
# the Tesseract version built in the Dockerfile
TESSERACT_VERSION := $(shell sed -n 's/^ARG TESSERACT_VERSION=//p' Dockerfile)

test-docker:
	TILDOC_TEST_REQUIRE_DB=1 $(GO) test -count=1 ./internal/db/ >/dev/null  # ensures the PostgreSQL test container
	docker build -q --target test -t tildoc-test .
	docker run --rm tildoc-test tesseract --version 2>&1 | grep -q "tesseract $(TESSERACT_VERSION)" || { echo "test image lacks tesseract $(TESSERACT_VERSION)"; exit 1; }
	docker run --rm --network host -v $(CURDIR):/src -v tildoc-gocache:/root/go \
		-e TILDOC_TEST_DATABASE_URL='$(TESTPG)' -e TILDOC_TEST_REQUIRE_DB=1 -e TILDOC_TEST_REQUIRE_TOOLS=1 \
		tildoc-test go test -count=1 ./...

# real clients (aws-cli, rclone s3/webdav/sftp) against a running build
e2e:
	test/e2e/run.sh

clean:
	rm -rf bin
