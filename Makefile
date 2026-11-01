# Developer entry points. Run `make help` for the list of targets.

GO              ?= go
GOLANGCI_LINT   ?= golangci-lint
GOVULNCHECK_VER ?= v1.9.0
PKGS            ?= ./...
COVER_PROFILE   ?= coverage.out
BENCH           ?= .
BENCH_COUNT     ?= 6

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Compile all packages.
	$(GO) build $(PKGS)

.PHONY: test
test: ## Run unit tests.
	$(GO) test $(PKGS)

.PHONY: race
race: ## Run unit tests with the race detector.
	$(GO) test -race $(PKGS)

.PHONY: cover
cover: ## Run tests with coverage and print a per-function summary.
	$(GO) test -race -covermode=atomic -coverprofile=$(COVER_PROFILE) $(PKGS)
	$(GO) tool cover -func=$(COVER_PROFILE)

.PHONY: bench
bench: ## Run benchmarks (feed the output to benchstat to compare commits).
	$(GO) test -run='^$$' -bench='$(BENCH)' -benchmem -count=$(BENCH_COUNT) $(PKGS)

.PHONY: lint
lint: ## Run golangci-lint (linters and formatters).
	$(GOLANGCI_LINT) run

.PHONY: fmt
fmt: ## Format code with the formatters configured in .golangci.yml.
	$(GOLANGCI_LINT) fmt

.PHONY: vet
vet: ## Run go vet.
	$(GO) vet $(PKGS)

.PHONY: vuln
vuln: ## Scan for known vulnerabilities.
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) $(PKGS)

.PHONY: tidy
tidy: ## Check that go.mod is tidy.
	$(GO) mod tidy -diff

.PHONY: check
check: tidy vet lint race ## Run everything CI runs.
