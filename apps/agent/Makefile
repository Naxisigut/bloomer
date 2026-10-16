GO ?= go
VERSION ?= 0.2.0-dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf '%s' '-dirty')

.PHONY: check test race build
check:
	$(GO) vet ./...
	$(GO) test ./...
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
build:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags '-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)' -o bin/bloomer-agent ./cmd/bloomer-agent
