# 小洞专用单点登录

本目录集中维护影策、小洞 PHP 后端、小洞 Vue 前端的 SSO 文档。总配置、协议概览、数据、测试结果和待验收事项以本文件为准；各端实现细节放在同目录专题。上游专题文档不重复维护本功能，影策文档总索引只保留入口。

| 文档 | 内容 |
| --- | --- |
| [README](README.md) | 三端总入口、配置、部署、账号边界、验收与上游更新 |
| [PHP 后端接入](php.md) | 小洞接口、授权码表、独立迁移与 PHP 测试 |
| [前端入口与授权页](frontend.md) | 小洞菜单、路由、授权页、实施记录与前端测试 |
| [本轮文件清单](CHANGES.md) | 每个文件的增删行数、移除位置与验证结果 |

所有新增的影策 SSO 代码、测试和三端说明文档均集中到本目录，不再在 app、auth、handler、repository、service 下新增适配文件。宿主原有文件只保留能力接口与调用入口，具体差异见末尾清单。

小洞影视的左侧「创作」栏目提供「画布/短剧」入口，新标签页完成免登后进入影策创作页。小洞证明会员身份，影策创建自己的普通用户与登录会话。两个平台的充值、积分、消费和会员权限分别管理，不同步密码、余额或管理员权限。

本功能默认关闭。交付包含三端代码、测试、示例配置和小洞新增表 SQL；不会自动修改真实 `.env`、执行业务数据库升级或启动服务。

## 你需要做的操作

以下步骤用于已有影策 Docker 部署和已有小洞站点。真实域名与站点编号尚未提供，示例不能原样用于生产。

1. **更新程序。** 将本次小洞 PHP 后端、Vue 前端及影策后端代码发布到对应项目。影策先用原有登录方式创建好管理员账号。
2. **给小洞新增一张表。** 备份小洞数据库，打开 `ai_cloooud/niucloud/addon/ht_hot/sql/canvas_sso.sql`，把 `{{prefix}}` 替换为当前数据库表前缀，然后只执行这个 SQL 文件。不要执行整个历史 `sql.txt`。
3. **生成一次密钥。** 执行 `openssl rand -hex 32`，将输出保存好。下一步的两个 `.env` 使用完全相同的密钥。
4. **填写两个配置文件。** 小洞后端填写 `ai_cloooud/niucloud/.env` 的 `[CANVAS_SSO]` 段；影策填写 `open-ai-canvas/.env`。具体内容见下面两段配置。将示例域名和站点编号改成自己的值，将密钥填到两边；准备启用时，小洞 `ENABLED` 和影策 `CANVAS_CLOOOUD_SSO_ENABLED` 都设为 `true`，允许首次自动建号时影策 `CANVAS_CLOOOUD_SSO_AUTO_REGISTER` 也设为 `true`。
5. **让配置生效。** 小洞按原部署方式重新加载 PHP 服务；影策在项目根目录按下方 Docker 命令构建并更新后端。无需再手工修改 Compose 文件，也不需要增加启动脚本。小洞前端不填写密钥。
6. **检查结果。** 用没有登录影策的浏览器，先登录小洞，点击左侧「画布/短剧」。预期新标签页直接进入影策创作页；退出影策后再次从小洞进入，仍是同一个账号。若提示影策存在原账号，先关闭其他影策标签页并退出原登录，再进入。

未执行上述部署和配置前，新增菜单可能显示“未启用”；这不等于已经完成真实环境免登。

## 登录过程与账号边界

1. 小洞前端的 `/canvas` 页面要求已有小洞登录状态，通过会员 API 获取影策固定入口。
2. 浏览器访问影策 `/api/auth/cloooud/start`，影策生成 state 与 PKCE verifier，设置绑定该浏览器的 HttpOnly Cookie，然后跳转小洞前端 `/canvas/authorize`。
3. 小洞前端使用原有会员 Token 调用 PHP 授权接口。PHP 校验固定客户端、站点与回调地址，签发 60 秒内有效的单次授权码，浏览器跳转影策回调。
4. 影策校验浏览器 state，由后端携带客户端密钥和 PKCE verifier 兑换会员身份，再签发影策自己的登录 Cookie。

小洞 Token 只用于小洞自己的 API；不放入跳转 URL，也不交给影策。客户端密钥只保存在双方后端。浏览器 URL 中的 code 是一次性凭证，仍须在代理和 APM 日志中脱敏。

影策身份映射使用 provider `cloooud`，subject 为 `SHA256(issuer):site_id:member_id`。同一身份重复登录复用同一用户；同名、同邮箱或同手机号的历史账号不会自动合并。首次建号要求显式开启 `AUTO_REGISTER`，它是独立的可信身份来源建号开关，不受本地公开注册开关控制；该流程不展示影策本地注册协议，启用前应在小洞的会员协议中覆盖此身份接入。新用户始终为普通用户，只沿用影策本地新用户赠送政策。

启用前必须先完成影策本地管理员初始化。不要将小洞管理员当作影策管理员；现有本地登录仍可使用。不要随意修改 issuer 或 site_id，否则会被识别为不同身份来源。本期没有历史账号绑定或合并界面。

影策已有同一身份的会话时允许重复免登；存在其他账号或无法验证的旧 Cookie 时拒绝替换，不创建新账号或签发新会话，并保留明确的 HTTP 403 提示。切换账号前，先关闭其他影策标签页，在剩余页面退出原登录，再从小洞重新进入；这避免旧画布内存与新账号 Cookie 混用。也可使用独立浏览器资料隔离不同账号。

两端退出登录分别生效，本期不包含统一退出。小洞在签发和兑换时重新校验会员、站点状态；已经签发的影策会话不持续查询小洞状态，因此小洞禁用会员不会立即注销已有影策会话。需要立即停用时，还需在影策禁用对应账号。

## 小洞后端配置与升级

在 `ai_cloooud` 仓库使用独立新增表文件 `niucloud/addon/ht_hot/sql/canvas_sso.sql`，确认数据库前缀、备份后再替换文件中的 `{{prefix}}` 并执行。不要执行整份历史 `install.sql` 或根 `sql.txt` 来升级现有站点。

PHP 的 `niucloud/.env` 配置模板如下，所有域名均为示例：

```ini
[CANVAS_SSO]
ENABLED = false
CLIENT_ID = "canvas"
CLIENT_SECRET = ""
ISSUER = "https://identity.example.com"
SITE_ID = 1
START_URL = "https://canvas.example.com/api/auth/cloooud/start"
REDIRECT_URL = "https://canvas.example.com/api/auth/cloooud/callback"
```

`CLIENT_SECRET` 通过密码管理器或 `openssl rand -hex 32` 生成，双方后端使用同一值，不提交仓库。允许 32–512 位非空格 ASCII 字符；建议使用生成的 64 位十六进制值。`CLIENT_ID` 允许 1–128 位字母、数字、点、下划线和连字符。`ISSUER` 是稳定的身份提供方标识，双方逐字一致，不要求它提供 OIDC 服务。`SITE_ID` 必须等于小洞前端实际使用的站点编号。

PHP 按 ThinkPHP `env('canvas_sso.*')` 读取；操作系统变量回退名为 `PHP_CANVAS_SSO_ENABLED`、`PHP_CANVAS_SSO_CLIENT_ID` 等，不能把普通 shell 的 `CANVAS_SSO_*` 当作已生效配置。独立接口及新增表维护说明见 [PHP 后端接入](php.md)。

## 影策后端配置

下列配置必须传入实际运行的 Go 进程。SSO 示例集中在本文件，不再追加到上游根目录 `.env.example`；复制示例不会自动启用功能。

```dotenv
CANVAS_CLOOOUD_SSO_ENABLED=false
CANVAS_CLOOOUD_SSO_AUTO_REGISTER=false
CANVAS_CLOOOUD_SSO_CLIENT_ID=canvas
CANVAS_CLOOOUD_SSO_CLIENT_SECRET=
CANVAS_CLOOOUD_SSO_ISSUER=https://identity.example.com
CANVAS_CLOOOUD_SSO_SITE_ID=1
CANVAS_CLOOOUD_SSO_AUTHORIZATION_URL=https://design.example.com/canvas/authorize
CANVAS_CLOOOUD_SSO_EXCHANGE_URL=https://identity.example.com/api/ht_hot/canvas_sso/exchange
CANVAS_CLOOOUD_SSO_REDIRECT_URL=https://canvas.example.com/api/auth/cloooud/callback
```

| 环境变量 | 值或含义 |
| --- | --- |
| `CANVAS_CLOOOUD_SSO_ENABLED` | 默认关闭；双方部署完成后显式设为 `true` |
| `CANVAS_CLOOOUD_SSO_AUTO_REGISTER` | 默认 `false`；允许小洞会员首次自动创建影策普通用户时设为 `true` |
| `CANVAS_CLOOOUD_SSO_CLIENT_ID` | `canvas`，与 PHP 完全一致 |
| `CANVAS_CLOOOUD_SSO_CLIENT_SECRET` | 与 PHP 相同的后端密钥 |
| `CANVAS_CLOOOUD_SSO_ISSUER` | `https://identity.example.com`，与 PHP 完全一致 |
| `CANVAS_CLOOOUD_SSO_SITE_ID` | 与 PHP 及小洞前端一致的正整数 |
| `CANVAS_CLOOOUD_SSO_AUTHORIZATION_URL` | 小洞前端完整授权页地址，例如 `https://design.example.com/canvas/authorize` |
| `CANVAS_CLOOOUD_SSO_EXCHANGE_URL` | PHP 完整 API 地址，例如 `https://identity.example.com/api/ht_hot/canvas_sso/exchange` |
| `CANVAS_CLOOOUD_SSO_REDIRECT_URL` | `https://canvas.example.com/api/auth/cloooud/callback`，与 PHP 的 `REDIRECT_URL` 完全一致 |

URL 必须为 HTTPS，不带查询参数、用户信息或片段。本地回环 `localhost`、`127.0.0.1`、`[::1]` 可以用 HTTP；可信私网兑换接口还必须通过 `CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS` 精确放行主机。不要启用全局私网放行。兑换请求不跟随重定向，因此 `EXCHANGE_URL` 应直接指向最终接口。

影策 start 与 callback 必须使用影策网站同一来源。通过网站反向代理访问 `/api`，不要从网站进入后跳转不同主机的裸后端，否则浏览器的 state 或会话 Cookie 无法匹配。PHP 的 `START_URL` 与 `REDIRECT_URL` 也会校验同源。生产代理应正确传递可信的 `X-Forwarded-Proto`，保证 Cookie 的 Secure 标志。

小洞前端保留现有 API、站点和路由 base 配置，无需增加前端密钥。若前端部署在子路径，例如 `/ai/`，则授权地址应为 `https://design.example.com/ai/canvas/authorize`。网站必须将该深层路由回退到 SPA 入口；小洞登录成功的回跳须保留完整授权查询参数。

### Docker 配置位置

SSO 配置填写在影策项目根 `.env`，即与 `docker-compose.deploy.yml` 同一目录的文件。部署文件已接好这 9 个变量，使用者不需要再修改 YAML 或编写脚本。此次开发没有修改你的真实 `.env`。

首次部署这份定制代码，在影策项目根目录执行以下两条命令。要求已有 PostgreSQL、Redis 正常运行，原部署 `.env` 完整；命令不会启动依赖服务或执行迁移服务：

```sh
docker compose -f docker-compose.deploy.yml -f docker-compose.build.yml build backend
docker compose -f docker-compose.deploy.yml -f docker-compose.build.yml up -d --no-deps --force-recreate backend
```

第一条把本地修改编译进后端镜像；第二条用新镜像和 `.env` 重新创建后端容器。之后只改 `.env` 时执行第二条即可，单独 `restart` 不会更新容器环境变量。官方未包含此修改的预编译镜像不能直接使用本功能。

以上配置接入针对 `docker-compose.deploy.yml`，不能直接套用到其他 Compose 文件或宿主机 `go run`。此处是发布说明，本次没有执行镜像构建、容器更新或真实环境启用。

## 接口与数据

| 接口 | 作用 |
| --- | --- |
| 影策 `GET /api/auth/cloooud/start?next=/create` | 创建登录上下文并 302 跳转；next 仅允许安全的本站非认证页面路径 |
| 影策 `GET /api/auth/cloooud/callback` | 消费 `state` 与 `code`，成功后设置本地 Cookie 并 302 返回本站 |
| 小洞 `GET /api/ht_hot/canvas_sso/config` | 要求会员登录，返回固定 `start_url`、`redirect_uri`、`client_id` |
| 小洞 `POST /api/ht_hot/canvas_sso/authorize` | 要求会员登录，签发单次授权码并返回固定回调 URL |
| 小洞 `POST /api/ht_hot/canvas_sso/exchange` | 后端 form-urlencoded 请求，校验客户端密钥、PKCE 和 Site-id，返回最小身份信息 |

小洞成功信封是 `code: 1`，与影策业务 API 的 `code: 0` 不同，由专用兑换逻辑转换。影策回调无会话时失败会跳转登录页显示通用错误；已有会话时失败保持明确的 HTTP 403 JSON，不跳入旧账号工作区。start 的参数、限流或配置错误按影策统一错误信封返回。

影策复用 `o_auth_states`、`user_identities`、`users` 和 `auth_sessions`，不新增表。state 数据库存摘要，有效期 5 分钟，并用 HttpOnly、SameSite=Lax、host-only Cookie 绑定浏览器；PKCE verifier 仅保存在服务端。小洞新增 `hthot_canvas_sso_code`（带实际部署表前缀），只保存授权码摘要、身份/客户端绑定及有效期，事务内条件消费，不能重复兑换。

SSO 响应禁止缓存并设置 `Referrer-Policy: no-referrer`。影策应用访问日志去除 SSO 路径的整个查询串；小洞专用接口不挂载记录请求体的 `ApiLog`。部署时还需对反向代理、前端授权页访问日志及 APM 做对应脱敏，禁止记录 code、state、verifier、secret、会员 Token。过期授权码可按 `expires_at` 分批清理；本功能不增加计划任务。

## 验证与启用顺序

先部署 PHP 新增表与接口、小洞前端菜单和授权页、影策后端，再对齐配置并显式启用。恢复 `ENABLED=false` 可停止新的 SSO 登录；不要删除已有账号、身份映射或账务记录。关闭 SSO 不会注销已经签发的本地会话。

代码测试使用临时 SQLite、模拟身份端点和前端请求 mock；未连接真实小洞账号或业务数据库。PHP 与 Vue 的测试命令分别见 [PHP 后端接入](php.md)和[前端入口与授权页](frontend.md)。影策可在 `backend/` 执行：

```sh
GOCACHE="$PWD/../.local/cache/go-build" \
GOMODCACHE="$PWD/../.local/cache/go-mod" \
go test -p 1 ./internal/integrations/cloooud ./internal/auth ./internal/handler ./cmd/server -count=1
```

在影策项目根目录执行 `python3 backend/internal/integrations/cloooud/deploy_test.py`，可验证 `.env` 变量确实传入后端。该检查要求已安装 Docker Compose，只读取临时测试配置，不读取实际 `.env`，不创建或启动容器。

上线前仍需在目标环境验收：

- 小洞已登录、未登录后回跳、Token 过期、请求中切换账号；授权页失败后重新发起。
- 影策首次普通用户创建、重复登录复用、其他已登录账号拒绝切换、用户缓存隔离、两端原有登录仍可用。
- state 不匹配、授权码到期/重放、错误租户/issuer/client/回调/PKCE、停用会员和影策账号；MySQL 并发兑换只有一次成功。
- 跨站 Cookie、HTTPS 代理、子路径路由、浏览器与代理日志、后台权限；两端充值和消费仍各自记账。

这些实际浏览器、MySQL 与部署检查尚未完成，不能以隔离测试替代。后续验收结果统一更新本文件，不再分散追加到上游待测试、功能清单或数据库文档。

### 已执行的验证

- 影策独立模块及 `internal/auth`、`internal/handler`、`cmd/server` 测试通过；覆盖浏览器绑定、重放、租户/issuer、禁用账号、同账号复用、异账号会话拒绝和日志查询串隐藏。
- 真实宿主接入测试覆盖启动入口的装配方式、模块回调与原生认证、Cookie 签发、原生会话识别和退出，以及实际 OpenAPI 路由合并结果。另有策略失败、登录期间被管理员禁用时禁止建会话的回归测试。上游身份服务使用临时模拟端点，数据库为内存 SQLite。
- Docker 配置测试验证 9 个变量逐一传入后端、未配置时保持关闭、其他容器不接收客户端密钥；测试未读取真实部署配置。
- 小洞 PHP 79 项隔离检查、前端 57 项专项测试及类型检查/构建在先前实现阶段通过。本轮仅整理影策代码，PHP/Vue 源码未变；不将先前结果描述为本轮重跑或真实浏览器验收。
- 未修改真实 `.env`、执行业务迁移或部署。

## 当前代码接入点与上游更新

主体文件位于本目录：`sso.go`、`config.go`、`exchange.go`、`http.go`、`native.go`、`metadata.go`、`openapi.yaml`；专项验证为四个 `*_test.go` 和 `deploy_test.py`。`native.go` 集中维护原生登录能力的调用顺序和登录时间条件更新，不复制原生会话生成算法。

下面列出独立包之外全部 7 个已有文件的接入点，路径相对于影策仓库根目录。外部不再新增文件，SSO 业务流程和数据库更新均留在模块中。

| 职责 | 当前文件 |
| --- | --- |
| 启动装配与日志 | `backend/cmd/server/main.go`：统一注册模块路由，将数据库和宿主能力传入模块，并调用模块日志脱敏函数 |
| 宿主 HTTP 能力 | `backend/internal/handler/auth.go`：短函数返回原有错误信封、限流、Cookie 函数 |
| 原生会话能力 | `backend/internal/auth/auth.go`：短函数返回原有会话生成方法，由模块先完成身份和状态校验后调用 |
| 宿主业务能力 | `backend/internal/app/auth_bridge.go`：短函数返回当前用户校验、会话生成、本地新用户政策和活动记录能力 |
| 运行时 OpenAPI | `backend/internal/handler/api.go`：调用模块合并规范；原 `handler/openapi.yaml` 保持上游内容 |
| Docker 配置传递 | `docker-compose.deploy.yml`：后端 environment 集中增加 9 行 |
| 文档入口 | `docs/index.md`：仅一行链接 |

先前新增的 `app/cloooud_bridge.go`、`auth/external_login.go`、`handler/cloooud.go`、`repository/external_login.go`、`service/aliases_cloooud.go` 已全部移除。原 `repository/oauth.go`、`handler/openapi.yaml` 和 `cmd/server/main_test.go` 保持上游内容。Go 集成不修改通用认证响应来展示小洞身份来源，外部身份映射仍保存在原有身份表中。模块通过注入的函数继续使用原生 Cookie、限流和会话策略。

后续合并上游时，重点核对路由注册、会话/Cookie、用户与身份表、出站访问保护和日志入口是否改变，再运行本文件列出的认证测试。不能仅以 Git 无冲突判断接入仍然有效。保留其他本地修改，不通过整体覆盖上游文件解决冲突。
