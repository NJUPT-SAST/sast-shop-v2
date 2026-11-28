# sast.fun 后端 CI 与部署

生产 API 使用 `https://api.sast.fun`。仓库采用基于变更检测的 CI、BSR 自动发布，以及不可变镜像 + 服务器 helper 的串行部署契约。

## CI 与 Proto 流水线

- **`.github/workflows/ci.yml`**：在 `main` 的 push / PR 上运行，通过 `detect-changes.yml`（`mode: ci`）识别变更模块。Matrix 固定包含全部 7 个模块（`internal/pkg` 与 6 个服务），未变更模块走 skip step 以保证分支保护所需的 check 名称稳定；改动 `internal/pkg/`、`proto/`、`buf.*`、`go.work`、`.golangci.yml` 或 CI 工作流时全量执行。`westpocketservice` 构建任务附带 `postgres:17` 并注入 `WEST_POCKET_TEST_DSN`。
- **`.github/workflows/buf.yml`**：在 `proto/**`、`buf.yaml`、`buf.lock` 变更时运行。PR 阶段校验 BSR breaking change（提交说明包含 `buf skip breaking` 时跳过）；push 到 `main` 时自动推送至 BSR（`buf.build/sast/sast-shop-v2`），配置 `FEISHU_BSR_WEBHOOK` 后同步推送飞书通知卡片。

## 发布流程

`.github/workflows/deploy.yml` 在 `main` push 时触发，`detect-changes.yml`（`mode: deploy`）按变更选择受影响服务：

- 改动 `internal/pkg/`、`proto/`、`buf.*`、`Dockerfile`、`.dockerignore`、`deploy/` 或部署工作流时，自动构建并部署全部 6 个生产服务（`userservice`、`catalogservice`、`paymentservice`、`spotservice`、`errandservice`、`westpocketservice`；2026-11 起 `westpocketservice` 已纳入自动发布）。
- 手动部署：在 GitHub Actions 运行 `Deploy`（`workflow_dispatch`），`service` 填写单个服务名（新环境首次发布 West Pocket 须先完成前置迁移与配置并手动指定 `westpocketservice`）；留空则沿用自动变更检测。操作前先确认当前提交 CI 通过并完成下方准备。

构建 job 将镜像推送至腾讯云 TCR，打 `:<7位SHA>` 与 `:sha-<完整40位SHA>` 两个标签（`ccr.ccs.tencentyun.com/<TCR_NAMESPACE>/<IMAGE_PREFIX>-<service>`，默认 `IMAGE_PREFIX=sast-shop`）。部署 job 使用 `production` Environment，按 `max-parallel: 1` 串行执行：先核验 ED25519 主机指纹（`SERVER_SSH_FINGERPRINT`），再将 TCR 凭据通过标准输入传给服务器 helper：

```text
sudo -n /usr/local/lib/sast-shop/deploy-image <service> <40位SHA> <image:sha-40位SHA> --registry-stdin
```

由服务器 helper 负责镜像拉取、服务就绪检查及失败回滚。

## GitHub 配置

| 类型 | 名称 | 说明 |
|---|---|---|
| Variable | `TCR_NAMESPACE` | 必填，TCR 命名空间 |
| Variable | `IMAGE_PREFIX` | 可选，TCR 仓库名前缀（默认 `sast-shop`） |
| Secret | `TCR_USERNAME` / `TCR_PASSWORD` | TCR 推拉凭据 |
| Secret | `SERVER_HOST` / `SERVER_USER` / `SSH_PRIVATE_KEY` | 部署服务器 SSH 访问凭据 |
| Secret | `SERVER_SSH_FINGERPRINT` | 服务器 ED25519 主机公钥指纹（`SHA256:...`） |
| Secret | `BUF_TOKEN` / `FEISHU_BSR_WEBHOOK` | `buf.yml` 推送 BSR 与飞书卡片通知凭据（webhook 可选） |

不要将凭据提交入库；`production` Environment 中的同名 Secret 会覆盖仓库级值。生产环境需配置 `APP_ENV=production` 及真实的 `DB_*`、`REDIS_*`、`FEISHU_APP_ID`/`FEISHU_APP_SECRET`、`COS_*`（`production` 下缺失 Feishu 或 `catalogservice` COS 凭据会在启动时 panic），并将 `1323–1328` 端口限制为仅本机回环访问。

## 增量迁移与专项升级

数据库迁移、外部云服务、网关与服务器 helper 首次配置均不由 `deploy.yml` 执行；对存量库执行任何增量迁移（`migrations/002_*.sql`～`010_*.sql`）前必须先备份，且**不能重新运行** `migrations/001_init.sql`。

- **管理员后台升级**：增量迁移、内部调用认证、首个超级管理员初始化、网关白名单与验收流程见 [管理员后台](./admin.md)；须同步发布移动端的交易轮次校验。
- **West Pocket 首次启用**：
  1. 备份数据库并执行 `DATABASE_URL='...' make migrate-west-pocket`（`migrations/002_west_pocket.sql`）。
  2. 按 [West Pocket 接入与验收](./west-pocket.md) 配置 `WEST_POCKET_INTERNAL_TOKEN`、`WEST_POCKET_ENCRYPTION_KEY`、私有 COS、腾讯云 IAI 和飞书消息能力。
  3. 更新服务器 Compose 与 helper 的服务白名单，确认 `westpocketservice` 就绪检查使用 `/health/ready`。
  4. 公网网关仅开放两个 public service 和上传路径，禁止暴露 `*InternalService`，并移除客户端传入的 `X-West-Pocket-Token`。
  5. 首次通过 Actions 手动触发 `Deploy` 并指定 `westpocketservice`，核对 `/health/ready` 与真实手机端流程（完成后后续变更走自动发布）。

合并代码和本地测试不代表生产环境已完成配置或上线。
