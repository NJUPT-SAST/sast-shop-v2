# West Pocket 接入与验收

West Pocket 是独立的移动端 AA 微服务，监听 `1328`。用户信息仍由 userservice 管理，付款状态仍由 paymentservice 管理。只有个人收款码时，付款流程为：飞书个人消息 → 移动网页账单 → 查看/保存微信收款码 → 用户标记已付款 → 发起人核实到账。没有商户支付、小程序支付或自动到账回调。

## 接口与数据

- 对外接口：`proto/sast/sastshopv2/westpocket/v1/west_pocket.proto` 中的 `FaceProfileService`、`WestPocketService`，使用现有 Connect 会话鉴权。
- 内部接口：`WestPocketInternalService`、新增用户搜索及收款码/取消接口，必须携带 `X-West-Pocket-Token`。该 header 在公网网关及前端代理被移除；内部服务应通过私网/本机直连。
- 上传：`POST /api/v1/west-pocket/uploads`，multipart 字段 `file`、`purpose`（`face_sample` / `group_photo`）、`pocket_id`（合照必填）、`consent_version`、`request_id`（UUID）。移动端通过同源 `/api/uploads/west-pocket` 转发可信会话。支持 JPEG/PNG/WebP，单文件最多 10 MB；服务端重编码移除 EXIF。
- 数据库：`migrations/002_west_pocket.sql` 新建 `westpocket` schema，包含活动、成员、照片、上传、人脸档案/样本、识别结果、持久任务、通知 outbox、授权记录和请求幂等记录。
- 金额为整数分，余数按用户 ID 升序分配；发起人默认参与且不为自己建账。发布后金额及成员冻结，付款和取消通过 paymentservice 的同源事务锁协调。
- 人脸识别只给出候选人，最终名单由发起人确认。未开通人脸服务时可以直接搜索姓名。

## 部署

1. 备份现有数据库，在已有 `001_init.sql` 基础上执行一次增量迁移：

   ```sh
   DATABASE_URL='postgresql://…' make migrate-west-pocket
   ```

   不要重新执行初始化迁移。增量迁移包含事务，遇到错误会停止。

2. 按 `.env.example` 配置所有服务共用的随机 `WEST_POCKET_INTERNAL_TOKEN`（至少 32 字符）。新增 userservice/paymentservice 接口与新服务一起部署。
3. 配置 `WEST_POCKET_ENCRYPTION_KEY`（随机 32 字节的 base64），用于加密活动发布时冻结的个人收款码。请持久保管密钥，已有账单不能在随意换钥后解密。
4. 配置**私有** COS 桶和 `WEST_POCKET_COS_*`。本次经用户确认复用已有私有桶，使用独立 `west-pocket/` 前缀，商城图片代理仅开放 `sast-shop/`；不得把新目录加入公开 CDN。对象始终私有，经过权限检查才签发 5 分钟访问地址。
5. 开通腾讯云 IAI 人脸识别并预建模型 3.0 人员库，填写 `WEST_POCKET_IAI_*`。SDK 使用 `2018-03-01` API。识别先检测合照，再逐脸裁剪搜索，支持超过十人的合照；默认相似度阈值 85、候选分差 5，发布前应使用已授权样本校准。
6. 配置已有 `FEISHU_APP_ID` / `FEISHU_APP_SECRET`，启用机器人及消息发送、图片上传权限，确保应用对参与用户可见。填写可被用户访问的 `WEST_POCKET_MOBILE_URL`。每位分摊人收到自己的账单及收款码，发起人收到汇总。
7. `make proto && make build`；Compose 已包含 `westpocketservice`，本地 `make run-all` 使用 `deploy/Caddyfile.local`。首次生产启用需配置服务器 helper 的服务白名单及 `/health/ready` 核验，之后在 `Deploy` 中显式指定 `westpocketservice`；当前自动发布继续只覆盖既有五个服务。公网网关只开放两个 public service 和上传路径，禁止把 `*InternalService` 整体暴露。
8. 移动端使用真实后端数据源 `local` 和对应 Connect 网关地址。进入底部加号 → West Pocket；个人中心「地址簿」下面可录入或撤回本人脸档案。

未配置第三方能力时，接口返回明确能力状态，页面提供姓名搜索路径；不会伪造识别或消息发送成功。`/health/live` 表示进程存活，`/health/ready` 检查数据库迁移，第三方能力通过 `GetCapabilities` 单独报告。

## 隐私与持久任务

人脸录入要求本人单独授权；合照上传要求上传者确认在场人员已知情。临时合照保留 24 小时，纪念合照保留 30 天，人脸授权有效期 365 天。撤回后立即停止本地身份匹配，云端删除由持久任务执行。成员共享纪念相册需要所有分摊成员独立同意，且可撤回。

任务和通知在 PostgreSQL 中持久化，使用租约、幂等键及重试处理进程中断。云端 API、飞书消息属于外部副作用，不能由本地事务保证 exactly-once；使用稳定人员 ID、账单来源键、飞书消息 UUID 降低重复风险。不要将私有图片签名 URL、收款码明文和云端密钥写入日志。

## 验证

```sh
buf lint
go test -race ./internal/pkg/... ./internal/service/userservice/... ./internal/service/paymentservice/... ./internal/service/westpocketservice/...
```

真实数据库集成测试通过 `WEST_POCKET_TEST_DSN` 指定可丢弃的 PostgreSQL 集群管理库（需 CREATE DATABASE 权限），自动创建/清理隔离 UTF8 数据库，不能配置生产库；测试使用替身云存储/识别/飞书适配器，不会发送真实消息。上线前还需使用实际腾讯云、飞书应用和手机验收：相机权限、多人合照、姓名兜底、付款金额、二维码保存、飞书个人消息、取消与人脸撤回。
