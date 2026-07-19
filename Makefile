SERVICES := userservice catalogservice paymentservice spotservice errandservice
WORKSPACE_MODULES := ./internal/pkg $(addprefix ./internal/service/,$(SERVICES))
PROTOBUF_GEN_DIR := gen/protocolbuffers/go
CONNECT_GEN_DIR := gen/connectrpc/go

.PHONY: lint lint-fix build run-% proto proto-lint proto-format migrate setup tidy

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
	@test -f $(PROTOBUF_GEN_DIR)/go.mod || \
		(cd $(PROTOBUF_GEN_DIR) && go mod init buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go)
	@test -f $(CONNECT_GEN_DIR)/go.mod || \
		(cd $(CONNECT_GEN_DIR) && go mod init buf.build/gen/go/sast/sast-shop-v2/connectrpc/go)
	@test -f go.work || go work init
	go work use $(WORKSPACE_MODULES) ./$(PROTOBUF_GEN_DIR) ./$(CONNECT_GEN_DIR)

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
