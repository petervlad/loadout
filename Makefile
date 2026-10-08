comma   := ,
GO      ?= go
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt vuln check clean

build: ## Build ./bin/loadout
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/loadout ./cmd/loadout

test: ## Run tests with the race detector
	$(GO) test -race -cover ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format code
	golangci-lint fmt

vuln: ## Check for reachable known vulnerabilities
	$(GO) tool govulncheck ./...

check: fmt lint test vuln ## Everything CI runs

clean:
	rm -rf bin

.PHONY: release-snapshot help

release-snapshot: ## Build all release archives locally into ./dist (no publish; SBOMs only with syft)
	HOMEBREW_TAP_TOKEN=unused goreleaser release --snapshot --clean --skip=publish$(if $(shell command -v syft),,$(comma)sbom)

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'
