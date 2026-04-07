.PHONY: $(shell egrep -o '^[a-zA-Z_/.-]+:' $(MAKEFILE_LIST) | sed 's/://')
SHELL=/bin/bash

help: ## ヘルプを表示する
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z].+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

code/fmt: ## コードのフォーマットを整える
	@go fmt ./...

code/lint: ## コードを静的解析する
	@go tool golangci-lint run --timeout=5m

code/test: ## コードをテストする
	@go test ./...

install: ## 依存関係をダウンロードする
	@go mod download
