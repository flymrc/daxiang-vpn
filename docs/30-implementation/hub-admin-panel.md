# Hub 控制面板实现方案

## 目标

Hub 控制面板用于运维查看和少量安全操作,不是终端用户客户端。v1 覆盖:

- 总览、授权码、在线租约、出口节点、操作日志。
- Android `jp-android-01` 换 IP。
- Caddy 负责 HTTPS 和反代;应用内管理员登录负责控制台门禁。
- OpenAPI 合同、SQLite model、Go/TypeScript codegen。

v1 不做 token 创建/删除,也不自动改 WireGuard peer 配置。

## 运行形态

```text
Browser
  -> HTTPS jp-proxy.ruichao.dev
  -> Caddy reverse_proxy
  -> reverse_proxy 127.0.0.1:18100
  -> zhhub admin /admin/
```

`zhhub` 同一二进制启动两个 listener:

- `ZHHUB_LISTEN=0.0.0.0:18080`:客户端 bootstrap/rotate API。
- `ZHHUB_ADMIN_LISTEN=127.0.0.1:18100`:管理控制台和 `/admin/api/*`。

公网只开放 `80/443`;`18100` 不直接开放。

## 代码结构

`hub/admin` 顶层只保留对 Hub 主程序公开的 facade 和前端目录:

- `hub/admin/admin.go`:公开 `NewServer` 与 `Server`,内部委托给 `internal/api`。
- `hub/admin/config.go`:公开 `Config` 与环境变量读取。
- `hub/admin/password.go`:公开管理员密码 hash/verify helper。
- `hub/admin/generate.go`:统一维护 OpenAPI 与 sqlc 生成命令。
- `hub/admin/web/`:Svelte 前端源码、构建产物和 Go embed shim。

后端实现放在 Go `internal` 目录下,只允许 `hub/admin` 父级树内引用:

- `hub/admin/internal/api/`:HTTP admin API、登录/session/CSRF、资源 handler、聚合摘要。
- `hub/admin/internal/db/`:SQLite schema、sqlc queries、store 封装。
- `hub/admin/internal/db/generated/`:sqlc 生成代码,Go 包名为 `generated`。
- `hub/admin/internal/spec/`:OpenAPI 合同与 oapi-codegen 配置。
- `hub/admin/internal/spec/generated/`:OpenAPI 生成代码,Go 包名为 `generated`。
- `hub/admin/internal/security/`:Argon2id PHC 密码实现。

OpenAPI 和 sqlc 分别放在各自领域的 `generated` 目录下,避免两套生成代码的类型名互相冲突。

## 合同与模型

- OpenAPI: `hub/admin/internal/spec/openapi.yml`
- Go API types: `hub/admin/internal/spec/generated/openapi_types.go`
- SQLite schema: `hub/admin/internal/db/schema.sql`
- SQL queries: `hub/admin/internal/db/queries.sql`
- sqlc output: `hub/admin/internal/db/generated/`
- TypeScript API types: `hub/admin/web/src/lib/openapi.d.ts`

SQLite 默认路径:`/opt/zongheng/zhhub/admin.db`。表:

- `admin_users`
- `admin_sessions`
- `admin_login_attempts`
- `tokens_cache`
- `token_leases`
- `egress_nodes`
- `rotate_locks`
- `audit_events`

## 安全边界

- Caddy 负责公网 HTTPS 入口和反向代理。
- 应用登录使用 Argon2id PHC hash,由 `ZHHUB_ADMIN_PASSWORD_HASH` 注入。
- session cookie 为 HttpOnly + Secure + SameSite Strict。
- 非 GET admin API 要求 `X-CSRF-Token`。
- 登录失败按 username + source IP 限速。
- API 不返回 WireGuard 私钥;token 只返回脱敏值和稳定 hash id。
- 客户端 bootstrap/rotate 与管理员操作都会写入 `audit_events`。

## 前端

前端在 `hub/admin/web`,使用 Svelte + Vite + TypeScript + Tailwind。生产运行不依赖 Node:

```powershell
cd hub/admin/web
npm ci
npm run build:embed
```

`build:embed` 会生成 `hub/admin/web/dist`,由 Go `embed` 直接打进 `zhhub`。`web/` 是唯一前端目录;`dist/` 是构建产物,不是第二套前端源码。

UI 先按 `design/Hub 控制台.dc.html` 做原型对齐:顶栏、侧栏、总览卡片、出口健康、最近操作、表格页和换 IP 弹窗优先保持原型尺寸、间距、颜色和信息结构。总览不再展示静态单链路拓扑卡,避免多出口场景下误导。v1 未实现的新建/编辑/删除 token、断开会话、重连隧道、控制台 SSH、筛选等控件保留原型位置,但统一禁用。

前端不引入路由库,使用 hash 路由保持页签状态:`#/overview`、`#/tokens`、`#/egress`、`#/clients`、`#/logs`。切换左侧菜单会同步更新 URL hash,刷新页面或复制链接时能回到同一页。

## 生成命令

OpenAPI/SQL 变化后:

```powershell
go generate ./hub/admin
cd hub/admin/web
npm run generate:api
npm run build:embed
```

## 测试

```powershell
go test ./hub/admin ./hub/internal/auth ./hub
cd hub/admin/web
npm run check
npm run build:embed
```
