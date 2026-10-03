# sast.fun 后端自动部署

API 使用 `https://api.sast.fun`。此环境独立于原商城，使用独立数据库和数据卷，不迁移原用户、订单或 West Pocket 数据。

## 发布流程

`.github/workflows/deploy.yml` 监听本仓库 `main` 上由 push 触发的 `CI` 成功完成事件。工作流再次查询 `ci.yml`，验证目标完整 SHA 的最新 push CI 已成功，只检出该 SHA。自动发布每次构建全部六个服务，所有镜像推送成功后，才开始按顺序部署。

镜像沿用现有腾讯云 TCR 仓库结构：

```text
ccr.ccs.tencentyun.com/<TCR_NAMESPACE>/<IMAGE_PREFIX>-<service>:sha-<完整40位SHA>
```

构建参数 `SERVICE` 选择服务，`BUILD_REVISION` 写入最终镜像的 `org.opencontainers.image.revision` 标签，供服务器核对版本。每个服务有独立构建缓存。

构建前和连接服务器前都查询当前 `main`。旧 CI 晚完成、构建期间 `main` 更新等情况会跳过过时发布。自动和手动发布共用并发组，`cancel-in-progress: false`，不会因下一次推送取消正在执行的部署。GitHub 不保证排队顺序，也可能替换等待中的运行，因此不依赖队列顺序保证版本正确。

Actions 页面可手动运行 `Deploy`，分支必须选择 `main`；`service` 可选 `all` 或单个服务。手动发布同样要求当前 `main` 的确切 SHA 已通过 push CI，不能借此跳过检查或部署历史提交。

## GitHub 配置

在仓库的 Actions Variables 中配置：

| 变量 | 值 |
| --- | --- |
| `TCR_NAMESPACE` | 沿用现有 TCR 命名空间 |
| `IMAGE_PREFIX` | 沿用现有镜像前缀；省略时为 `sast-shop` |

在仓库或 `production` Environment 的 Actions Secrets 中配置：

| Secret | 用途 |
| --- | --- |
| `TCR_USERNAME` | TCR 推送及拉取账号；构建 job 使用，必须可在仓库级读取 |
| `TCR_PASSWORD` | TCR 推送及拉取凭证；构建 job 使用，必须可在仓库级读取 |
| `SERVER_HOST` | `sast.fun` |
| `SERVER_USER` | 服务器部署专用账号，须有下述 helper 的定向 sudo 权限 |
| `SSH_PRIVATE_KEY` | 该部署账号的专用 SSH 私钥 |
| `SERVER_SSH_FINGERPRINT` | 经可信渠道核验的 SSH 主机密钥 SHA256 指纹 |

`production` Environment 可配置分支限制与人工审批；如果要求完全自动发布，不启用强制人工审批。自动触发的工作流文件必须存在于默认分支；本项目默认分支应为 `main`。工作流 token 只申请 `contents: read` 和 `actions: read`。

部署 job 使用 `production` Environment。该环境若配置了同名 `TCR_USERNAME` / `TCR_PASSWORD` Secret，会覆盖仓库级值；对应账号必须有目标镜像的拉取权限。

不要把服务器登录密码、数据库密码、SSH 私钥或 TCR 凭证提交到仓库。SSH action 固定到已核验的 v1.2.2 提交 `2ead5e36573f08b82fbfce1504f1a4b05a647c6f`，并强制提供主机指纹。

## 服务器部署约定

工作流只调用预先安装且由 root 管理的 helper：

```sh
python3 -c 'import json, os, sys; json.dump({"registry": "ccr.ccs.tencentyun.com", "username": os.environ["TCR_USERNAME"], "password": os.environ["TCR_PASSWORD"]}, sys.stdout)' | \
  sudo -n /usr/local/lib/sast-shop/deploy-image "$SERVICE" "$REVISION" \
    "ccr.ccs.tencentyun.com/$TCR_NAMESPACE/$IMAGE_PREFIX-$SERVICE:sha-$REVISION" --registry-stdin
```

工作流通过 SSH 环境转发 TCR 凭证，再把 `{registry, username, password}` JSON 直接经管道送入 helper 的标准输入。凭证不放入命令行参数、不输出到日志。helper 使用临时 Docker 配置登录并拉取私有镜像，结束后清除临时配置，不在服务器持久保存 registry 凭证，也不依赖手动创建的 `registry.json`。服务器需安装 Python 3；不得对这段凭证传递脚本启用 shell tracing。

helper 与其配置应仅允许 root 写入，部署账号只应获得该 helper 的定向 sudo 权限。其部署操作须限制在本商城独立 Compose 项目内：校验服务、完整 SHA、镜像仓库和版本标签；持有共享发布锁；拉取镜像后只重建目标服务；检查就绪状态；失败时恢复该服务之前的镜像并返回失败。

工作流遇到任一服务发布失败就停止后续服务。回滚以单服务为单位，已成功发布的前序服务仍保留新版本；六服务发布不是跨服务事务。发布涉及不兼容协议或数据库迁移时，需要单独安排兼容性与迁移顺序。数据库迁移和已有数据导入不由此工作流自动执行。

工作流不会执行全局 Docker 清理、重启已有数据库或变更全局反向代理。服务器 Compose、环境变量、域名路由和 helper 的首次安装及运维修改需独立处理。

首次启用时，先完成服务器环境和以上 GitHub 配置，再推送到 `main`。确认 `CI`、六个 `Build & push` job 和 `Deploy services in sequence` 成功，并核对 `api.sast.fun` 及服务器服务就绪状态。仅提交工作流文件不代表外部 Secrets 或服务器授权已经配置完成。

参考：[GitHub workflow_run 触发器](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run)、[工作流运行查询 API](https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-workflow)。
