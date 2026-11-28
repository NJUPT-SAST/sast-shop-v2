# AGENTS.md

This file provides guidance for AI coding agents working in the SAST Shop v2 backend repository.

## Project Overview

SAST Shop v2 is a campus commerce microservice backend for NJUPT, built with Go 1.26. The Go workspace (`go.work`, gitignored and generated via `make proto`) contains one shared library (`internal/pkg`) and six independently runnable services communicating via Connect-RPC.

## Common Commands

```bash
# Regenerate Go code from proto and initialize/update go.work
make proto
make proto-lint
make proto-format

# Build / run services (each service searches upward for repo-root .env)
make build
make run-userservice       # or catalogservice, paymentservice, spotservice, errandservice, westpocketservice
make run-all               # starts local Caddy gateway (:6660) + all 6 services

# Code quality & module hygiene
make lint
make lint-fix
make tidy

# Database migrations (requires DATABASE_URL)
make migrate               # initial schema: migrations/001_init.sql (fresh DB only)
make migrate-west-pocket   # additive migration: migrations/002_west_pocket.sql
# Incremental migrations (e.g. migrations/003_spot_goods_delisting.sql) apply via psql directly

# Tests
go test -race -count=1 ./internal/pkg/...
go test -race -count=1 ./internal/service/userservice/... ./internal/service/catalogservice/... ./internal/service/paymentservice/... ./internal/service/spotservice/... ./internal/service/errandservice/... ./internal/service/westpocketservice/...
```

Each service needs PostgreSQL and Redis running. Copy `.env.example` to `.env` and fill in local credentials; never commit `.env` or real secrets.

## Module Map

| Module | Directory | Port (env var) | Domain |
|---|---|---|---|
| `internal/pkg` | `internal/pkg/` | — | Shared library |
| `userservice` | `internal/service/userservice/` | `USER_SERVICE_PORT` (1323) | Feishu SSO login, user profiles, shipping addresses |
| `catalogservice` | `internal/service/catalogservice/` | `CATALOG_SERVICE_PORT` (1324) | Stores, product templates, barcodes, product image uploads |
| `paymentservice` | `internal/service/paymentservice/` | `PAYMENT_SERVICE_PORT` (1325) | Bills, QR codes, payment confirmation, West Pocket bill coordination |
| `spotservice` | `internal/service/spotservice/` | `SPOT_SERVICE_PORT` (1326) | Instant-buy spot goods, stock ledger, orders |
| `errandservice` | `internal/service/errandservice/` | `ERRAND_SERVICE_PORT` (1327) | Group-buy errands: demands → tasks → distribution → payment |
| `westpocketservice` | `internal/service/westpocketservice/` | `WEST_POCKET_SERVICE_PORT` (1328) | Consented mobile AA collection, face matching, durable outbox |

All six services import `internal/pkg`. Never add direct Go imports between services — cross-service calls go through generated Connect-RPC clients (`*Internal` services).

## Architecture

### Request Flow

```
Client → Echo v5 router (e.Any(path+"*"))
       → protovalidate + logging interceptor chain
       → auth interceptor (session token in Redis, or X-Dev-User-ID bypass when APP_ENV=development)
       → internal token interceptor (X-West-Pocket-Token for West Pocket internal RPCs)
       → Handler (implements generated Connect-RPC interface)
         → Service layer (business logic)
           → Repository layer (uptrace/bun ORM queries)
             → PostgreSQL
```

### Startup Bootstrap

```go
config.Init()       // Loads .env upward, parses into global AppConfig
logger.Init(name)   // zerolog — console in dev, JSON in prod
postgres.Init()     // uptrace/bun DB global
redis.Init(name)    // go-redis client global with service key prefix (pings Redis on startup)
feishu.Init()       // Feishu SDK client (panics on missing creds in prod; conditional in westpocketservice)
e := echo.New()
v1.Init(e)          // Registers Connect-RPC handlers and HTTP endpoints (catalogservice also calls cos.Init())
e.Start(":port")    // westpocketservice wires internal/{handler/v1,model,repository,service,support} with graceful http.Server shutdown
```

### Shared Library (`internal/pkg/`)

- `admin/` & `adminflow/` — Admin RBAC auth checks, fund/correction queries, mutation audit helpers, and whole-order correction flows.
- `bun/postgres/` — PostgreSQL connection (`uptrace/bun` + `pgdriver`).
- `config/` — env-var parsing via `caarlos0/env/v11` + `godotenv`. Global `AppConfig *Config`.
- `connect/interceptor/` — validation+logging chain, `AuthRequired` (Bearer session token + dev `X-Dev-User-ID` bypass), and `west_pocket` internal token check.
- `constant/` — service names, session TTL, header names.
- `cos/` — Tencent Cloud COS client & object helpers for product images via public CDN (skips when unconfigured in `development`, panics in `production`; West Pocket manages its own private COS client in `westpocketservice`).
- `errmsg/` — domain error message helpers per service.
- `feishu/` — Feishu OAuth, JSAPI ticket/signature, IM notifications, and caching.
- `idgen/` — Snowflake ID generator (`SAST_SHOP_IDGEN_WORKER_ID`).
- `logger/` — zerolog setup (console in dev, JSON in prod).
- `redis/` — go-redis client, `SessionStore` (key prefix `session:`), upload limiter & quota.
- `rpcerror/` — `NewInternalError` helper wrapping protobuf `BusinessError` oneofs.
- `search/` — literal substring keyword normalization (`NormalizeKeyword`, trims whitespace, max 200 Unicode runes).
- `testutil/` — disposable PostgreSQL database harness for integration tests.
- `timeutil/` — `updated_at` optimistic concurrency helpers.

### Error Pattern

Each domain defines error codes in its proto file (`proto/sast/sastshopv2/{domain}/v1/`). Handlers wrap business errors via `rpcerror`:

```go
func userError(code userv1.UserErrorCode, msg string) *connect.Error {
    return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
        UserError: &userv1.UserError{Code: code, Message: msg},
    }, "")
}
```

### Database & Migrations

- All models use `uptrace/bun` with schema-qualified tables across 6 PostgreSQL schemas: `user`, `catalog`, `payment`, `spot`, `errand`, `westpocket`.
- Migrations live in `migrations/` (`001_init.sql` through `010_store_product_categories.sql`, covering West Pocket, spot delisting, `zhparser` full-text search, admin RBAC/corrections, and two-level store product categories). Keep migrations additive for existing databases; never modify `001_init.sql` in a way that breaks incremental rollouts without a matching migration.
- Money is always stored and computed as integer cents (`int32` in Go models and protos, `INTEGER` in PostgreSQL).

### Protobuf & Code Generation

- Proto files live in `proto/sast/sastshopv2/{common,admin,user,catalog,payment,spot,errand,westpocket}/v1/`.
- Generated code (`gen/protocolbuffers/go` and `gen/connectrpc/go`) is gitignored and linked via `go.work`. After editing any `.proto` file, run `make proto-lint && make proto` and verify all affected services build and pass tests. When updating Go module dependencies locally before new protos are published to BSR, use `go get ... && go work sync` instead of `go mod tidy` (since `go mod tidy` ignores `go.work`).

### Security & Privacy Constraints

- `X-Dev-User-ID` bypass is strictly gated to `APP_ENV=development`; never relax production auth checks.
- `*InternalService` endpoints must not be exposed on public gateways. West Pocket internal RPCs require `X-West-Pocket-Token` (stripped at public gateways).
- Never log plaintext QR snapshots, private COS signed URLs, encryption keys (`WEST_POCKET_ENCRYPTION_KEY`), or cloud/Feishu credentials. See `docs/README.md` (`docs/west-pocket.md`, `docs/admin.md`, `docs/deployment-ci.md`) for West Pocket, admin RBAC, and CI/deployment contracts.

## Testing

- Unit and handler tests exist across `internal/pkg` and all six services (run with `go test -race -count=1`).
- PostgreSQL integration tests create and drop isolated temporary databases when configured:
  - Search/catalog/spot integration tests use `SAST_SHOP_TEST_POSTGRES_URL`.
  - West Pocket integration tests use `WEST_POCKET_TEST_DSN`.
  - Never point test env vars at a production database. Without them, DB-backed integration tests skip cleanly while unit/validation tests still run.

## AI Skills

Project-level agent skills are pinned in `skills-lock.json` (`connectrpc`, core `golang-*` skills, and `supabase-postgres-best-practices`) and installed into `.agents/skills/` (gitignored). Restore locally with:

```bash
pnpm dlx skills experimental_install
# or: npx skills experimental_install
```
