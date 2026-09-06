# SAST Shop v2

[English](./README.md)

南京邮电大学（NJUPT）校园电商后端，基于 Go 微服务架构构建。

## 技术栈

| 技术 | 用途 |
|---|---|
| Go 1.26 | 开发语言 |
| Connect-RPC | API 框架（HTTP/gRPC） |
| Echo v5 | HTTP 路由 |
| PostgreSQL + bun | 主数据库 |
| Redis + go-redis | 会话与缓存 |
| Buf + protovalidate | Proto 管理与请求校验 |
| zerolog | 结构化日志 |
| 飞书（Lark） | SSO 单点登录 |

## 架构

```
客户端 → Echo v5 → protovalidate 拦截器 → auth 拦截器 → Handler
                                                          ↓
                                                     Service 层
                                                          ↓
                                                   Repository（bun ORM）
                                                          ↓
                                                     PostgreSQL
```

工作区包含一个共享库和五个可独立部署的微服务，各自运行在独立端口上。服务之间通过 Connect-RPC 通信，不存在直接的 Go import 依赖。

### 服务一览

| 服务 | 目录 | 端口 | 说明 |
|---|---|---|---|
| `internal/pkg` | `internal/pkg/` | — | 共享库（配置、日志、数据库、Redis、拦截器） |
| User | `internal/service/userservice/` | 1323 | 飞书 SSO 登录、用户信息、收货地址 |
| Catalog | `internal/service/catalogservice/` | 1324 | 店铺、商品模板、条形码 |
| Payment | `internal/service/paymentservice/` | 1325 | 账单、收款码、支付确认 |
| Spot | `internal/service/spotservice/` | 1326 | 现货商品、库存、订单 |
| Errand | `internal/service/errandservice/` | 1327 | 拼团跑腿：需求 → 任务 → 分发 → 收款 |

### 项目结构

```
sast-shop-v2/
├── proto/sast/sastshopv2/    # Protobuf API 定义（5 个领域）
│   ├── common/v1/            # 共享错误类型（BusinessError oneof）
│   ├── user/v1/              # Auth、User、Address 服务
│   ├── catalog/v1/           # Store、ProductTemplate 服务
│   ├── payment/v1/           # Bill、QrCode 服务
│   ├── spot/v1/              # SpotGoods、SpotOrder 服务
│   └── errand/v1/            # 跑腿需求/任务/分发/订单服务
├── gen/                      # 生成的 .pb.go + .connect.go（gitignore）
├── buf.yaml                  # Buf 模块配置
├── buf.gen.yaml              # 代码生成插件
├── go.work                   # Go 工作区（6 个模块）
├── .env.example              # 环境变量模板
├── .golangci.yml             # Linter 配置
└── internal/
    ├── pkg/                  # 共享库
    │   ├── config/           # 环境变量配置（caarlos0/env + godotenv）
    │   ├── logger/           # zerolog 配置（开发环境 console，生产环境 JSON）
    │   ├── bun/postgres/     # PostgreSQL 连接（bun + pgdriver）
    │   ├── redis/            # Redis 客户端 + 会话存储
    │   ├── connect/interceptor/  # Auth + 校验拦截器
    │   ├── constant/         # 服务名称、会话 TTL、Header 名称
    │   └── rpcerror/         # 业务错误辅助函数
    └── service/
        ├── userservice/      # cmd/app/main.go + internal/{handler,model,repository,service}
        ├── catalogservice/
        ├── paymentservice/
        ├── spotservice/
        └── errandservice/
```

## API 概览

API 共包含 **46 个 RPC 方法**，分布在 14 个服务定义中。核心业务流程：

- **认证**：飞书授权码登录 → 会话令牌 → 存入 Redis（30 分钟有效期）
- **现货交易**：卖家上架商品 → 买家下单 → 生成支付账单 → 付款 → 确认 → 完成
- **跑腿拼团**：买家发布需求 → 团长接单创建任务 → 采购 → 分发 → 收款 → 完成
- **跨服务通信**：内部端点（`*Internal` 后缀）处理服务间调用（如 Payment 在账单清账后回调 Errand 的 `OnPaymentConfirmed`）

## 数据库

19 张表，分布在 5 个 PostgreSQL schema 中：

| Schema | 表 |
|---|---|
| `user` | `user_account`、`auth_session`、`member_address` |
| `catalog` | `catalog_store`、`catalog_product_template`、`catalog_product_barcode`、`catalog_product_image` |
| `payment` | `payment_qr_code`、`payment_bill`、`payment_confirmation_log` |
| `spot` | `spot_goods`、`spot_stock_ledger`、`spot_order` |
| `errand` | `errand_demand`、`errand_demand_item`、`errand_task`、`errand_task_item`、`errand_task_assignment`、`errand_price_change_log`、`errand_action_log` |

所有模型使用 `uptrace/bun` ORM，表名采用 schema 前缀。

## 快速开始

### 环境要求

- Go 1.26+
- PostgreSQL
- Redis
- [Buf CLI](https://buf.build/docs/cli/)
- [golangci-lint](https://golangci-lint.run/welcome/install/)
- [pre-commit](https://pre-commit.com/#install)

### 安装与运行

```bash
# 克隆仓库
git clone git@github.com:NJUPT-SAST/sast-shop-v2.git && cd sast-shop-v2

# 复制环境变量模板并填写实际值
cp .env.example .env

# 安装 Git Hooks
make setup

# 执行数据库迁移
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/sast_shop_v2?sslmode=disable
make migrate

# 启动服务
make run-userservice
```

每个服务会从当前目录向上搜索并加载 `.env` 文件。

### Make 命令

| 命令 | 说明 |
|---|---|
| `make run-<service>` | 启动指定服务（`userservice`、`catalogservice`、`paymentservice`、`spotservice`、`errandservice`） |
| `make build` | 编译全部服务，产物输出到 `bin/` |
| `make proto` | 从 proto 重新生成 Go 代码并推送到 BSR |
| `make proto-lint` | 检查 proto 文件 |
| `make proto-format` | 格式化 proto 文件 |
| `make migrate` | 执行 `migrations/001_init.sql`（需设置 `DATABASE_URL`） |
| `make lint` | 对所有模块执行 golangci-lint |
| `make lint-fix` | 执行 golangci-lint 并自动修复 |
| `make tidy` | 对所有模块执行 `go mod tidy` |
| `make setup` | 安装 pre-commit hooks（克隆后执行一次） |

### 开发环境绕过认证

在开发模式下（`APP_ENV=development`），可通过设置 `X-Dev-User-ID` Header 为有效用户 ID 来绕过认证。

## 当前状态

项目处于早期开发阶段，目前已实现：

- **User 服务**：`GetUserInfo`、`GetUsers`（内部接口）— 真实数据库查询
- **其他所有 Handler**：返回占位的 "To be implemented" 错误

暂无测试。欢迎贡献。

## 部署

生产部署采用容器化方案：每次 push 到 `main` 会构建 Docker 镜像并推送到腾讯云容器镜像服务（TCR），随后通过 SSH 在服务器上升级有变更的服务。

### 流程

```
push 到 main
  → 检测变更的服务（internal/pkg、proto/、Dockerfile 变更 → 全部服务）
  → 构建并推送 ccr.ccs.tencentyun.com/<命名空间>/sast-shop-<服务>:<短sha> 到 TCR
  → SSH：拉取镜像，将 sast/<服务>:current 旋转为 backup，新镜像打上 current 标签
  → docker compose up -d --no-deps <服务>
```

手动部署：Actions → Deploy → Run workflow（可选单个服务）。

### GitHub 配置

| 类型 | 名称 | 说明 |
|---|---|---|
| Secret | `TCR_USERNAME` / `TCR_PASSWORD` | 推送镜像的 TCR 凭据（建议使用长期访问令牌） |
| Secret | `SERVER_HOST` / `SERVER_USER` / `SSH_PRIVATE_KEY` | 部署服务器 SSH 访问 |
| Variable | `TCR_NAMESPACE` | TCR 命名空间，如 `sast` |
| Variable | `IMAGE_PREFIX` | TCR 仓库名前缀（默认 `sast-shop`） |
| Variable | `COMPOSE_DIR` | 服务器上存放 `docker-compose.yml` 的目录（默认 `/data/sast-shop-v2`） |

### 服务器一次性初始化

1. 复制 compose 模板：`mkdir -p /data/sast-shop-v2 && cp deploy/docker-compose.yml /data/sast-shop-v2/`
2. 参照 `.env.example` 创建 `/data/sast-shop-v2/.env`，填入生产配置。必须设置：
   - 真实的 `REDIS_HOST`/`REDIS_PORT` —— 服务启动时会 PING Redis，失败直接 panic
   - `DB_*` 数据库凭据
   - `APP_ENV=production` 并配真实飞书凭据（`FEISHU_APP_ID`、`FEISHU_APP_SECRET`）——`APP_ENV=development` 会启用 `X-Dev-User-ID` 认证旁路，生产环境不允许
   - COS 变量可选 —— catalogservice 缺少时会优雅降级
3. 对生产数据库执行一次迁移：
   `psql "$DATABASE_URL" -f migrations/001_init.sql`
4. 防火墙将 1323–1327 端口限制为仅 loopback —— 服务绑定 `0.0.0.0`；宿主机 Caddy 通过 `127.0.0.1` 访问它们。
5. 确保服务器装有带 Compose v2 插件的 Docker。

### 手动回滚

每次部署都会把上一个镜像旋转为 `sast/<服务>:backup`。回滚命令：

```bash
cd /data/sast-shop-v2
docker tag sast/userservice:backup sast/userservice:current
docker compose up -d --no-deps userservice
```

> 若 TCR 仓库为私有，需在服务器上执行一次 `docker login ccr.ccs.tencentyun.com`。

## 许可证

MIT
