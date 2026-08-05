# Local and untagged builds report a development version. Release builds
# are driven by GoReleaser, which injects the Git tag and build metadata.
.DEFAULT_GOAL := help

GO ?= go
PKG ?= ./cmd/zot
BINARY ?= zot
BIN_DIR ?= bin
DEV_VERSION ?= 0.0.0-dev
VERSION ?= $(DEV_VERSION)
GO_INSTALL_VERSION ?= latest
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help build install go-install test test-fast vet lint fmt-check fmt check run clean release

help:
	@printf '%s\n' \
		'build       build ./bin/zot' \
		'install     install the current checkout with go install' \
		'go-install  install a published module version (GO_INSTALL_VERSION=latest)' \
		'test        run all tests with the race detector' \
		'test-fast   run all tests without the race detector' \
		'vet         run go vet ./...' \
		'lint        run vet and verify gofmt' \
		'fmt         format Go source files' \
		'check       run test-fast and lint' \
		'run         build and run ./bin/zot (pass ARGS="...")' \
		'clean       remove build output' \
		'release     cross-build release binaries (set VERSION=...)'

build:
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)" $(PKG)

# Install the current checkout. With the default VERSION this is a dev build.
install:
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

# Install a published/module version, rather than the current checkout.
go-install:
	$(GO) install github.com/patriceckhart/zot/cmd/zot@$(GO_INSTALL_VERSION)

test:
	$(GO) test -race ./...

test-fast:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt issues"; exit 1)

lint: vet fmt-check

check: test-fast lint

fmt:
	gofmt -w .

run: build
	"$(BIN_DIR)/$(BINARY)" $(ARGS)

clean:
	rm -rf "$(BIN_DIR)"

release:
	@if [ "$(VERSION)" = "$(DEV_VERSION)" ] || [ "$(VERSION)" = "0.0.0" ] || printf '%s\n' "$(VERSION)" | grep -Eq '(^|[.-])[0-9]{14}-[A-Za-z0-9]+'; then \
		echo "set VERSION to a release version, for example: make release VERSION=0.1.0" >&2; \
		exit 1; \
	fi
	@mkdir -p "$(BIN_DIR)"
	GOOS=linux   GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)-linux-amd64" $(PKG)
	GOOS=linux   GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)-linux-arm64" $(PKG)
	GOOS=darwin  GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)-darwin-amd64" $(PKG)
	GOOS=darwin  GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)-darwin-arm64" $(PKG)
	GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BINARY)-windows-amd64.exe" $(PKG)
