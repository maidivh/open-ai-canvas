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
- 真人认证域名诊断（同一 AK/SK）：`ListAssetGroups` 在 `api.wantrace.com` 与 `open.feeling.ltd` 均返回 200；`CreateVisualValidateSession` 两个域名均返回 503 `ServiceUnavailable`，RequestId `ad6bd915390c4055b1010ba82f65a073`。说明凭据/签名正常，问题在橙幻真人认证服务或账号权限。
- 工作台渠道确认：用户截图显示 `seedance 2.0 mini` 有「2 个渠道」，橙幻已作为第二个渠道出现并带「支持真人」标签；不需要再往 `logical_model_routes` 加橙幻。

## 4. 正式站部署与配置

- 正式站：`https://dj.cloooud.com`
- 更新方式：项目根执行 `bash .local/deploy/update.sh all`（脚本上传当前工作区源码，不会 git pull；`.local` 未入库，新电脑若没有需从旧电脑复制）
- 本次部署前备份：`backups/20261111-033337-1478643`
- 已复制到正式站：
  - 渠道「橙幻」：`https://api.wantrace.com`，已启用
  - 渠道 API Key：已用正式站 `.settings-key` 重新加密写入
  - 模型（4 个，均带「支持真人」标签，协议 `volcengine-ark-video`）：
    - `doubao-seedance-2-0-mini-260615`：480P/720P 2300 积分/百万输出 Token
    - `doubao-seedance-2-0-260128`：480P/720P 4600，1080P 5100，2160P 2600
    - `doubao-seedance-2-0-fast-260128`：480P/720P 3700
    - `doubao-seedance-2-5-260628`：480P/720P 7000，1080P 7700
  - 价格按火山同模型现有档位复制；mini 价格按用户确认作为正式价；共 11 个价格档
  - 素材库：`site=videoclaw`、`region=cn-beijing`、`projectName=default`、可信素材同步开启、真人/虚拟素材组开启
  - 素材库 AK/SK：已用正式站密钥重新加密写入
  - 真人回调地址：`https://dj.cloooud.com`
  - 生产 `.env` 已设置 `CANVAS_PUBLIC_BASE_URL=https://dj.cloooud.com`
- 前台模型：工作台「平台服务」会直接列出已启用的系统渠道模型，橙幻已作为这些 Seedance 模型的第二个渠道出现（用户截图确认「2 个渠道」）。`logical_model_routes` 里仍只有 4 条火山路由，但这是前台模型页的路由列表，不影响工作台按渠道选择。

## 5. 当前阻塞与已知问题

1. **真人认证 503（等橙幻技术）**
   - 正式站发起真人认证报：`方舟素材库请求失败（HTTP 503）：ServiceUnavailable 真人认证暂时关闭，请稍后重试`
   - 已用同一 AK/SK 直接对两个域名验证：`ListAssetGroups` 均 200；`CreateVisualValidateSession` 均 503，RequestId `ad6bd915390c4055b1010ba82f65a073`。
   - 结论：AK/SK、签名、回调地址正常，问题在橙幻真人认证服务或账号权限。
   - 找橙幻技术确认：`sd2:face` / 真人认证权限、账号实名/企业认证、活体供应商可用性。
2. **`/api/wallet` 500（与素材库无关）**
   - 共享钱包为 `external` 模式，`/api/wallet` 要求当前用户有 `provider='cloooud'` 的 SSO 身份。
   - 管理员账号 `b1af...` 在 `user_identities` 中 0 条 cloooud 身份，触发普通错误「请从小洞AI免登后使用共享积分」，handler 映射成 500；SSO 用户有身份，钱包正常。
   - 远程钱包 `https://ai.cloooud.com` 可达（无签名请求 401），不是网络/服务故障。
   - 未修改。后续修法：管理员走小洞 SSO/绑定身份，或把该条件映射成 401/403 + reason，并让前端停止轮询、显示明确提示。
3. **视频/音频素材上传（等橙幻技术）**
   - 当前素材库只允许图片；Seedance 2.5 的参考视频/音频、真人素材的视频/音频还没做。
   - 需要橙幻确认 `AssetType=Video/Audio` 的支持范围和人脸一致性校验要求。
4. **真人认证错误提示**
   - 现在界面直接显示上游 `{"Code":...,"Message":...}`；等 503 问题解决后再决定是否改成更友好的文案。

## 6. 新电脑继续开发

- 先读本文件，再按分支和提交继续。
- 本地数据目录：`backend/` 下运行
  ```bash
  CANVAS_BACKEND_DATA_DIR=../.local/project-workbench-debug go run ./cmd/server
  ```
- 前端：`cd web && bun run dev`
- 生产密钥在正式站 `/data/.settings-key`，不要复制本地 `.settings-key` 覆盖；跨环境密钥必须重新加密。
- 生产数据变更（渠道模型、价格、标签、素材库设置）**不在 git**，以本文件记录为准；直接改 DB 后需要重启 backend 刷新模型目录。
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
