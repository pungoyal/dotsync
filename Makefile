MODULE  := $(shell go list -m)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/dotsync.Version=$(VERSION)

# Versions of tools that `make` runs with `go run`, in step with .github/workflows/ci.yml.
ACTIONLINT_VERSION ?= v1.7.12
FUZZTIME           ?= 30s

# $(call need,tool): stop with a hint when a development tool is not installed.
need = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is not installed. Install it with your package manager, or run 'mise install' for every tool pinned in mise.toml"; exit 1; }

.PHONY: build test lint fmt-check actionlint vulncheck docs fuzz min-go ci ci-full reference snapshot clean

build: ## build bin/dotsync
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/dotsync ./cmd/dotsync

test: ## vet + race-enabled tests
	go vet ./...
	go test -race -count=1 ./...

reference: ## regenerate the documentation site's generated reference (website/src/data/reference.json)
	go test ./internal/dotsync -run TestWebsiteReference -update-reference

lint: ## golangci-lint + shellcheck
	$(call need,golangci-lint)
	golangci-lint run ./...
	$(call need,shellcheck)
	shellcheck install.sh scripts/*.sh packaging/*/*.sh

fmt-check: ## fail if any Go file is not gofmt-ed
	@test -z "$$(gofmt -l .)" || { gofmt -d .; exit 1; }

actionlint: ## lint the GitHub workflows
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) -color

vulncheck: ## known vulnerabilities in the code's dependencies and the standard library
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

docs: ## build the documentation site, which checks its internal links
	$(call need,npm)
	cd website && npm ci && npm run build

fuzz: ## every fuzz target for FUZZTIME each (default 30s), as CI does
	@for t in $$(grep -ho '^func Fuzz[A-Za-z0-9_]*' internal/dotsync/*_test.go | cut -d' ' -f2); do \
		echo "fuzz $$t"; \
		go test -run='^$$' -fuzz="^$$t\$$" -fuzztime=$(FUZZTIME) ./internal/dotsync || exit 1; \
	done

min-go: ## build with the minimum Go version from go.mod (downloads that toolchain once)
	GOTOOLCHAIN=go$$(go mod edit -json | sed -n 's/.*"Go": "\(.*\)".*/\1/p') go build ./...

ci: fmt-check lint actionlint test vulncheck docs ## what CI checks on a pull request, minus the slow jobs
	@echo "ci: ok"

ci-full: ci min-go fuzz snapshot ## ci plus the slow jobs: minimum Go, fuzzing, release snapshot
	@echo "ci-full: ok"

snapshot: ## local release build of every platform into dist/ (no publishing)
	$(call need,goreleaser)
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
