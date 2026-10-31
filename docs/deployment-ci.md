# sast.fun 后端部署

API 使用 `https://api.sast.fun`。部署沿用主干现有的按变更检测、不可变镜像和服务器 helper 契约。

## 发布流程

`.github/workflows/deploy.yml` 在 `main` push 时触发，`detect-changes.yml` 根据文件变化选择受影响服务。共享库、proto、容器或部署配置变化会构建现有五个生产服务。新 `westpocketservice` 暂不加入自动发布，避免首次迁移或私有能力配置尚未完成时阻断既有服务。

Actions 页面可手动运行 `Deploy`，`service` 填指定服务名；West Pocket 首次发布需显式填写 `westpocketservice`。先确认当前提交 CI 成功并完成下方准备。留空继续使用现有自动变更检测。

镜像使用 `ccr.ccs.tencentyun.com/<TCR_NAMESPACE>/<IMAGE_PREFIX>-<service>:sha-<完整40位SHA>`，默认 `IMAGE_PREFIX` 为 `sast-shop`。部署 job 使用 `production` Environment，串行调用服务器已有 `/usr/local/lib/sast-shop/deploy-image`，保留主机指纹核验、凭据标准输入传递、服务就绪检查及失败回滚。

## 配置和首次启用

沿用现有 Actions Variables `TCR_NAMESPACE`、`IMAGE_PREFIX`，以及 Secrets `TCR_USERNAME`、`TCR_PASSWORD`、`SERVER_HOST`、`SERVER_USER`、`SSH_PRIVATE_KEY`、`SERVER_SSH_FINGERPRINT`。不要把凭据提交入库；Environment 的同名 Secret 会覆盖仓库级值。

West Pocket 首次部署前须：

1. 备份数据库并执行 `migrations/002_west_pocket.sql`，不能重新运行初始化迁移。
2. 依照 [West Pocket 接入与验收](west-pocket.md) 配置内部 token、加密密钥、私有 COS、腾讯云 IAI 和飞书消息能力。
3. 更新服务器 Compose 和 helper 的服务白名单，确认 `westpocketservice` 的就绪检查使用 `/health/ready`。
4. 公网网关只开放 public RPC 和上传路径，不能开放 InternalService，并移除客户端提供的 `X-West-Pocket-Token`。
5. 显式部署新服务，核对真实健康端点及手机流程。

数据库迁移、外部云服务、网关和服务器 helper 的首次配置不由该工作流执行。合并代码和本地测试不代表生产服务已配置或上线。
