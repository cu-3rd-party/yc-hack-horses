# Лимиты сборок: не больше 4 ядер (иначе убивает машины других агентов)
TASKSET ?= taskset -c 0-3
GOMAXPROCS := 4
GOFLAGS += -p=4
export GOMAXPROCS GOFLAGS
GO_BUILD := $(TASKSET) $(GO) build

# Управление
GO ?= go
BACKEND_DIR := backend
APP_DIR := app

.PHONY: build backend test test-backend lint backend-fmt compose-up compose-down compose-logs compose-status

backend: ## сборка ноды
	cd $(BACKEND_DIR) && $(GO_BUILD) -o ../bin/node ./cmd/node

backend-test: ## все тесты backend
	cd $(BACKEND_DIR) && $(TASKSET) $(GO) test ./...

backend-fmt: ## gofumpt
	cd $(BACKEND_DIR) && $(GO) run mvdan.cc/gofumpt@latest -w .

backend-lint:
	cd $(BACKEND_DIR) && $(GO) vet ./... && $(GO) run mvdan.cc/gofumpt@latest -l .

proto: ## генерация protobuf
	cd $(BACKEND_DIR) && $(GO) generate ./api/...

app-install:
	cd $(APP_DIR) && npm ci --cache $$(npm config get cache)

app-build:
	cd $(APP_DIR) && npm run build

compose-up:
	docker compose --env-file .env -f infra/compose/docker-compose.yml up -d

compose-down:
	docker compose --env-file .env -f infra/compose/docker-compose.yml down

compose-logs: ## логи центральной ноды (docker compose)
	docker compose --env-file .env -f infra/compose/docker-compose.yml logs -f node

compose-status: ## статус стеков docker compose
	docker compose --env-file .env -f infra/compose/docker-compose.yml ps

dev-certs: ## dev CA и сертификаты (development-only)
	bash infra/scripts/gen-dev-certs.sh

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "%-16s %s\n", $$1, $$2}'
