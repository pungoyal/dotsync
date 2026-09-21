MODULE  := $(shell go list -m)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/dotsync.Version=$(VERSION)

.PHONY: build test lint snapshot clean

build: ## build bin/dotsync
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/dotsync ./cmd/dotsync

test: ## vet + race-enabled tests
	go vet ./...
	go test -race -count=1 ./...

lint: ## golangci-lint + shellcheck
	golangci-lint run ./...
	shellcheck install.sh scripts/*.sh packaging/*/*.sh

snapshot: ## local release build of every platform into dist/ (no publishing)
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
