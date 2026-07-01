# Hub 控制面板实现方案

## 目标

Hub 控制面板用于运维查看和少量安全操作,不是终端用户客户端。v1 覆盖:

- 总览、授权码、在线租约、出口节点、操作日志。
- Android `jp-android-01` 换 IP。
- 双层门禁:Caddy `basic_auth` + 应用内管理员登录。
- OpenAPI 合同、SQLite model、Go/TypeScript codegen。

v1 不做 token 创建/删除,也不自动改 WireGuard peer 配置。

## 运行形态

```text
Browser
  -> HTTPS jp-proxy.ruichao.dev
  -> Caddy basic_auth
  -> reverse_proxy 127.0.0.1:18100
  -> zhhub admin /admin/
```

`zhhub` 同一二进制启动两个 listener:

- `ZHHUB_LISTEN=0.0.0.0:18080`:客户端 bootstrap/rotate API。
- `ZHHUB_ADMIN_LISTEN=127.0.0.1:18100`:管理控制台和 `/admin/api/*`。

公网只开放 `80/443`;`18100` 不直接开放。

## 合同与模型

- OpenAPI: `hub/admin/openapi.yml`
- Go API types: `hub/admin/openapi_types.gen.go`
- SQLite schema: `hub/admin/schema.sql`
- SQL queries: `hub/admin/queries.sql`
- sqlc output: `hub/admin/storage/`
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

- Caddy `basic_auth` 是公网第一层门禁。
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
