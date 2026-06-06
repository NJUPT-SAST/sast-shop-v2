# SAST Shop v2

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

### 安装与运行

```bash
# 克隆仓库
git clone <repo-url> && cd sast-shop-v2

# 复制环境变量模板并填写实际值
cp .env.example .env

# 从 proto 生成 Go 代码
buf generate

# 确保 PostgreSQL 和 Redis 已启动，然后启动任意服务
cd internal/service/userservice && go run ./cmd/app
```

每个服务会从当前目录向上搜索并加载 `.env` 文件，因此可以在各服务的 `cmd/app` 目录下直接运行。

### 开发命令

```bash
# 修改 .proto 文件后重新生成代码
buf generate

# 对所有工作区模块执行 lint
make lint
make lint-fix        # 自动修复

# 从仓库根目录运行单个服务
go -C internal/service/userservice run ./cmd/app
```

### VS Code

项目包含 `.vscode/launch.json`，已配置好 user service 的调试启动项。

### 开发环境绕过认证

在开发模式下（`APP_ENV=development`），可通过设置 `X-Dev-User-ID` Header 为有效用户 ID 来绕过认证。

## 当前状态

项目处于早期开发阶段，目前已实现：

- **User 服务**：`GetUserInfo`、`GetUsers`（内部接口）— 真实数据库查询
- **其他所有 Handler**：返回占位的 "To be implemented" 错误

暂无测试。欢迎贡献。

## 许可证

MIT
