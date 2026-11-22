.PHONY: help dev dev-observability check-layout deps fmt vet lint lint-go lint-web typecheck \
        test test-go test-race test-web security-test e2e load build build-go build-web \
        integration compose-config compose-up compose-down db-reset migrate seed verify clean

SHELL := /usr/bin/env bash

help: ## Show available GatewayForge targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nGatewayForge Makefile Targets:\n\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

dev: ## Start local Docker Compose stack, apply migrations, and seed demo fixtures
	docker compose up -d --build --wait
	$(MAKE) migrate
	$(MAKE) seed
	@echo "GatewayForge ready: Studio http://127.0.0.1:3000 | Control Plane http://127.0.0.1:18080 | Gateway http://127.0.0.1:18081"

dev-observability: ## Start full stack including Prometheus (:19090) and Jaeger (:16686)
	docker compose --profile observability up -d --build --wait
	$(MAKE) migrate
	$(MAKE) seed

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

security-test: ## Run adversarial security, chaos, container, and secret scan checks
	@bash scripts/check-containers.sh
	@bash scripts/check-secrets.sh
	go test -race -v ./tests/security/...

build-go: ## Build control-plane, gateway, migrate, seed, and mock-upstream binaries into bin/
	@mkdir -p bin
	go build -o bin/control-plane ./cmd/control-plane
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/mock-upstream ./cmd/mock-upstream
	go build -o bin/migrate ./cmd/migrate
	go build -o bin/seed ./cmd/seed

build-web: ## Build production Next.js bundle
	cd apps/web && npm run build

build: build-go build-web ## Build all binaries and web app

integration: ## Run Go integration tests against running Compose infrastructure
	INTEGRATION_TESTS=1 go test -v -race ./tests/integration/...

e2e: ## Run Compose-backed Playwright end-to-end suite (serial workers)
	cd apps/web && npx playwright test --workers=1

load: ## Run reproducible Go load benchmark profile against local gateway stack
	go test -v ./tests/load/...

compose-config: ## Validate all Docker Compose profiles
	docker compose config --quiet
	docker compose --profile observability config --quiet
	docker compose --profile test config --quiet

compose-up: ## Start local Docker Compose stack
	docker compose up -d --build --wait

compose-down: ## Stop local Docker Compose stack
	docker compose down

db-reset: ## Reset local development PostgreSQL schema
	@bash scripts/reset-db.sh

migrate: ## Apply SQL migrations to local PostgreSQL
	DATABASE_URL="$${DATABASE_URL:-postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable}" go run ./cmd/migrate up

seed: ## Seed idempotent local demo fixtures into PostgreSQL
	DATABASE_URL="$${DATABASE_URL:-postgres://gatewayforge:gatewayforge_local_dev_pw@127.0.0.1:5439/gatewayforge?sslmode=disable}" go run ./cmd/seed

verify: check-layout fmt lint typecheck test-race test-web build compose-config ## Run full local quality gate
	@echo "All GatewayForge quality gates passed."

clean: ## Remove build artifacts
	rm -rf bin apps/web/.next apps/web/tsconfig.tsbuildinfo
