# Headnet build and development tasks.
#
# `make help` lists everything. The targets here are the same ones CI runs, so
# a green local run means a green pipeline; anything CI does that this file
# cannot is a bug in this file.
#
# Windows does not ship make. scripts/dev.ps1 mirrors these targets for
# PowerShell; see docs/development/setup.md.

SHELL := /bin/sh
.DEFAULT_GOAL := help

# Build metadata stamped into the binaries. VERSION falls back to a
# git describe, so a developer build still reports a truthful revision.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

VERSION_PKG := github.com/headnet/headnet/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).version=$(VERSION) \
	-X $(VERSION_PKG).commit=$(COMMIT) \
	-X $(VERSION_PKG).buildDate=$(BUILD_DATE)

BIN_DIR := bin
GO_PACKAGES := ./...

# cgo is off so the binaries are static and cross-compile from one machine.
# The SQLite driver is pure Go precisely so this is possible.
export CGO_ENABLED ?= 0

.PHONY: help
help: ## Show this help
	@echo "Headnet development tasks:"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo

# ---------------------------------------------------------------------------
# Development
# ---------------------------------------------------------------------------

.PHONY: dev
dev: ## Run the control plane against a local SQLite database
	@echo "Starting the Headnet control plane on http://localhost:8080"
	@echo "Health: curl http://localhost:8080/health"
	HEADNET_LOG_FORMAT=text go run ./apps/server

.PHONY: dev-web
dev-web: ## Run the web UI dev server (proxies the API to :8080)
	pnpm --filter @headnet/web dev

.PHONY: install
install: ## Install frontend dependencies
	pnpm install --frozen-lockfile

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: build-server build-cli ## Build every binary for the host platform

.PHONY: build-server
build-server: ## Build the control-plane server
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/headnet-server ./apps/server

.PHONY: build-cli
build-cli: ## Build the command-line interface
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/headnet ./apps/cli

.PHONY: build-web
build-web: ## Build the web UI into apps/web/dist
	pnpm --filter @headnet/web build

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BIN_DIR) apps/web/dist coverage.out coverage.html

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

.PHONY: test
test: test-go test-web ## Run every test suite

.PHONY: test-go
test-go: ## Run the Go tests
	go test -count=1 $(GO_PACKAGES)

.PHONY: test-race
test-race: ## Run the Go tests under the race detector (needs a C toolchain)
	CGO_ENABLED=1 go test -race -count=1 $(GO_PACKAGES)

.PHONY: test-web
test-web: ## Run the frontend tests
	pnpm --filter @headnet/web test

.PHONY: cover
cover: ## Produce a Go coverage report at coverage.html
	go test -coverprofile=coverage.out -covermode=atomic $(GO_PACKAGES)
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

# ---------------------------------------------------------------------------
# Static analysis
# ---------------------------------------------------------------------------

.PHONY: lint
lint: lint-go lint-web ## Run every linter

.PHONY: lint-go
lint-go: ## Run go vet and golangci-lint
	go vet $(GO_PACKAGES)
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint is not installed; skipping (see docs/development/setup.md)"

.PHONY: lint-web
lint-web: ## Lint and type-check the frontend
	pnpm --filter @headnet/web lint
	pnpm --filter @headnet/web check

.PHONY: fmt
fmt: ## Format Go and frontend sources
	gofmt -w .
	pnpm --filter @headnet/web format

.PHONY: fmt-check
fmt-check: ## Fail if anything is unformatted
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

.PHONY: vuln
vuln: ## Scan for known vulnerabilities in Go dependencies
	@command -v govulncheck >/dev/null 2>&1 \
		|| go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck $(GO_PACKAGES)

.PHONY: tidy
tidy: ## Tidy the Go module and verify dependencies
	go mod tidy
	go mod verify

# ---------------------------------------------------------------------------
# Aggregate
# ---------------------------------------------------------------------------

.PHONY: check
check: fmt-check lint test ## Everything CI runs; run this before pushing

.PHONY: ci
ci: fmt-check lint-go test-go vuln ## The Go half of the pipeline

# ---------------------------------------------------------------------------
# Containers
# ---------------------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build the server container image
	docker build -f deploy/docker/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t headnet/headnet:$(VERSION) -t headnet/headnet:latest .

.PHONY: compose-up
compose-up: ## Start the stack with Docker Compose
	docker compose -f deploy/compose/docker-compose.yml up --build

.PHONY: compose-down
compose-down: ## Stop the Docker Compose stack
	docker compose -f deploy/compose/docker-compose.yml down
