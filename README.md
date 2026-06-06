# SAST Shop v2

[中文版](./README_CN.md)

Campus commerce backend for NJUPT (Nanjing University of Posts and Telecommunications), built with Go microservices.

## Tech Stack

| Technology | Purpose |
|---|---|
| Go 1.26 | Language |
| Connect-RPC | API framework (HTTP/gRPC) |
| Echo v5 | HTTP router |
| PostgreSQL + bun | Primary database |
| Redis + go-redis | Session & cache |
| Buf + protovalidate | Proto management & validation |
| zerolog | Structured logging |
| Feishu (Lark) | SSO authentication |

## Architecture

```
Client → Echo v5 → protovalidate interceptor → auth interceptor → Handler
                                                                    ↓
                                                              Service layer
                                                                    ↓
                                                         Repository (bun ORM)
                                                                    ↓
                                                               PostgreSQL
```

The workspace contains one shared library and five independently deployable microservices, each running on its own port. Services communicate via Connect-RPC — there are no direct Go imports between services.

### Services

| Service | Directory | Port | Description |
|---|---|---|---|
| `internal/pkg` | `internal/pkg/` | — | Shared library (config, logger, DB, Redis, interceptors) |
| User | `internal/service/userservice/` | 1323 | Feishu SSO login, user profiles, shipping addresses |
| Catalog | `internal/service/catalogservice/` | 1324 | Stores, product templates, barcodes |
| Payment | `internal/service/paymentservice/` | 1325 | Bills, QR codes, payment confirmation |
| Spot | `internal/service/spotservice/` | 1326 | Instant-buy goods, inventory, orders |
| Errand | `internal/service/errandservice/` | 1327 | Group-buy errands: demands → tasks → distribution → payment |

### Project Structure

```
sast-shop-v2/
├── proto/sast/sastshopv2/    # Protobuf API definitions (5 domains)
│   ├── common/v1/            # Shared error types (BusinessError oneof)
│   ├── user/v1/              # Auth, User, Address services
│   ├── catalog/v1/           # Store, ProductTemplate services
│   ├── payment/v1/           # Bill, QrCode services
│   ├── spot/v1/              # SpotGoods, SpotOrder services
│   └── errand/v1/            # Errand demand/task/distribution/order services
├── gen/                      # Generated .pb.go + .connect.go (gitignored)
├── buf.yaml                  # Buf module config
├── buf.gen.yaml              # Code generation plugins
├── go.work                   # Go workspace (6 modules)
├── .env.example              # Environment template
├── .golangci.yml             # Linter configuration
└── internal/
    ├── pkg/                  # Shared library
    │   ├── config/           # Env-var config (caarlos0/env + godotenv)
    │   ├── logger/           # zerolog setup (console in dev, JSON in prod)
    │   ├── bun/postgres/     # PostgreSQL connection (bun + pgdriver)
    │   ├── redis/            # Redis client + session store
    │   ├── connect/interceptor/  # Auth + validation interceptors
    │   ├── constant/         # Service names, session TTL, header names
    │   └── rpcerror/         # Business error helper
    └── service/
        ├── userservice/      # cmd/app/main.go + internal/{handler,model,repository,service}
        ├── catalogservice/
        ├── paymentservice/
        ├── spotservice/
        └── errandservice/
```

## API Overview

The API exposes **46 RPC methods** across 14 service definitions. Key workflows:

- **Auth**: Login via Feishu code → session token → stored in Redis (30min TTL)
- **Spot trade**: Seller publishes goods → Buyer creates order → Payment bill created → Pay → Confirm → Complete
- **Errand (group trade)**: Buyer posts demand → Captain creates task → Shopping → Distribute → Collect payment → Complete
- **Cross-service**: Internal endpoints (suffixed `*Internal`) handle service-to-service calls (e.g., Payment calls Errand's `OnPaymentConfirmed` when bills clear)

## Database

19 tables across 5 PostgreSQL schemas:

| Schema | Tables |
|---|---|
| `user` | `user_account`, `auth_session`, `member_address` |
| `catalog` | `catalog_store`, `catalog_product_template`, `catalog_product_barcode`, `catalog_product_image` |
| `payment` | `payment_qr_code`, `payment_bill`, `payment_confirmation_log` |
| `spot` | `spot_goods`, `spot_stock_ledger`, `spot_order` |
| `errand` | `errand_demand`, `errand_demand_item`, `errand_task`, `errand_task_item`, `errand_task_assignment`, `errand_price_change_log`, `errand_action_log` |

All models use `uptrace/bun` ORM with schema-qualified table names.

## Getting Started

### Prerequisites

- Go 1.26+
- PostgreSQL
- Redis
- [Buf CLI](https://buf.build/docs/cli/)

### Setup

```bash
# Clone and enter the repo
git clone git@github.com:NJUPT-SAST/sast-shop-v2.git && cd sast-shop-v2

# Copy environment template and fill in your values
cp .env.example .env

# Generate Go code from proto definitions
buf generate

# Ensure PostgreSQL and Redis are running, then start any service
cd internal/service/userservice && go run ./cmd/app
```

Each service loads `.env` by searching upward from its working directory, so you can run services from their respective `cmd/app` directories.

### Development

```bash
# Regenerate proto after changing .proto files
buf generate

# Lint all workspace modules
make lint
make lint-fix       # with auto-fix

# Run a single service (from repo root)
go -C internal/service/userservice run ./cmd/app
```

### Dev Auth Bypass

In development mode (`APP_ENV=development`), you can bypass authentication by setting the `X-Dev-User-ID` header with a valid user ID.

## Current Status

This project is in early development. Currently implemented:

- **User service**: `GetUserInfo`, `GetUsers` (internal) — real database queries
- **All other handlers**: Return placeholder "To be implemented" errors

No tests exist yet. Contributions are welcome.

## License

MIT
