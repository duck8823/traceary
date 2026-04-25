.PHONY: $(shell egrep -o '^[a-zA-Z_/.-]+:' $(MAKEFILE_LIST) | sed 's/://')
SHELL=/bin/bash

help: ## Show available targets
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z].+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

code/build: ## Build all packages
	@go build ./...

code/fmt: ## Format source code
	@go fmt ./...

code/lint: ## Run static analysis
	@go tool golangci-lint run --timeout=5m

code/test: ## Run all tests
	@go test ./...

code/cover: ## Run tests with coverage report
	@go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@echo 'HTML report: go tool cover -html=coverage.out'

docs/check: ## Verify bilingual documentation pairs
	@python3 scripts/verify_docs_i18n.py

integrations/check: ## Verify native integration packages
	@python3 scripts/verify_integrations.py

release/check: ## Verify release marketplace and plugin manifests
	@python3 scripts/verify_release_manifests.py

release/gemini-extension: ## Package Gemini CLI extension archive to dist/
	@./scripts/package_gemini_extension.sh

release/snapshot: ## Build snapshot release artifacts to dist/
	@goreleaser release --snapshot --clean

release/bump: ## Bump version across all manifests (usage: make release/bump VERSION=X.Y.Z)
	@test -n "$(VERSION)" || (echo "Usage: make release/bump VERSION=X.Y.Z" >&2 && exit 1)
	@python3 scripts/bump_version.py --version "$(VERSION)"
	@python3 scripts/verify_release_manifests.py
	@python3 scripts/verify_integrations.py

ci: docs/check release/check integrations/check code/lint code/test ## Run full CI validation

install: ## Download Go module dependencies
	@go mod download
