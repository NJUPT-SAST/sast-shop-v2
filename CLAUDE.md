# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SAST Shop v2 is a microservice campus commerce backend for NJUPT, built with Go 1.26. The workspace (`go.work`) contains one shared library and five independently runnable services communicating via Connect-RPC.

## Build / Run / Generate / Lint

```bash
# Generate Go from proto (run from repo root)
buf generate

# Lint all workspace modules
make lint
make lint-fix        # with auto-fix

# Run a single service (each service starts its own HTTP server)
cd internal/service/userservice && go run ./cmd/app
```

Each service needs PostgreSQL and Redis running. Copy `.env.example` to `.env` and fill in credentials. The `.env` file is loaded from the repo root (services search upward for it).

## Module Map

| Module | Directory | Port (env var) |
|---|---|---|
| `internal/pkg` (shared lib) | `internal/pkg/` | — |
| `userservice` | `internal/service/userservice/` | `USER_SERVICE_PORT` (1323) |
| `catalogservice` | `internal/service/catalogservice/` | `CATALOG_SERVICE_PORT` (1324) |
| `paymentservice` | `internal/service/paymentservice/` | `PAYMENT_SERVICE_PORT` (1325) |
| `spotservice` | `internal/service/spotservice/` | `SPOT_SERVICE_PORT` (1326) |
| `errandservice` | `internal/service/errandservice/` | `ERRAND_SERVICE_PORT` (1327) |

All five services import `internal/pkg`. There are no direct inter-service Go imports — cross-service calls go through generated Connect-RPC clients.

## Architecture

### Request Flow

```
Client → Echo v5 router (e.Any(path+"*"))
       → protovalidate interceptor (validates request messages)
       → auth interceptor (optional — session token or X-Dev-User-ID bypass)
       → Handler struct (implements generated Connect-RPC interface)
         → Service layer (business logic)
           → Repository layer (bun ORM queries)
             → PostgreSQL
       → Redis (session store, key prefix: "session:")
```

### Startup Bootstrap (identical across all services)

```go
config.Init()       // Loads .env, parses into global AppConfig
logger.Init(name)   // zerolog — console in dev, JSON in prod
postgres.Init()     // bun DB global
redis.Init()        // go-redis client global
e := echo.New()
v1.Init(e)          // Registers all Connect-RPC handlers for this service
e.Start(":port")
```

### Handler Registration

Each service's `internal/handler/v1/handler.go` exposes an `Init(e *echo.Echo)` that:
1. Creates a **validation chain** (protovalidate + logging interceptor) applied to all handlers
2. Creates an **auth interceptor** (session-token based, with dev bypass via `X-Dev-User-ID` header)
3. Registers handler structs by calling `e.Any(path+"*", echo.WrapHandler(mux))`

Auth-protected handlers get both the validation chain and auth interceptor. Internal handlers (service-to-service) get only the validation chain.

### Shared Library (`internal/pkg/`)

- `config/` — env-var parsing via `caarlos0/env` + `godotenv`. Global `AppConfig *Config`.
- `logger/` — zerolog setup. Console writer in dev (`zerolog.ConsoleWriter`), JSON in prod.
- `bun/postgres/` — PostgreSQL connection via `uptrace/bun` + `pgdriver`.
- `redis/` — go-redis client. Also provides `SessionStore` implementation (key prefix `session:`).
- `connect/interceptor/` — `AuthRequired` interceptor (reads Bearer token, validates via SessionStore interface), validation+logging chain.
- `constant/` — service name strings, session TTL, header names.
- `rpcerror/` — `NewInternalError` helper wrapping protobuf `BusinessError` oneofs.

### Error Pattern

Each service defines error codes in its proto file. The handler helper constructs Connect errors:

```go
func userError(code userv1.UserErrorCode, msg string) *connect.Error {
    return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
        UserError: &userv1.UserError{Code: code, Message: msg},
    }, "")
}
```

Error code ranges: common (0–1000), user (2000+), catalog/payment/spot/errand (each with own range).

### Database

All models use `uptrace/bun` with schema-qualified tables (e.g., `user.user_account`, `catalog.catalog_store`, `spot.spot_goods`). Models are in `internal/model/model.go` per service. Repository functions accept `*bun.DB` directly.

### Proto Generation

Proto files live in `proto/sast/sastshopv2/{domain}/v1/`. Generated code (`.pb.go` + `.connect.go`) goes into `gen/` (gitignored). The Buf module is `buf.build/sast/sast-shop-v2` with a dependency on `buf.build/bufbuild/protovalidate`. After changing proto files, run `buf generate`.

### Current State

The project is in early development. Only `userservice` has partial real logic (GetUserInfo, GetByUserIDs). All other handlers return "To be implemented" placeholder errors. No tests exist yet.
