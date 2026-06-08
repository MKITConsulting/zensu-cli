.PHONY: build test vet cover snapshot install

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/MKITConsulting/zensu-cli/internal/version.Version=$(VERSION) \
	-X github.com/MKITConsulting/zensu-cli/internal/version.Commit=$(COMMIT) \
	-X github.com/MKITConsulting/zensu-cli/internal/version.Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/zensu ./cmd/zensu

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/zensu

test:
	go test -race ./...

vet:
	go vet ./...

cover:
	go test -coverprofile=cover.out ./...
	go tool cover -func=cover.out

# Cross-platform snapshot via goreleaser (no publish).
snapshot:
	goreleaser release --snapshot --clean
