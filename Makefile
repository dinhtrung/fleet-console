# fleet-console build and packaging.
#
# `make build` embeds the built board so the binary serves it with no dist/.
# `make release` is the reproducible artifact install.sh verifies: fixed
# metadata and a deterministic tarball.

VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
RELEASE_COMMIT ?= release
RELEASE_TIME ?= 2026-10-28T17:00:00Z

BINARY := fleet-console
GOFLAGS := -trimpath -tags embedboard
LDVARS := -s -w -X main.version=$(VERSION)
ARTIFACT_ARCH := $(shell uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
RELEASE := $(BINARY)-$(VERSION)-linux-$(ARTIFACT_ARCH).tar.gz

.PHONY: all web build test release clean

all: build

web:
	npm ci
	npm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -R dist/. internal/webui/dist/

build: web
	CGO_ENABLED=0 go build $(GOFLAGS) \
		-ldflags "$(LDVARS) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)" \
		-o $(BINARY) ./cmd/fleet-console

test:
	CGO_ENABLED=0 go test ./...

release: web
	CGO_ENABLED=0 go build $(GOFLAGS) \
		-ldflags "$(LDVARS) -X main.commit=$(RELEASE_COMMIT) -X main.buildTime=$(RELEASE_TIME)" \
		-o $(BINARY) ./cmd/fleet-console
	tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
		-cf $(BINARY)-$(VERSION)-linux-$(ARTIFACT_ARCH).tar $(BINARY)
	gzip -9 -n -f $(BINARY)-$(VERSION)-linux-$(ARTIFACT_ARCH).tar
	sha256sum $(RELEASE) > $(RELEASE).sha256
	cat $(RELEASE).sha256

clean:
	rm -f $(BINARY) $(BINARY)-*.tar $(BINARY)-*.tar.gz $(BINARY)-*.sha256
	rm -rf internal/webui/dist
