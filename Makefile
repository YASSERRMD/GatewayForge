.PHONY: help check-layout deps fmt vet lint lint-go lint-web typecheck \
        test test-go test-race test-web build build-go build-web \
        integration compose-config compose-up compose-down db-reset verify clean

SHELL := /usr/bin/env bash

help: ## Show available GatewayForge targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nGatewayForge Makefile Targets:\n\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

check-layout: ## Verify required monorepo directory layout
	@bash scripts/check-layout.sh

deps: ## Download Go module and web dependencies
	go mod download
	cd apps/web && npm ci --no-audit --no-fund

fmt: ## Format Go sources
	go fmt ./...

vet: ## Run go vet across all Go packages
	go vet ./...

lint-go: ## Run golangci-lint if installed, plus go vet
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found; ran go vet only"; \
	fi

lint-web: ## Run ESLint in apps/web
	cd apps/web && npm run lint

lint: lint-go lint-web ## Run all linters

typecheck: ## Run TypeScript strict typecheck in apps/web
	cd apps/web && npm run typecheck

test-go: ## Run Go unit tests
	go test ./...

test-race: ## Run Go unit tests with the race detector
	go test -race ./...

test-web: ## Run Vitest suite in apps/web
	cd apps/web && npm run test -- --run

test: test-go test-web ## Run unit test suites

build-go: ## Build control-plane, gateway, and mock-upstream binaries into bin/
	@mkdir -p bin
	go build -o bin/control-plane ./cmd/control-plane
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/mock-upstream ./cmd/mock-upstream

build-web: ## Build production Next.js bundle
	cd apps/web && npm run build

build: build-go build-web ## Build all binaries and web app

integration: ## Run Go integration tests against running Compose infrastructure
	INTEGRATION_TESTS=1 go test -v -race ./tests/integration/...

compose-config: ## Validate docker-compose.yml
	docker compose config --quiet

compose-up: ## Start local Docker Compose stack
	docker compose up -d --build --wait

compose-down: ## Stop local Docker Compose stack
	docker compose down

db-reset: ## Reset local development PostgreSQL schema
	@bash scripts/reset-db.sh

verify: check-layout fmt lint typecheck test-race test-web build compose-config ## Run full local quality gate
	@echo "All GatewayForge quality gates passed."

clean: ## Remove build artifacts
	rm -rf bin apps/web/.next apps/web/tsconfig.tsbuildinfo
