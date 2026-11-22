.PHONY: help check-layout

SHELL := /usr/bin/env bash

help: ## Show available GatewayForge targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nGatewayForge Makefile Targets:\n\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

check-layout: ## Verify required monorepo directory layout
	@bash scripts/check-layout.sh
