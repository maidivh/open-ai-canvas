# 小洞 PHP 后端接入

所属仓库：`ai_cloooud`。本文中的代码、SQL 和测试路径均相对于该仓库，不能在影策仓库直接执行 PHP 命令。总配置、账号边界和三端验收记录统一维护在 [README](README.md)。

本功能只向影策证明当前小洞会员身份，不同步登录密码、邮箱、会员等级、余额或点数，不调用钱包、充值与消费逻辑。影策创建自己的登录会话；小洞会员 Token 不传给影策。默认关闭，不影响原有登录方式。

## 配置

在部署环境的 `niucloud/.env` 增加独立 INI section，使用 [README 的小洞后端配置模板](README.md#小洞后端配置与升级)。客户端密钥只保存在双方后端，不能放进 Vue 前端配置或构建产物；这里不再维护第二份配置示例。

配置读取遵循当前 ThinkPHP `env('canvas_sso.*')` 行为：INI section 与框架环境配置均可使用；操作系统环境变量回退名称为 `PHP_CANVAS_SSO_ENABLED`、`PHP_CANVAS_SSO_CLIENT_ID` 等。不要把普通 shell 的 `CANVAS_SSO_*` 当成已生效的 PHP 配置。

`ISSUER` 是稳定身份提供方标识，必须与影策配置逐字相同；身份键是 `(issuer, site_id, member_id)`。启用后不要随意更换 issuer 或 site_id。URL 只接受 HTTPS，开发允许 `localhost`、`127.0.0.1`、`[::1]` 的 HTTP；不接受用户信息、查询参数、片段、反斜杠。start 与 callback 必须同源，路径固定如上。callback 逐字匹配配置，不接受任意外部跳转地址。

## 升级

1. 备份并确认当前数据库前缀，在独立迁移 `niucloud/addon/ht_hot/sql/canvas_sso.sql` 中替换 `{{prefix}}`，仅执行该文件的新增建表语句。
2. 部署插件 PHP 文件和路由、PC 端授权页以及影策后端。不要为升级执行整份 `install.sql` 或根 `sql.txt`，它们包含其他历史语句。
3. 对齐双方 client_id、secret、issuer、site_id、回调 URL，再由运维显式启用。当前交付不会修改 `.env` 或执行数据库迁移。
4. 验证失败可将 `ENABLED` 恢复为 false；保留表和历史记录，不需要删除会员或影策用户。

新增 `hthot_canvas_sso_code` 表保存 SHA-256 授权码摘要、site/member/client、callback、PKCE challenge、签发/到期/消费时间。有效期固定 60 秒，唯一索引防止摘要重复；事务内重新检查会员归属、状态及删除标记、站点状态与有效期，再通过条件更新保证最多成功兑换一次。表没有 Token、密码或客户端 secret 字段。维护时可按 `expires_at` 分批清理过期记录；本功能不新增计划任务。

建表 SQL 已为全部 10 个字段添加中文 `COMMENT`，可直接在数据库工具中查看。`expires_at`、`consumed_at`、`create_time` 均为秒级 Unix 时间戳，`consumed_at=0` 表示尚未兑换。已存在但缺少字段注释的表，在确认结构与 `canvas_sso.sql` 一致并备份后，将 `niucloud/addon/ht_hot/sql/canvas_sso_comments.sql` 中的 `{{prefix}}` 替换为实际表前缀，只执行这个注释更新文件；无需重建表。重新执行 `CREATE TABLE IF NOT EXISTS` 不会补全已有表的注释。

本地已执行注释更新，并逐项核对 10 个字段的注释、类型、默认值、排序规则、自增属性和索引；仅注释发生变化。执行前的表结构保存在影策 `.local/sso-config-backups/` 下，服务器数据库未执行此次更新。

## 协议

三条 API 都使用小洞标准 `{code,data,msg}` 信封，成功 code=1；错误为非 2xx 与 code=0。SSO 控制器响应为 `Cache-Control: no-store`，不会将请求正文写入业务日志。独立路由不挂载 `ApiLog`，兑换错误也不会交给会记录请求参数的全局异常处理器。反向代理/APM 的请求体抓取和带查询参数的 callback 访问日志须由部署侧脱敏；不得记录 code、verifier、secret 或会员 Token。

| 接口 | 鉴权及输入 | 成功 data |
| --- | --- | --- |
| `GET /api/ht_hot/canvas_sso/config` | 原会员 Token；显式 `Site-id`，按现有客户端传 `channel` | `start_url, redirect_uri, client_id` |
| `POST /api/ht_hot/canvas_sso/authorize` | 原会员 Token；`Site-id`、`channel`；正文 `response_type=code, client_id, redirect_uri, state, code_challenge, code_challenge_method=S256` | `redirect_url` |
| `POST /api/ht_hot/canvas_sso/exchange` | 仅影策后端；`Site-id`、`channel`；`application/x-www-form-urlencoded` 正文 `grant_type=authorization_code, client_id, client_secret, redirect_uri, code, code_verifier` | `issuer, site_id, member_id, username, display_name` |

授权和配置路由沿用 `ApiCheckToken(true)`；兑换路由不要求会员 Token，以配置中的 client_secret 进行恒定时间比较，并验证明确的 Site-id。`channel` 沿用调用方约定，不用它推导租户。输入字段必须为字符串，未知正文参数拒绝；state 为 32–256 位 URL-safe 字符，challenge 为 43 位 base64url，verifier 为 43–128 位 RFC 7636 字符。

流程：会员点击进入影策 → 浏览器访问影策 start → 影策设置绑定浏览器的 state/PKCE 并跳转 PC `/canvas/authorize` → PC 授权页通过会员 Token 调用 PHP authorize → 浏览器返回已配置 callback → 影策后端兑换 code → 影策签发自身会话。登录跳转回来时须保留授权参数；只有影策 start 产生的浏览器上下文才可完成回调。

影策若已有同一身份的有效会话，可重复免登；若已登录其他账号或旧 Cookie 无法验证，会拒绝替换并保留 HTTP 403 提示。切换前先关闭其他影策标签页，再退出原登录后重新进入，避免旧画布内存与新账号 Cookie 混用。两端退出分别生效；小洞停用会员阻止新的签发/兑换，但不会立即注销已经建立的影策会话，需要立即停用时还应在影策禁用对应用户。

## 验证

从 `ai_cloooud` 仓库根目录执行（PHP CLI 必须具备 PDO SQLite）：

```sh
php niucloud/addon/ht_hot/tests/canvas_sso.php
```

测试加载 vendor autoload 与框架 helper，并构造未初始化的隔离容器，使用临时 SQLite；不启动 ThinkPHP 应用、不读取部署 `.env`、不访问真实数据库。覆盖 RFC 7636 S256、成功与重放、到期边界、错误 client/secret/PKCE/callback/tenant、缺失会员、会员禁用/删除/迁移、站点禁用/到期、明文不落库、配置 URL 边界、控制器信封和错误隔离；具备 pcntl 时额外执行并发兑换。SQLite 并发竞争可能以数据库忙拒绝其中一个请求，测试要求恰好一次成功并禁止再次兑换。

上线前仍需实际 MySQL 迁移与并发验证、浏览器跨站登录联调、Nginx/APM 日志脱敏检查。SQLite 测试不等于这些部署验收。需验收：已登录和未登录入口、错误 state/回调、过期和重放、跨租户、停用会员/站点；确认影策 Cookie 正常、小洞 Token 未出现在 URL、双方点数保持独立。
