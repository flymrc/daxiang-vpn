# 2026-07-01 Hub 控制面板合同优先实现

本次按“OpenAPI -> DB model -> codegen -> 后端 -> 前端 -> 测试 -> 文档”实现 Hub 管理控制台 v1。

## 已完成

- 新增 `hub/admin/openapi.yml` 作为管理 API 合同。
- 新增 SQLite schema、sqlc queries 与生成代码。
- 新增 Hub admin 后端:
  - `127.0.0.1:18100` 管理 listener。
  - 应用内登录、Argon2id、session cookie、CSRF、登录限速。
  - `/admin/api/overview`、`tokens`、`leases`、`egress`、`events`、`rotate-ip`。
  - 客户端 bootstrap/rotate 与管理员操作写入 SQLite `audit_events`。
- 新增 Svelte + Vite + Tailwind 前端,构建产物 `hub/admin/web/dist` 由 Go embed 直接托管。
- 前端改为按 `design/Hub 控制台.dc.html` 做原型优先对齐:
  - 顶栏品牌/Hub 在线状态/主题切换/用户 pill 与原型保持一致。
  - 左侧导航、总览统计卡、数据通路、出口健康、最近操作、授权码、出口节点、在线客户端、操作日志、换 IP 弹窗使用原型同一套样式语言。
  - 功能未落地的 token 新建/编辑/删除、断开会话、重连隧道等入口暂时只提示后续功能。
- 修正 Svelte 5 入口挂载方式,由 `new App(...)` 改为 `mount(App, ...)`。
- 新增 `zhhub-admin-hash` 命令用于生成管理员密码 hash。
- 更新架构、部署、诊断和服务器访问文档。

## 未执行的生产变更

本次只改仓库代码和文档,未在线上执行以下操作:

- 安装/配置 Caddy。
- 迁移 Docker `librespeed` 端口。
- 修改 UFW。
- 重启或替换生产 `zhhub.service`。

这些操作已写入 `docs/20-operations/runbooks/hub-api-deploy.md`,上线前需单独确认。

## 验证

- `go generate ./hub/admin`
- `go test ./hub/admin ./hub/internal/auth ./hub`
- `npm run check` in `hub/admin/web`
- `npm run build:embed` in `hub/admin/web`
- Python Playwright + Vite dev server mock `/admin/api/*`,在 `1320x860` 视口截图检查总览首屏。
- Python Playwright 切换总览、授权码、出口节点、在线客户端、操作日志,确认无运行错误且 `1320px` 视口无横向溢出。
