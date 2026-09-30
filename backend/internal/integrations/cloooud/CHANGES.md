# SSO 归拢修正文件清单

本轮将五个包外新增适配文件全部移除，相关实现集中到 `backend/internal/integrations/cloooud/`。影策的新增代码、测试和三端说明文档均在此目录。模块外仅修改原有文件，用于接入宿主能力、路由、配置和文档入口。

## 相对当前 HEAD 保留的已有文件修改

共 7 个原有文件，增加 51 行、删除 2 行（含后续补充的接入注释）。包括前轮已完成的配置和文档入口，不代表这 7 个文件都是本轮新改。

| 文件 | 增加 | 删除 | 用途 |
| --- | ---: | ---: | --- |
| `backend/cmd/server/main.go` | +6 | -1 | 启动入口统一装配模块，调用日志脱敏 |
| `backend/internal/app/auth_bridge.go` | +11 | -0 | 提供原有用户、会话、新用户政策和活动记录能力 |
| `backend/internal/auth/auth.go` | +7 | -0 | 向可信服务端集成提供原生会话函数 |
| `backend/internal/handler/api.go` | +4 | -1 | 合并模块 OpenAPI 声明 |
| `backend/internal/handler/auth.go` | +11 | -0 | 提供原有 Cookie、限流和错误响应函数 |
| `docker-compose.deploy.yml` | +11 | -0 | 从根 .env 传递 SSO 配置 |
| `docs/index.md` | +1 | -0 | 模块文档入口 |

这些短接入点继续复用原有会话与 HTTP 策略；SSO 流程和登录时间更新在模块内维护。

## 归拢修正时的增删明细

对比本轮开始时的工作区，共 14 项，增加 147 行、删除 111 行；不包含本清单自身。以下路径均相对于影策仓库根目录。移除的是此前为本功能新增的未跟踪文件，没有删除上游原有文件。

| 文件 | 增加 | 删除 | 状态 |
| --- | ---: | ---: | --- |
| `backend/cmd/server/main.go` | +1 | -0 | 修改 |
| `backend/internal/app/auth_bridge.go` | +9 | -0 | 修改 |
| `backend/internal/app/cloooud_bridge.go` | +0 | -17 | 移除包外新增文件 |
| `backend/internal/auth/auth.go` | +5 | -0 | 修改 |
| `backend/internal/auth/external_login.go` | +0 | -33 | 移除包外新增文件 |
| `backend/internal/handler/auth.go` | +9 | -1 | 修改 |
| `backend/internal/handler/cloooud.go` | +0 | -15 | 移除包外新增文件 |
| `backend/internal/integrations/cloooud/README.md` | +9 | -11 | 更新集中维护说明 |
| `backend/internal/integrations/cloooud/integration_test.go` | +4 | -1 | 更新真实宿主装配测试 |
| `backend/internal/integrations/cloooud/native.go` | +56 | -0 | 集中原生登录编排与条件更新时间 |
| `backend/internal/integrations/cloooud/sso.go` | +6 | -5 | 通过注入能力调用原生登录 |
| `backend/internal/integrations/cloooud/sso_test.go` | +48 | -2 | 更新接线并补充失败路径测试 |
| `backend/internal/repository/external_login.go` | +0 | -20 | 移除包外新增文件 |
| `backend/internal/service/aliases_cloooud.go` | +0 | -6 | 移除包外新增文件 |

小洞 PHP、Vue 两个仓库本轮没有修改。真实 .env、业务数据库、部署环境均未变动。

## 验证结果

- 本轮独立模块、原认证、HTTP handler、服务器入口四个 Go 包测试通过。
- 补充策略失败、登录期间账号被禁用时禁止签发会话的测试后，模块测试再次通过。
- 宿主接入测试使用真实装配方式，验证模拟授权回调签发原生会话、原有认证识别会话、原有退出撤销会话及 OpenAPI 合并。
- 独立只读复核未发现 Important/Critical 问题；复核与运行测试分别完成。
- 前轮临时 .env 的 Docker Compose 配置检查通过；本轮 Compose 未再修改，未重跑该检查。
- 未做实际站点浏览器联调、MySQL 迁移或部署。PHP/Vue 先前测试未在本轮重跑。

## 接入注释补充

归拢完成后，在上述 7 个已有文件的接入处补充中文注释，说明各自作用及核心模块路径；不改变运行逻辑。本清单同步更新了相对 HEAD 的统计，前面的归拢明细保留当时数据。本次仅检查差异与空白格式，未重跑测试。
