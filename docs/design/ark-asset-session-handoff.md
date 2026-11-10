# 影策 · 橙幻素材库接入会话交接

> 用途：换电脑后继续本会话。分支、生产部署、配置、验证结果和未完成事项都在这里。
>
> 约束：这是二开功能，**不要合并到 `main` / `medivh_dev`，不要提交 PR**。

## 1. 分支与提交

- 分支：`feat/videoclaw-asset-provider`
- 远端：`fork` = `git@github.com:maidivh/open-ai-canvas.git`
- 基线：`medivh_dev` @ `c4ca8ab3`
- 本会话提交（从旧到新）：
  - `ab0045ae` feat: 支持 Video Claw 供应商与站点隔离
  - `69a0c4ec` docs: 补全真人认证官方流程与二期边界
  - `fd616c3a` feat: 增加 Wantrace 控制面站点（中间方案，后来被 `c5ffe8f9` 收敛）
  - `c5ffe8f9` fix: 接口固定为 api.wantrace.com
  - `eda28846` fix: 站点名称改为橙幻
  - `801e21ef` docs: 一期真实生成验收结果
  - `b9a03baa` feat: 真人认证回调兼容缺失 resultCode
  - `cf5f45ea` fix: 供应商不匹配时先跳过再校验凭据

新电脑：

```bash
git clone git@github.com:maidivh/open-ai-canvas.git
cd open-ai-canvas
git checkout feat/videoclaw-asset-provider
cat docs/design/ark-asset-session-handoff.md
```

已有仓库则：

```bash
git fetch fork
git checkout feat/videoclaw-asset-provider
git pull fork feat/videoclaw-asset-provider
```

## 2. 做了什么

- 方舟素材库新增 `videoclaw` 站点：
  - 接口固定 `https://api.wantrace.com/api/v1/private/sd2/assets`
  - `open.feeling.ltd` 只是厂商控制台，不作为接口域名
  - Region 固定 `cn-beijing`，ProjectName 默认 `default`
- `ArkPrivateAssetBinding` 增加 `site`，唯一键改为 `(resource_id, project_name, site)`，Schema 94。
- 渠道 BaseURL 供应商识别：
  - `api.wantrace.com` / `open.feeling.ltd` → `videoclaw`
  - `*.volces.com` / `*.volcengineapi.com` → `volcengine`
  - `*.byteplus*` → `byteplus`
  - 明确不匹配时跳过自动同步，保留公网 URL；未知代理保持原行为
- Video Claw 签名前不发送 `Accept`，签名后补回，保证 `SignedHeaders` 与文档一致。
- 真人认证回调容错：
  - `resultCode=10000` 或缺失 → 继续查 `GetVisualValidateResult`
  - `resultCode` 非 10000，或缺失但 `algorithmBaseRespCode` 非 0 → 判失败并退款
- 管理页站点名显示「橙幻」，Region/ProjectName 只读。
- 文档：`docs/design/ark-asset-provider-videoclaw.mdx`、`http-api.mdx`、`code-map.mdx`、`backend-database.mdx`、`pending-test.mdx`。

## 3. 已验证

- 本地真实生成：02:30 的橙幻任务两张参考图均为 `asset://asset-...`，上游 200；绑定表 `site=videoclaw` 两条 active。
- 本地 02:44 火山引擎任务保留 TOS 公网 URL，未串供应商。
- 后端 `go test ./internal/app` 全量通过；`verify-before-push` 11 项 0 失败。
- 正式站：Schema 94 ready，backend/web healthy；橙幻模型测试 `POST /api/admin/channels/CHANNEL_000008/models/test` 返回 200，耗时约 154 秒。

## 4. 正式站部署与配置

- 正式站：`https://dj.cloooud.com`
- 更新方式：项目根执行 `bash .local/deploy/update.sh all`（脚本上传当前工作区源码，不会 git pull；`.local` 未入库，新电脑若没有需从旧电脑复制）
- 本次部署前备份：`backups/20261111-033337-1478643`
- 已复制到正式站：
  - 渠道「橙幻」：`https://api.wantrace.com`，模型 `doubao-seedance-2-0-mini-260615`，协议 `volcengine-ark-video`，480P/720P 两档价格
  - 渠道 API Key：已用正式站 `.settings-key` 重新加密写入，渠道已启用
  - 素材库：`site=videoclaw`、`region=cn-beijing`、`projectName=default`、可信素材同步开启、真人/虚拟素材组开启
  - 素材库 AK/SK：已用正式站密钥重新加密写入
  - 真人回调地址：`https://dj.cloooud.com`
  - 生产 `.env` 已设置 `CANVAS_PUBLIC_BASE_URL=https://dj.cloooud.com`
- 前台模型「Seedance 视频」尚未挂橙幻模型路由；需要另一个会话或管理员在「前台模型」里补。

## 5. 当前阻塞

1. **真人认证 503**
   - 正式站发起真人认证报：`方舟素材库请求失败（HTTP 503）：ServiceUnavailable 真人认证暂时关闭，请稍后重试`
   - 请求已到达橙幻，AK/SK/签名/回调地址正常；是厂商侧 `CreateVisualValidateSession` 服务不可用或账号没有 `sd2:face` / 真人认证权限。
   - 已建议联系橙幻确认：`sd2:face` 权限、账号实名/企业认证、真人认证供应商可用性。
2. **前台模型路由缺失**
   - 正式站工作台暂时选不到橙幻模型，不影响素材库接口测试。
3. **`/api/wallet` 500**
   - 生产日志持续出现，与素材库无关，疑似共享钱包既有问题，需单独排查。

## 6. 新电脑继续开发

- 本地数据目录：`backend/` 下运行
  ```bash
  CANVAS_BACKEND_DATA_DIR=../.local/project-workbench-debug go run ./cmd/server
  ```
- 前端：`cd web && bun run dev`
- 生产密钥在正式站 `/data/.settings-key`，不要复制本地 `.settings-key` 覆盖；跨环境密钥必须重新加密。
- 不要合并 `main` / `medivh_dev`，不要开 PR。

## 7. 关键文件

- `backend/internal/app/provider_ark_private_assets.go`
- `backend/internal/app/settings_ark_assets.go`
- `backend/internal/app/ark_portraits.go`
- `backend/internal/database/migrations.go`（v94）
- `backend/internal/model/models_platform.go`
- `backend/internal/repository/repository_channels_settings.go`
- `web/src/pages/admin/settings/ark-private-assets-settings-page.tsx`
- `web/src/services/api/auth.ts`
- `docs/design/ark-asset-provider-videoclaw.mdx`
