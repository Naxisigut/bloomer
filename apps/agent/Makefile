GO ?= go
VERSION ?= 1.0.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf '%s' '-dirty')

.PHONY: check test race build binary package dmg
check:
	$(GO) vet ./...
	$(GO) test ./...
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
build: dmg
package: dmg
dmg: binary
	$(GO) run ./cmd/bloomer-package -root ../.. -version '$(VERSION)'
binary:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags '-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)' -o bin/bloomer-agent ./cmd/bloomer-agent
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags '-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)' -o bin/bloomerctl ./cmd/bloomerctl
