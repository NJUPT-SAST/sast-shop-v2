SERVICES := userservice catalogservice paymentservice spotservice errandservice

.PHONY: lint lint-fix build run-% proto migrate setup tidy

# --- Development ---

run-%:
	go run ./internal/service/$*/cmd/app

# --- Build ---

build:
	@for svc in $(SERVICES); do \
		echo "Building $$svc..."; \
		go build -o bin/$$svc ./internal/service/$$svc/cmd/app || exit 1; \
	done

# --- Proto ---

proto:
	buf generate

proto-lint:
	buf lint

proto-format:
	buf format --write

# --- Database ---

migrate:
	psql "$$DATABASE_URL" -f migrations/001_init.sql

# --- Code quality ---

lint:
	golangci-lint run $$(go list -f '{{.Dir}}/...' -m | xargs)

lint-fix:
	golangci-lint run --fix $$(go list -f '{{.Dir}}/...' -m | xargs)

tidy:
	@for dir in internal/pkg $(addprefix internal/service/,$(SERVICES)); do \
		echo "Tidying $$dir..."; \
		(cd $$dir && go mod tidy) || exit 1; \
	done

# --- Setup ---

setup:
	pre-commit install
