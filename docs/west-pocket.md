# West Pocket 接入与验收

West Pocket 是独立的移动端 AA 微服务，监听 `1328`。用户信息由 `userservice` 管理，付款状态由 `paymentservice` 管理。个人收款码模式下的流程为：飞书个人消息 → 移动网页账单 → 查看/保存收款码 → 用户标记已付款 → 发起人核实到账（无商户支付、小程序支付或自动到账回调）。

## 接口与数据

- **对外 RPC**：`proto/sast/sastshopv2/westpocket/v1/west_pocket.proto` 中的 `FaceProfileService`、`WestPocketService`，沿用现有 Connect 会话鉴权。
- **内部 RPC**：`WestPocketInternalService` 以及 `userservice`/`paymentservice` 新增的内部查询、收款码与取消接口，均要求携带 `X-West-Pocket-Token`（至少 32 字符）。公网网关与前端代理必须移除该 header，服务间通过私网/本机直连。
- **上传接口**：`POST /api/v1/west-pocket/uploads`，multipart 字段为 `file`、`purpose`（`face_sample` / `group_photo`）、`pocket_id`（合照必填）、`consent_version`、`request_id`（UUID）。移动端经同源 `/api/uploads/west-pocket` 转发可信会话。支持 JPEG/PNG/WebP，单文件最大 10 MB，服务端重编码移除 EXIF 并限制并发上传数。
- **数据库**：`migrations/002_west_pocket.sql` 新建 `westpocket` schema（11 张表：活动、成员、上传、照片、人脸档案/样本、异步任务、识别结果、通知 outbox、授权记录与请求幂等记录）。
- **分摊与状态**：金额为整数分（`int32`），余数按用户 ID 升序分配；发起人默认参与且不为自己建账。发布后金额及成员冻结，付款与取消通过 `paymentservice` 的同源事务锁协调。
- **人脸兜底**：人脸识别只输出候选人，最终名单由发起人确认；未开通人脸服务时可直接搜索姓名。

## 配置与部署

1. 备份现有数据库，在已有 `001_init.sql` 基础上执行一次增量迁移（带事务且遇错即停，**禁止重跑初始化迁移**）：

   ```sh
   DATABASE_URL='postgresql://…' make migrate-west-pocket
   ```

2. 按 `.env.example` 为 `userservice`、`paymentservice`、`westpocketservice` 配置相同的随机 `WEST_POCKET_INTERNAL_TOKEN`（至少 32 字符），并同步部署相关内部接口变更。
3. 配置随机 32 字节 base64 编码的 `WEST_POCKET_ENCRYPTION_KEY`，用于静态加密发布时冻结的个人收款码。务必持久保管，随意轮换会导致既有账单无法解密。
4. 配置**私有** COS 桶及 `WEST_POCKET_COS_*`（可复用已有私有桶并使用独立 `west-pocket/` 前缀；商城图片代理仅开放 `sast-shop/`，禁止将 `west-pocket/` 暴露到公开 CDN）。对象保持私有 ACL，鉴权通过后才签发 5 分钟有效的预签名 GET URL。
5. 开通腾讯云 IAI（`2018-03-01` API）并预建模型 3.0 人员库，配置 `WEST_POCKET_IAI_*`。识别采用先整图检测、再逐脸裁剪搜索（支持超过 10 人的合照）；默认阈值 `85`、候选分差 `5`，上线前应使用已授权样本校准。
6. 配置 `FEISHU_APP_ID` / `FEISHU_APP_SECRET`（开启机器人、消息发送与图片上传权限，确保应用对参与用户可见）及用户可访问的 `WEST_POCKET_MOBILE_URL`。分摊成员收到个人账单与收款码，发起人收到汇总。
7. 本地联调执行 `make proto && make run-all`（通过 `deploy/Caddyfile.local` 路由至 `127.0.0.1:1328` 并剥离 `X-West-Pocket-Token`）。新环境首次生产启用需更新服务器 Compose 与 helper 服务白名单并核验 `/health/ready`，再于 GitHub Actions `Deploy` 显式指定 `westpocketservice`；自 2026-11 起 `westpocketservice` 已纳入自动发布，后续代码变更随 CI/Deploy 自动上线（详见 [CI 与部署](./deployment-ci.md)）。公网网关仅开放两个 public service 和上传路径，禁止暴露 `*InternalService`。
8. 移动端使用真实后端数据源 `local` 及对应 Connect 网关：底部加号 → West Pocket；个人中心「地址簿」下方可录入或撤回本人脸档案。

未配置第三方能力时，接口通过 `GetCapabilities` 返回明确状态，前端降级为姓名搜索，不会伪造识别或发送成功。`/health/live` 检查进程存活，`/health/ready` 检查 `westpocket.pocket` 迁移表是否存在。

## 隐私与持久任务

- **授权与留存**：人脸录入需本人单独授权（有效期 365 天）；合照上传需确认在场人员知情。临时合照保留 24 小时，纪念合照保留 30 天。撤回人脸授权后立即停止本地匹配，云端删除由持久任务异步完成；共享纪念相册需所有分摊成员独立同意且可撤回。
- **持久任务与幂等**：异步任务（`async_job`）和通知（`notification_outbox`）持久化在 PostgreSQL，通过租约、幂等键和重试应对进程中断。云端 API 与飞书消息属于外部副作用，无法由本地事务保证 exactly-once，通过稳定人员 ID、账单来源键与飞书消息 UUID 降低重复风险。
- **日志红线**：严禁将私有图片预签名 URL、收款码明文、`WEST_POCKET_ENCRYPTION_KEY` 及云端/飞书凭据写入日志。

## 验证

```sh
buf lint
go test -race -count=1 ./internal/pkg/... ./internal/service/userservice/... ./internal/service/paymentservice/... ./internal/service/westpocketservice/...
```

数据库集成测试通过 `WEST_POCKET_TEST_DSN` 指定具备 `CREATE DATABASE` 权限的可丢弃 PostgreSQL 集群（禁止指向生产库），自动创建并清理隔离 UTF8 库；测试使用本地替身适配云存储/识别/飞书，不会产生真实外发。上线前仍须在真实腾讯云、飞书应用和真机上验收：相机权限、多人合照、姓名兜底、分摊金额、二维码保存、飞书个人消息、取消与人脸撤回。
