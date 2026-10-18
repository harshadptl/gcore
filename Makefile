## gcore — common tasks. Run `make` to list them.

BIN      := bin/app
GO       := go
TEMPL    := $(GO) tool templ

.DEFAULT_GOAL := help
.PHONY: help setup generate dev run build fmt vet test test-pg migrate migrate-down migrate-status seed \
        docker-up docker-down rename clean

help: ## Show available commands
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

setup: ## First run: fetch modules (writes go.sum), create .env, generate templates
	$(GO) mod tidy
	@[ -f .env ] || (cp .env.example .env && echo "created .env from .env.example")
	$(TEMPL) generate

generate: ## Compile .templ files to Go
	$(TEMPL) generate

dev: ## Live reload at http://localhost:7331 (restarts on .go/.templ/.css/.js changes)
	$(TEMPL) generate --watch --proxy="http://localhost:3000" --open-browser=false \
		--watch-pattern='(.+\.go$$)|(.+\.templ$$)|(.+_templ\.txt$$)|(web/static/.+\.(css|js)$$)' \
		--cmd="$(GO) run ./cmd/app serve"

run: generate ## Run the server once
	$(GO) run ./cmd/app serve

build: generate ## Build a production binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/app

fmt: ## Format Go and templ files
	$(TEMPL) fmt internal/view
	gofmt -w cmd internal web

vet: generate ## Static checks
	$(GO) vet ./...

test: generate ## Run tests (SQLite in-memory)
	$(GO) test ./...

test-pg: generate ## Run tests against PostgreSQL: make test-pg PG=postgres://... (schema is wiped!)
	@test -n "$(PG)" || (echo "usage: make test-pg PG=postgres://user:pass@localhost:5432/blog_test?sslmode=disable" && exit 1)
	TEST_DATABASE_URL="$(PG)" $(GO) test ./...

migrate: ## Apply pending migrations
	$(GO) run ./cmd/app migrate up

migrate-down: ## Roll back the last migration group
	$(GO) run ./cmd/app migrate down

migrate-status: ## Show migration status
	$(GO) run ./cmd/app migrate status

seed: ## Insert demo users and posts
	$(GO) run ./cmd/app seed

docker-up: ## Start app + PostgreSQL with Docker Compose
	docker compose up --build -d
	@echo "→ http://localhost:3000"

docker-down: ## Stop Docker Compose services
	docker compose down

rename: ## Rename the Go module: make rename MODULE=github.com/you/blog
	@test -n "$(MODULE)" || (echo "usage: make rename MODULE=github.com/you/blog" && exit 1)
	$(GO) mod edit -module $(MODULE)
	grep -rl 'github.com/harshadptl/gcore' --include='*.go' --include='*.templ' . | xargs sed -i.bak 's#github.com/harshadptl/gcore#$(MODULE)#g'
	find . -name '*.bak' -delete
	$(TEMPL) generate
	@echo "Module renamed to $(MODULE)"

clean: ## Remove build output and generated dev files
	rm -rf bin tmp
	find . -name '*_templ.txt' -delete
