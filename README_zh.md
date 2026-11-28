# SAST Shop v2

[English](./README.md) · [文档索引](./docs/README.md)

南京邮电大学（NJUPT）校园电商后端，基于 Go 微服务架构构建。

## 技术栈

| 技术 | 用途 |
|---|---|
| Go 1.26 | 开发语言 |
| Connect-RPC | API 框架（HTTP/gRPC） |
| Echo v5 | HTTP 路由 |
| PostgreSQL + bun | 主数据库 |
| Redis + go-redis | 会话、缓存与上传配额 |
| Buf + protovalidate | Proto 管理与请求校验 |
| zerolog | 结构化日志 |
| 飞书（Lark） | SSO 单点登录与 IM 消息通知 |
| 腾讯云 COS / IAI | 商品图片存储、West Pocket 私有存储与人脸识别 |

## 架构

```
客户端 → Echo v5 → protovalidate + 日志拦截器 → 认证 / 内部令牌拦截器 → Handler
                                                                          ↓
                                                                     Service 层
                                                                          ↓
                                                                   Repository（bun ORM）
                                                                          ↓
                                                                     PostgreSQL
```

工作区包含一个共享库（`internal/pkg`）和 **6 个**可独立运行的微服务，各自监听独立端口。服务之间统一通过 Connect-RPC（`*InternalService`）通信，不存在直接的 Go import 依赖。

### 服务一览

| 服务 | 目录 | 端口 | 说明 |
|---|---|---|---|
| `internal/pkg` | `internal/pkg/` | — | 共享库（配置、日志、数据库、Redis、拦截器、飞书、COS、搜索、测试工具） |
| User | `internal/service/userservice/` | 1323 | 飞书 SSO 登录、用户信息、收货地址 |
| Catalog | `internal/service/catalogservice/` | 1324 | 店铺、商品模板、条形码、商品图片上传 |
| Payment | `internal/service/paymentservice/` | 1325 | 账单、收款码、支付确认、West Pocket 账单协调 |
| Spot | `internal/service/spotservice/` | 1326 | 现货商品、库存流水、订单 |
| Errand | `internal/service/errandservice/` | 1327 | 拼团跑腿：需求 → 任务 → 分发 → 收款 |
| West Pocket | `internal/service/westpocketservice/` | 1328 | 移动端授权 AA 收款、人脸匹配、持久飞书通知 outbox |

### 项目结构

```
sast-shop-v2/
├── proto/sast/sastshopv2/    # Protobuf API 定义（7 个业务域 + common）
│   ├── common/v1/            # 共享错误类型（BusinessError oneof）
│   ├── admin/v1/             # 管理员治理、审计与支付纠错内部 RPC
│   ├── user/v1/              # Auth、User、Address、UserInternal 服务
│   ├── catalog/v1/           # Catalog（店铺）、ProductCategory、ProductTemplate、CatalogInternal 服务
│   ├── payment/v1/           # Bill、QrCode、PaymentInternal 服务
│   ├── spot/v1/              # SpotGoods、SpotOrder 服务
│   ├── errand/v1/            # ErrandDemand、ErrandTask、BuyerErrandOrder、GroupTradeInternal 服务
│   └── westpocket/v1/        # FaceProfile、WestPocket、WestPocketInternal 服务
├── gen/                      # 生成的 .pb.go + .connect.go（gitignore）
├── migrations/               # SQL 初始化与增量迁移（001–010）
├── deploy/                   # 本地网关配置（Caddyfile.local）与 PostgreSQL zhparser Dockerfile
├── docs/                     # 运维与功能文档（见 docs/README.md）
├── buf.yaml / buf.gen.yaml   # Buf 模块与代码生成配置
├── go.work                   # Go 工作区（gitignore，由 make proto 生成，含 9 个模块）
├── .env.example              # 环境变量模板
├── .golangci.yml             # Linter 配置
└── internal/
    ├── pkg/                  # 共享库
    │   ├── admin/            # 管理员 RBAC 鉴权、查询、变更与资金辅助
    │   ├── adminflow/        # 跨服务管理员整单纠错流程
    │   ├── bun/postgres/     # PostgreSQL 连接（bun + pgdriver）
    │   ├── config/           # 环境变量配置（caarlos0/env/v11 + godotenv）
    │   ├── connect/interceptor/ # 校验、日志、认证与 West Pocket 内部令牌拦截器
    │   ├── constant/         # 服务名称、会话 TTL、Header 名称
    │   ├── cos/              # 腾讯云 COS 商品图片客户端
    │   ├── errmsg/           # 各域错误信息辅助函数
    │   ├── feishu/           # 飞书 OAuth、JSAPI 票据/签名、IM 通知
    │   ├── idgen/            # 雪花 ID 生成器
    │   ├── logger/           # zerolog 配置（开发环境 console，生产环境 JSON）
    │   ├── redis/            # Redis 客户端、会话存储、上传限流与配额
    │   ├── rpcerror/         # BusinessError 辅助函数
    │   ├── search/           # 搜索关键词规范化（NormalizeKeyword）
    │   ├── testutil/         # 隔离 PostgreSQL 集成测试工具
    │   └── timeutil/         # 乐观锁（updated_at）辅助函数
    └── service/
        ├── userservice/      # cmd/app/main.go + internal/{handler,model,repository,service}
        ├── catalogservice/
        ├── paymentservice/
        ├── spotservice/
        ├── errandservice/
        └── westpocketservice/
```

## API 概览

API 共包含 **98 个 RPC 方法**，分布在 **19 个服务定义**中（含内部跨服务契约）：

- **认证**：飞书授权码登录 → Bearer 会话令牌 → 存入 Redis（30 分钟有效期）
- **现货交易**：卖家上架商品 → 买家下单 → 生成支付账单 → 付款 → 确认 → 完成
- **跑腿拼团**：买家发布需求 → 团长创建任务 → 采购 → 分发 → 收款 → 完成
- **West Pocket**：移动端 AA 分摊 → 可选授权人脸识别或姓名搜索 → 冻结收款码与账单 → 飞书个人通知与人工核账（详见 [docs/west-pocket.md](./docs/west-pocket.md)）
- **跨服务通信**：`*InternalService` 端点专供内部服务调用，禁止暴露在公网网关上

## 数据库

**31 张表，分布在 6 个 PostgreSQL schema 中**（所有模型使用 `uptrace/bun` ORM 及带 schema 前缀的表名）：

| Schema | 表 |
|---|---|
| `user`（3） | `user_account`、`auth_session`、`member_address` |
| `catalog`（4） | `catalog_store`、`catalog_product_template`、`catalog_product_barcode`、`catalog_product_image` |
| `payment`（3） | `payment_qr_code`、`payment_bill`、`payment_confirmation_log` |
| `spot`（3） | `spot_goods`、`spot_stock_ledger`、`spot_order` |
| `errand`（7） | `errand_demand`、`errand_demand_item`、`errand_task`、`errand_task_item`、`errand_task_assignment`、`errand_price_change_log`、`errand_action_log` |
| `westpocket`（11） | `pocket`、`pocket_member`、`upload`、`pocket_photo`、`face_profile`、`face_sample`、`async_job`、`face_match`、`notification_outbox`、`consent_event`、`request_dedup` |

迁移脚本位于 `migrations/`：

- **全新数据库**：执行 `make migrate`（`001_init.sql`）后依次应用增量迁移（`002_west_pocket.sql`～`010_store_product_categories.sql`）。
- **存量数据库**：备份后通过 `psql` 执行未应用的增量迁移（`002`～`010`），切勿重跑 `001_init.sql`。详见 [docs/admin.md](./docs/admin.md) 与 [docs/west-pocket.md](./docs/west-pocket.md)。

## 快速开始

### 环境要求

- Go 1.26+
- PostgreSQL 与 Redis
- [Buf CLI](https://buf.build/docs/cli/)
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- [pre-commit](https://pre-commit.com/#install)
- [Caddy](https://caddyserver.com/)（可选，使用 `make run-all` 时需要）

### 安装与运行

```bash
# 克隆仓库
git clone git@github.com:NJUPT-SAST/sast-shop-v2.git && cd sast-shop-v2

# 复制环境变量模板并填写本地凭据
cp .env.example .env

# 生成 protobuf / Connect 代码并初始化 go.work
make proto

# 安装 Git Hooks
make setup

# 执行数据库迁移（全新库）
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/sast_shop_v2?sslmode=disable
make migrate
make migrate-west-pocket

# 启动单个服务，或一键启动全部 6 个服务 + 本地 Caddy 网关（:6660）
make run-userservice
# make run-all
```

每个服务会从当前工作目录向上搜索并加载仓库根目录 `.env`。

### Make 命令

| 命令 | 说明 |
|---|---|
| `make run-<service>` | 启动指定服务（`userservice`、`catalogservice`、`paymentservice`、`spotservice`、`errandservice`、`westpocketservice`） |
| `make run-all` | 启动本地 Caddy 网关（`127.0.0.1:6660`，使用 `deploy/Caddyfile.local`）及全部 6 个服务 |
| `make build` | 编译全部 6 个服务，产物输出到 `bin/` |
| `make proto` | 从 proto 生成 Go 代码到 `gen/` 并初始化/更新 `go.work`（BSR 发布由 `.github/workflows/buf.yml` 在 `main` 触发） |
| `make proto-lint` | 检查 proto 文件（`buf lint`） |
| `make proto-format` | 原地格式化 proto 文件（`buf format --write`） |
| `make migrate` | 执行 `migrations/001_init.sql`（仅全新库；需设置 `DATABASE_URL`） |
| `make migrate-west-pocket` | 以 `ON_ERROR_STOP=1` 执行增量迁移 `migrations/002_west_pocket.sql` |
| `make lint` | 对工作区所有模块执行 `golangci-lint` |
| `make lint-fix` | 对工作区所有模块执行 `golangci-lint --fix` |
| `make tidy` | 对 `internal/pkg` 及全部 6 个服务执行 `go mod tidy` |
| `make setup` | 安装 pre-commit hooks（克隆后执行一次） |

### 开发环境绕过认证

在开发模式（`APP_ENV=development`）下，可通过设置请求头 `X-Dev-User-ID` 为有效活跃用户 ID 来绕过认证；在 `APP_ENV=production` 下严格禁用。

## 关键词搜索与测试

`SpotGoodsService/ListSpotGoods` 与 `ProductTemplateService/GetProductTemplateList` 支持可选参数 `keyword`：

- 自动去除首尾空白，最多接受 200 个 Unicode 字符。
- 采用大小写无关的字面子串匹配，`%`、`_`、反斜杠与引号不会作为通配符或操作符。
- 现货商品匹配商品模板标题、描述、条码或店铺名；商品模板匹配标题、描述或条码。
- 两个接口均支持 `store_id=0`（查询全部店铺）且 `page_size` 范围为 `1..100`；过滤在分页之前执行，`total_count` 为匹配总数。
- 现货搜索在 PostgreSQL 中跨 schema 读取 `catalog` 表（不引入跨服务 Go import，现货数据库账号需具备 `catalog` 只读权限），商品详情仍经 Catalog RPC 补全。

运行全仓单元与 Handler 测试：

```bash
go test -race -count=1 ./internal/pkg/...
go test -race -count=1 ./internal/service/userservice/... ./internal/service/catalogservice/... ./internal/service/paymentservice/... ./internal/service/spotservice/... ./internal/service/errandservice/... ./internal/service/westpocketservice/...
```

数据库集成测试在配置环境变量后会自动创建并销毁隔离临时库（禁止指向生产库）：

- `SAST_SHOP_TEST_POSTGRES_URL`：catalog / spot 集成测试（加载 `migrations/001_init.sql`）
- `WEST_POCKET_TEST_DSN`：West Pocket 集成测试（加载 `001_init.sql` + `002_west_pocket.sql`，使用本地替身适配云存储/识别/飞书）

未配置时数据库集成测试会自动跳过，单元测试与校验测试仍正常运行。

## 部署与文档

生产环境运行在 `https://api.sast.fun`，通过 GitHub Actions（`.github/workflows/ci.yml`、`buf.yml`、`deploy.yml`）完成校验与发布：

- 推送到 `main` 后自动检测变更服务，向腾讯云 TCR 推送不可变镜像（`ccr.ccs.tencentyun.com/<TCR_NAMESPACE>/<IMAGE_PREFIX>-<service>:sha-<40位SHA>`），并在 `production` Environment 中以 `max-parallel: 1` 串行调用服务器 `/usr/local/lib/sast-shop/deploy-image`（含 `SERVER_SSH_FINGERPRINT` 主机指纹核验与失败回滚）。
- 自动发布目前覆盖既有 5 个核心服务；`westpocketservice` 在完成迁移与私有能力配置后通过 Actions → `Deploy`（`workflow_dispatch`）显式发布。
- 生产环境必须配置 `APP_ENV=production` 及真实的 `DB_*`、`REDIS_*`、`FEISHU_*`、`COS_*` 凭据（`production` 下缺失飞书或 `catalogservice` COS 凭据会在启动时 panic），并将 `1323–1328` 端口限制为仅本机回环访问。

更多运维与接入说明见：

- [docs/README.md](./docs/README.md) — 后端文档索引
- [docs/deployment-ci.md](./docs/deployment-ci.md) — CI 流水线、TCR 发布、密钥配置与首次启用清单
- [docs/west-pocket.md](./docs/west-pocket.md) — West Pocket 配置、隐私留存、持久 outbox 与验收清单
- [AGENTS.md](./AGENTS.md) — AI 编程代理开发指南

## 许可证

MIT
