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
	@go run ./cmd/repo-tooling docs verify-i18n

integrations/check: ## Verify native integration packages
	@go run ./cmd/repo-tooling integrations verify

release/check: ## Verify release marketplace and plugin manifests
	@python3 scripts/verify_release_manifests.py

landing/check: ## Verify docs/landing/ stays in sync with VERSION
	@go run ./cmd/repo-tooling docs verify-landing

release/gemini-extension: ## Package Gemini CLI extension archive to dist/
	@./scripts/package_gemini_extension.sh

release/snapshot: ## Build snapshot release artifacts to dist/
	@goreleaser release --snapshot --clean

release/bump: ## Bump version across all manifests (usage: make release/bump VERSION=X.Y.Z)
	@test -n "$(VERSION)" || (echo "Usage: make release/bump VERSION=X.Y.Z" >&2 && exit 1)
	@go run ./cmd/repo-tooling release bump-version --version "$(VERSION)"
	@python3 scripts/verify_release_manifests.py
	@go run ./cmd/repo-tooling integrations verify
	@go run ./cmd/repo-tooling docs verify-landing

ci: docs/check release/check integrations/check landing/check code/lint code/test ## Run full CI validation

install: ## Download Go module dependencies
	@go mod download
