# Developer entry points. Run `make help` for the list of targets.

GO              ?= go
GOLANGCI_LINT   ?= golangci-lint
GOVULNCHECK_VER ?= v1.9.0
PKGS            ?= ./...
COVER_PROFILE   ?= coverage.out
BENCH           ?= .
BENCH_COUNT     ?= 6
FUZZTIME        ?= 30s
PYTHON          ?= python3
# package:FuzzTarget pairs run by `make fuzz`.
FUZZ_TARGETS    := ./solarpos:FuzzPosition ./irradiance:FuzzTranspose
OPENMETEO_URL   := https://api.open-meteo.com/v1/forecast?latitude=50.0647&longitude=19.945&hourly=shortwave_radiation,direct_normal_irradiance,diffuse_radiation,temperature_2m,wind_speed_10m&wind_speed_unit=ms&timezone=UTC&forecast_days=3

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build
build: ## Build bin/pvforecast and bin/pvforecast-api with the version stamped in.
	$(GO) build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o bin/pvforecast ./cmd/pvforecast
	$(GO) build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o bin/pvforecast-api ./cmd/pvforecast-api

.PHONY: docker
docker: ## Build the API container image from source (pvforecast-api:local).
	docker build --build-arg VERSION=$(VERSION) -t pvforecast-api:local .

.PHONY: docker-run
docker-run: docker ## Run the API container on :8080.
	docker run --rm -p 8080:8080 -e LOG_FORMAT=text pvforecast-api:local

LAMBDA_DIR      := deploy/aws
LAMBDA_FUNCTION ?= pvforecast-api

.PHONY: lambda-zip
lambda-zip: ## Build deploy/aws/build/bootstrap.zip (linux/arm64) for AWS Lambda.
	rm -rf $(LAMBDA_DIR)/build && mkdir -p $(LAMBDA_DIR)/build
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath \
		-ldflags '-s -w -X main.version=$(VERSION)' -o $(LAMBDA_DIR)/build/bootstrap ./cmd/pvforecast-api
	cd $(LAMBDA_DIR)/build && zip -q -X bootstrap.zip bootstrap

.PHONY: lambda-deploy
lambda-deploy: lambda-zip ## Upload new code to the Lambda function (uses your AWS credentials).
	aws lambda update-function-code --function-name $(LAMBDA_FUNCTION) \
		--zip-file fileb://$(LAMBDA_DIR)/build/bootstrap.zip --output text --query 'LastUpdateStatus'
	aws lambda wait function-updated-v2 --function-name $(LAMBDA_FUNCTION)

.PHONY: tf-check
tf-check: ## Check Terraform formatting, validity and lint (no AWS credentials needed).
	terraform -chdir=$(LAMBDA_DIR) fmt -check -recursive
	terraform -chdir=$(LAMBDA_DIR) init -backend=false -input=false >/dev/null
	terraform -chdir=$(LAMBDA_DIR) validate
	cd $(LAMBDA_DIR) && tflint

.PHONY: serve
serve: build ## Run the HTTP API on :8080 with text logs.
	LOG_FORMAT=text ./bin/pvforecast-api

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

.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME each.
	@set -e; for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "fuzz $$pkg $$name"; \
		$(GO) test $$pkg -run='^$$' -fuzz="^$$name\$$" -fuzztime=$(FUZZTIME); \
	done

.PHONY: fixtures
fixtures: ## Regenerate pvlib reference fixtures (needs Python 3 and tools/fixtures/requirements.txt).
	$(PYTHON) -I tools/fixtures/gen.py --out internal/testdata

.PHONY: record
record: ## Record a live Open-Meteo response for Kraków (needs network access).
	curl -fsS '$(OPENMETEO_URL)' -o internal/testdata/openmeteo/recorded_krakow.json

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
