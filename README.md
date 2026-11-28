# SAST Shop v2

[中文版](./README_zh.md) · [Documentation](./docs/README.md)

Campus commerce backend for NJUPT (Nanjing University of Posts and Telecommunications), built with Go microservices.

## Tech Stack

| Technology | Purpose |
|---|---|
| Go 1.26 | Language |
| Connect-RPC | API framework (HTTP/gRPC) |
| Echo v5 | HTTP router |
| PostgreSQL + bun | Primary database |
| Redis + go-redis | Session, cache, upload quota |
| Buf + protovalidate | Proto management & validation |
| zerolog | Structured logging |
| Feishu (Lark) | SSO authentication & IM notifications |
| Tencent Cloud COS / IAI | Product images, West Pocket private storage & face recognition |

## Architecture

```
Client → Echo v5 → protovalidate + logging interceptors → auth / internal-token interceptor → Handler
                                                                                                ↓
                                                                                          Service layer
                                                                                                ↓
                                                                                     Repository (bun ORM)
                                                                                                ↓
                                                                                           PostgreSQL
```

The workspace contains one shared library (`internal/pkg`) and **six** independently runnable microservices, each listening on its own port. Services communicate exclusively via Connect-RPC (`*InternalService` definitions) — there are no direct Go imports between services.

### Services

| Service | Directory | Port | Description |
|---|---|---|---|
| `internal/pkg` | `internal/pkg/` | — | Shared library (config, logger, DB, Redis, interceptors, Feishu, COS, search, testutil) |
| User | `internal/service/userservice/` | 1323 | Feishu SSO login, user profiles, shipping addresses |
| Catalog | `internal/service/catalogservice/` | 1324 | Stores, product templates, barcodes, product image uploads |
| Payment | `internal/service/paymentservice/` | 1325 | Bills, QR codes, payment confirmation, West Pocket bill coordination |
| Spot | `internal/service/spotservice/` | 1326 | Instant-buy spot goods, stock ledger, orders |
| Errand | `internal/service/errandservice/` | 1327 | Group-buy errands: demands → tasks → distribution → payment |
| West Pocket | `internal/service/westpocketservice/` | 1328 | Consented mobile AA collection, face matching, durable Feishu outbox |

### Project Structure

```
sast-shop-v2/
├── proto/sast/sastshopv2/    # Protobuf API definitions (7 business domains + common)
│   ├── common/v1/            # Shared error types (BusinessError oneof)
│   ├── admin/v1/             # Admin governance, audit & payment correction internal RPCs
│   ├── user/v1/              # Auth, User, Address, UserInternal services
│   ├── catalog/v1/           # Catalog (Store), ProductCategory, ProductTemplate, CatalogInternal services
│   ├── payment/v1/           # Bill, QrCode, PaymentInternal services
│   ├── spot/v1/              # SpotGoods, SpotOrder services
│   ├── errand/v1/            # ErrandDemand, ErrandTask, BuyerErrandOrder, GroupTradeInternal services
│   └── westpocket/v1/        # FaceProfile, WestPocket, WestPocketInternal services
├── gen/                      # Generated .pb.go + .connect.go (gitignored)
├── migrations/               # SQL schema & additive migrations (001 through 010)
├── deploy/                   # Local gateway config (Caddyfile.local) & PostgreSQL zhparser Dockerfile
├── docs/                     # Operational & feature guides (see docs/README.md)
├── buf.yaml / buf.gen.yaml   # Buf module & codegen config
├── go.work                   # Go workspace (gitignored, generated via make proto; 9 modules)
├── .env.example              # Environment template
├── .golangci.yml             # Linter configuration
└── internal/
    ├── pkg/                  # Shared library
    │   ├── admin/            # Admin RBAC auth, queries, mutations & fund helpers
    │   ├── adminflow/        # Cross-domain admin whole-order correction flows
    │   ├── bun/postgres/     # PostgreSQL connection (bun + pgdriver)
    │   ├── config/           # Env-var config (caarlos0/env/v11 + godotenv)
    │   ├── connect/interceptor/ # Validation, logging, auth & West Pocket internal token
    │   ├── constant/         # Service names, session TTL, header names
    │   ├── cos/              # Tencent Cloud COS product-image client
    │   ├── errmsg/           # Domain error message helpers
    │   ├── feishu/           # Feishu OAuth, JSAPI ticket/signature, IM notifications
    │   ├── idgen/            # Snowflake ID generator
    │   ├── logger/           # zerolog setup (console in dev, JSON in prod)
    │   ├── redis/            # Redis client, session store, upload limiter & quota
    │   ├── rpcerror/         # BusinessError helper
    │   ├── search/           # Keyword normalization (NormalizeKeyword)
    │   ├── testutil/         # Isolated PostgreSQL test harness
    │   └── timeutil/         # Optimistic concurrency (updated_at) helpers
    └── service/
        ├── userservice/      # cmd/app/main.go + internal/{handler,model,repository,service}
        ├── catalogservice/
        ├── paymentservice/
        ├── spotservice/
        ├── errandservice/
        └── westpocketservice/
```

## API Overview

The API defines **98 RPC methods across 19 services** (including internal service-to-service contracts):

- **Auth**: Feishu OAuth code login → Bearer session token stored in Redis (30-minute TTL)
- **Spot trade**: Seller lists goods → Buyer places order → Payment bill created → Pay → Confirm → Complete
- **Errand (group trade)**: Buyer posts demand → Captain creates task → Shopping → Distribute → Collect payment → Complete
- **West Pocket**: Mobile AA split → Optional consented face matching or name search → Frozen QR & bills → Feishu notifications & manual settlement confirmation (see [docs/west-pocket.md](./docs/west-pocket.md))
- **Cross-service**: `*InternalService` endpoints handle service-to-service RPCs and must never be exposed on public gateways

## Database

**31 tables across 6 PostgreSQL schemas** (all models use `uptrace/bun` with schema-qualified table names):

| Schema | Tables |
|---|---|
| `user` (3) | `user_account`, `auth_session`, `member_address` |
| `catalog` (4) | `catalog_store`, `catalog_product_template`, `catalog_product_barcode`, `catalog_product_image` |
| `payment` (3) | `payment_qr_code`, `payment_bill`, `payment_confirmation_log` |
| `spot` (3) | `spot_goods`, `spot_stock_ledger`, `spot_order` |
| `errand` (7) | `errand_demand`, `errand_demand_item`, `errand_task`, `errand_task_item`, `errand_task_assignment`, `errand_price_change_log`, `errand_action_log` |
| `westpocket` (11) | `pocket`, `pocket_member`, `upload`, `pocket_photo`, `face_profile`, `face_sample`, `async_job`, `face_match`, `notification_outbox`, `consent_event`, `request_dedup` |

Migrations live in `migrations/`:

- **Fresh database**: run `make migrate` (`001_init.sql`) followed by incremental migrations (`002_west_pocket.sql` through `010_store_product_categories.sql`).
- **Existing database**: apply unapplied additive migrations (`002`–`010`) via `psql` after backup; never rerun `001_init.sql`. See [docs/admin.md](./docs/admin.md) and [docs/west-pocket.md](./docs/west-pocket.md).

## Getting Started

### Prerequisites

- Go 1.26+
- PostgreSQL & Redis
- [Buf CLI](https://buf.build/docs/cli/)
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- [pre-commit](https://pre-commit.com/#install)
- [Caddy](https://caddyserver.com/) (optional, required for `make run-all`)

### Setup

```bash
# Clone and enter the repo
git clone git@github.com:NJUPT-SAST/sast-shop-v2.git && cd sast-shop-v2

# Copy environment template and fill in local credentials
cp .env.example .env

# Generate protobuf/Connect code and initialize go.work
make proto

# Install git hooks
make setup

# Apply database migrations (fresh DB)
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/sast_shop_v2?sslmode=disable
make migrate
make migrate-west-pocket

# Start one service, or all 6 services + local Caddy gateway (:6660)
make run-userservice
# make run-all
```

Each service loads `.env` by searching upward from its working directory.

### Make Commands

| Command | Description |
|---|---|
| `make run-<service>` | Start a service (`userservice`, `catalogservice`, `paymentservice`, `spotservice`, `errandservice`, `westpocketservice`) |
| `make run-all` | Start local Caddy gateway (`127.0.0.1:6660` via `deploy/Caddyfile.local`) and all 6 services |
| `make build` | Compile all 6 services to `bin/` |
| `make proto` | Regenerate Go code into `gen/` and initialize/update `go.work` (BSR publishing runs in `.github/workflows/buf.yml` on `main`) |
| `make proto-lint` | Lint proto files (`buf lint`) |
| `make proto-format` | Format proto files in place (`buf format --write`) |
| `make migrate` | Apply `migrations/001_init.sql` (fresh DB only; requires `DATABASE_URL`) |
| `make migrate-west-pocket` | Apply additive `migrations/002_west_pocket.sql` with `ON_ERROR_STOP=1` |
| `make lint` | Run `golangci-lint` across all workspace modules |
| `make lint-fix` | Run `golangci-lint` with `--fix` across all workspace modules |
| `make tidy` | Run `go mod tidy` across `internal/pkg` and all 6 services |
| `make setup` | Install pre-commit hooks (run once after cloning) |

### Dev Auth Bypass

In development mode (`APP_ENV=development`), authentication can be bypassed by setting the `X-Dev-User-ID` header to an active user ID. This bypass is strictly disabled when `APP_ENV=production`.

## Keyword Search & Testing

`SpotGoodsService/ListSpotGoods` and `ProductTemplateService/GetProductTemplateList` accept an optional `keyword`:

- Surrounding whitespace is trimmed; up to 200 Unicode characters are accepted.
- Matching is case-insensitive on literal substrings (`%`, `_`, backslashes, and quotes are not wildcards).
- Spot goods match template titles, descriptions, barcodes, or store names; product templates match titles, descriptions, or barcodes.
- Both endpoints accept `store_id=0` (all stores) and `page_size` in `1..100`; filtering runs before pagination and `total_count` reflects matching rows.
- Spot search reads the `catalog` schema directly in PostgreSQL (no cross-service Go import; the spot DB role requires read access to `catalog` tables), while product detail hydration still calls Catalog RPCs.

Run unit and handler tests across the workspace:

```bash
go test -race -count=1 ./internal/pkg/...
go test -race -count=1 ./internal/service/userservice/... ./internal/service/catalogservice/... ./internal/service/paymentservice/... ./internal/service/spotservice/... ./internal/service/errandservice/... ./internal/service/westpocketservice/...
```

Database-backed integration tests create and drop isolated temporary databases when configured (never point them at production):

- `SAST_SHOP_TEST_POSTGRES_URL` — catalog/spot integration tests (applies `migrations/001_init.sql`)
- `WEST_POCKET_TEST_DSN` — West Pocket integration tests (applies `001_init.sql` + `002_west_pocket.sql` using fake cloud/Feishu adapters)

Without these variables, DB integration tests skip cleanly while unit and validation tests still run.

## Deployment & Docs

Production runs on `https://api.sast.fun` via GitHub Actions (`.github/workflows/ci.yml`, `buf.yml`, `deploy.yml`):

- Pushes to `main` detect changed services, push immutable images (`ccr.ccs.tencentyun.com/<TCR_NAMESPACE>/<IMAGE_PREFIX>-<service>:sha-<40-char-sha>`) to Tencent Cloud TCR, and deploy serially (`max-parallel: 1`) in the `production` Environment via `/usr/local/lib/sast-shop/deploy-image` with ED25519 host fingerprint verification (`SERVER_SSH_FINGERPRINT`).
- Auto-deploy currently covers the 5 core services; `westpocketservice` is deployed explicitly via Actions → `Deploy` (`workflow_dispatch`) after completing migration and private provider setup.
- Production requires `APP_ENV=production` and real `DB_*`, `REDIS_*`, `FEISHU_*`, and `COS_*` credentials (missing Feishu or `catalogservice` COS credentials panic on startup in `production`). Keep ports `1323–1328` restricted to loopback behind the public gateway.

For full operational details, see:

- [docs/README.md](./docs/README.md) — Documentation index
- [docs/deployment-ci.md](./docs/deployment-ci.md) — CI workflows, TCR deployment, secrets/variables, and rollout checklist
- [docs/west-pocket.md](./docs/west-pocket.md) — West Pocket setup, privacy/retention policies, durable outbox, and acceptance checks
- [AGENTS.md](./AGENTS.md) — Guidance for AI coding agents

## License

MIT
