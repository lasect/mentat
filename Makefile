SQLC_VERSION := v1.31.1
GOOSE_VERSION := v3.27.1
APP_MIGRATIONS := scripts/migrations/app
COMPOSE_FILE := deploy/compose/postgres.yaml
DEV_DATABASE_URL := postgres://mentat:mentat@localhost:5432/mentat?sslmode=disable
TEST_DATABASE_URL := postgres://mentat:mentat@localhost:5432/mentat_test?sslmode=disable
GO := env GOCACHE=$(CURDIR)/.cache/go-build go

.PHONY: db-up db-down db-logs db-migrate test-integration sqlc sqlc-vet migrate-up migrate-down migrate-status \
	fmt-check vet test test-race frontend-check extension-check check

db-up:
	docker compose -f $(COMPOSE_FILE) up -d --wait
	$(MAKE) db-migrate

db-down:
	docker compose -f $(COMPOSE_FILE) down

db-logs:
	docker compose -f $(COMPOSE_FILE) logs -f postgres

db-migrate:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(DEV_DATABASE_URL)" up
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(TEST_DATABASE_URL)" up

test-integration: db-up
	env MENTAT_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(MAKE) test

sqlc:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

sqlc-vet:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) vet

migrate-up:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(MENTAT_DATABASE_URL)" up

migrate-down:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(MENTAT_DATABASE_URL)" down

migrate-status:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(MENTAT_DATABASE_URL)" status

test:
	$(GO) test ./...

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd cli internal -name '*.go' -type f))"

vet:
	$(GO) vet ./...

test-race:
	$(GO) test -race ./...

frontend-check:
	npm --prefix web/app run typecheck
	npm --prefix web/app run lint
	npm --prefix web/app run build

extension-check:
	cargo fmt --manifest-path extension/Cargo.toml --check
	cargo clippy --manifest-path extension/Cargo.toml --all-targets -- -D warnings
	cargo test --manifest-path extension/Cargo.toml

check: fmt-check vet test test-race sqlc-vet frontend-check extension-check
