# 小洞前端入口与授权页

所属仓库：`ai_cloooud_design`。本文中的代码与测试路径相对于该仓库。总配置、三端接口概览和验收记录统一维护在 [README](README.md)，PHP 接口细节见 [PHP 后端接入](php.md)。

## 产品边界

桌面侧栏「创作」组新增「画布/短剧」，通过原生链接新标签打开 `/canvas`，保留应用部署 base。授权只打通身份，不共享积分、充值、卡密或支付。手机导航与首页不新增入口；跳转页本身可在窄屏阅读和操作。

## 页面与职责

- `CanvasSsoView.vue`：入口与授权共用的轻量跳转页，无需加载 MainLayout。正常请求立即执行，不展示品牌、标题、卡片或按钮；等待超过 500 毫秒才显示加载动画，只有失败时展示提示、重新进入与返回工作台。
- `useCanvasSso.ts`：单次配置获取、授权和导航；组件卸载、账号切换、路由变化时取消请求并丢弃结果。重试从 Go start 开始新事务，不自动重放失败的授权 POST。
- `canvasSso.ts`：纯函数验证配置 URL、单值授权 query 与回调 URL。
- `api/canvasSso.ts`：复用会员请求层，Token、Authorization 与 Site-id 仍由拦截器注入。

失败卡片沿用 TDesign 色彩与圆角变量，内容最大宽度 420px，窄屏保留两侧边距；按钮区域可以换行，错误通过 `role=alert` 宣告，返回工作台保留键盘焦点。加载动画通过 `role=status` 提供可访问名称，失败、重试或卸载时清理视觉计时器。500 毫秒仅控制动画，不延迟授权和跳转；浏览器仍会经过授权路由，不能保证零加载或地址栏不变化。

## 配置与数据合同

无需增加前端环境变量或密钥。会员后端提供登录保护的 `GET /ht_hot/canvas_sso/config`，响应 `{code:1,data:{start_url,redirect_uri,client_id}}`。入口和回调必须同源，路径分别为 `/api/auth/cloooud/start` 和 `/api/auth/cloooud/callback`，均不带查询参数或片段。生产使用 HTTPS；本地联调仅 localhost、127.0.0.1、[::1] 允许 HTTP。

Go start 跳到应用部署 base 下的 `/canvas/authorize`，携带且仅携带 `response_type=code`、`client_id`、`redirect_uri`、`state`、`code_challenge`、`code_challenge_method=S256`。客户端和回调精确匹配配置；state 为 64 位小写十六进制，challenge 为 43 位 base64url；重复、缺失及未知 query 均拒绝。

前端向 `POST /ht_hot/canvas_sso/authorize` 提交上述字段；成功响应 `{code:1,data:{redirect_url}}`。回跳链接必须匹配配置回调的 origin 与 path，仅允许 code 与 state 两个单值参数，code 为 64 位小写十六进制，state 匹配本次请求。URL 不允许用户名、密码或 fragment，会员 Token 不进入任何链接。

两条页面路由均 requiresAuth。401 复用请求层的登录跳转，完整保留 fullPath query。已登录用户访问带 redirect 的登录页时，路由守卫拆分 path/query/hash 后恢复，避免把完整 URL 当 path 导致授权参数丢失。

## 验证边界

专项测试覆盖真实菜单 base、新标签链接、保护路由、登录回跳、URL 白名单、异常/重复 query、过期授权、取消与账号切换、禁止重复请求，以及 320px 环境的组件内容和返回路径。jsdom 不提供真实布局测量，窄屏外观、跨域 Cookie、真实会员接口、Go 回调与最终登录仍需部署环境浏览器验收。未启动服务，未修改运行配置或数据库。

## 实施记录

1. 使用现有会员请求层新增配置 GET 与授权 POST，浏览器不保存 SSO 客户端密钥。
2. 先补 URL 和异步授权测试，再实现纯验证函数与页面私有 composable。
3. 桌面侧栏新增保留 router base 的原生新标签链接，两条独立页面路由添加登录保护。
4. 修正已登录用户访问登录页时丢失 redirect query 的路由行为，补真实守卫回归测试。
5. 执行专项 Vitest、vue-tsc 与构建；实际结果记录在 README，不将 jsdom 当作浏览器联调。
6. 会员请求层对主动取消保留 Promise 拒绝语义，但不弹出底层 `canceled` 提示，避免账号切换或卸载打断用户。

原有 `.env.production`、移动端数字人修改及根目录设计验收记录保持原样。

## 验证命令

在 `ai_cloooud_design` 仓库根目录使用现有依赖执行，不需要启动开发服务器：

```sh
npm test -- src/api/__tests__/canvasSso.spec.ts src/api/__tests__/canvasSsoAuth.spec.ts src/router/__tests__/canvasSso.spec.ts src/router/__tests__/mobileAccess.spec.ts src/views/canvas/__tests__ src/layouts/__tests__/MainLayout.spec.ts src/layouts/__tests__/MainLayout.mobile.spec.ts
npm run build
```

以上命令属于小洞前端仓库；影策 `web/` 仍按自身项目约定使用 Bun，不能混用依赖目录。
