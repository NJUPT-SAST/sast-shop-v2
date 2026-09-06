# SAST Shop v2

[中文版](./README_zh.md)

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
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- [pre-commit](https://pre-commit.com/#install)

### Setup

```bash
# Clone and enter the repo
git clone git@github.com:NJUPT-SAST/sast-shop-v2.git && cd sast-shop-v2

# Copy environment template and fill in your values
cp .env.example .env

# Install git hooks
make setup

# Apply database migrations
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/sast_shop_v2?sslmode=disable
make migrate

# Start a service
make run-userservice
```

Each service loads `.env` by searching upward from its working directory.

### Make Commands

| Command | Description |
|---|---|
| `make run-<service>` | Start a service (`userservice`, `catalogservice`, `paymentservice`, `spotservice`, `errandservice`) |
| `make build` | Compile all services to `bin/` |
| `make proto` | Regenerate Go code from proto and push to BSR |
| `make proto-lint` | Lint proto files |
| `make proto-format` | Format proto files in place |
| `make migrate` | Apply `migrations/001_init.sql` (requires `DATABASE_URL`) |
| `make lint` | Run golangci-lint across all modules |
| `make lint-fix` | Run golangci-lint with auto-fix |
| `make tidy` | Run `go mod tidy` across all modules |
| `make setup` | Install pre-commit hooks (run once after cloning) |

### Dev Auth Bypass

In development mode (`APP_ENV=development`), you can bypass authentication by setting the `X-Dev-User-ID` header with a valid user ID.

## Current Status

This project is in early development. Currently implemented:

- **User service**: `GetUserInfo`, `GetUsers` (internal) — real database queries
- **All other handlers**: Return placeholder "To be implemented" errors

No tests exist yet. Contributions are welcome.

## Deployment

Production deployment is container-based: every push to `main` builds Docker images and pushes them to Tencent Cloud Container Registry (TCR), then upgrades the changed services on the server over SSH.

### Flow

```
push to main
  → detect changed services (changes to internal/pkg, proto/, Dockerfile → all services)
  → build & push ccr.ccs.tencentyun.com/<namespace>/sast-shop-<service>:<short-sha> to TCR
  → SSH: pull image, rotate sast/<service>:current → backup, tag new image as current
  → docker compose up -d --no-deps <service>
```

Manual deploy: Actions → Deploy → Run workflow (optionally pick a single service).

### GitHub configuration

| Kind | Name | Description |
|---|---|---|
| Secret | `TCR_USERNAME` / `TCR_PASSWORD` | TCR credentials for pushing images (long-term token recommended) |
| Secret | `SERVER_HOST` / `SERVER_USER` / `SSH_PRIVATE_KEY` | Deployment server SSH access |
| Variable | `TCR_NAMESPACE` | TCR namespace, e.g. `sast` |
| Variable | `IMAGE_PREFIX` | TCR repository name prefix (default `sast-shop`) |
| Variable | `COMPOSE_DIR` | Server directory holding `docker-compose.yml` (default `/data/sast-shop-v2`) |

### One-time server setup

1. Copy the compose template: `mkdir -p /data/sast-shop-v2 && cp deploy/docker-compose.yml /data/sast-shop-v2/`
2. Create `/data/sast-shop-v2/.env` from `.env.example` with production values. Required:
   - real `REDIS_HOST`/`REDIS_PORT` — services ping Redis at startup and panic on failure
   - `DB_*` database credentials
   - `APP_ENV=production` plus real Feishu credentials (`FEISHU_APP_ID`, `FEISHU_APP_SECRET`) — with `APP_ENV=development` the `X-Dev-User-ID` auth bypass is enabled, which must not happen in production
   - COS variables are optional — catalogservice degrades gracefully without them
3. Apply migrations once against the production database:
   `psql "$DATABASE_URL" -f migrations/001_init.sql`
4. Restrict ports 1323–1327 to loopback on the firewall — services bind `0.0.0.0`; host Caddy reaches them at `127.0.0.1`.
5. Ensure the server has Docker with the Compose v2 plugin.

### Manual rollback

Each deploy rotates the previous image to `sast/<service>:backup`. To roll back:

```bash
cd /data/sast-shop-v2
docker tag sast/userservice:backup sast/userservice:current
docker compose up -d --no-deps userservice
```

> If the TCR repositories are private, log the server in once with `docker login ccr.ccs.tencentyun.com`.

## License

MIT
