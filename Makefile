SQLC_VERSION := v1.31.1
GOOSE_VERSION := v3.27.1
APP_MIGRATIONS := scripts/migrations/app
GO := env GOCACHE=$(CURDIR)/.cache/go-build go

.PHONY: sqlc sqlc-vet migrate-up migrate-down migrate-status \
	fmt-check vet test test-race frontend-check extension-check check

sqlc:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate

sqlc-vet:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) vet

migrate-up:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(TETRA_DATABASE_URL)" up

migrate-down:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(TETRA_DATABASE_URL)" down

migrate-status:
	$(GO) run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir $(APP_MIGRATIONS) postgres "$(TETRA_DATABASE_URL)" status

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
