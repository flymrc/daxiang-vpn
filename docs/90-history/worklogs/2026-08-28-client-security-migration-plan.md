# 2026-08-28 客户端安全迁移计划与观测链

## 背景

现有生产遥测可以统计客户端活动，但不能按安装实例证明客户端版本、是否使用客户端本地 WireGuard 密钥，以及请求来自哪个可信入口。因此不能据此删除 legacy 私钥或关闭公网兼容 listener。

## 本次变更

- 新增长期执行清单：[客户端 HTTPS 与本地 WireGuard 密钥迁移计划](../../40-security/client-security-migration-plan.md)，并在 [文档入口](../../README.md) 加入链接。
- 新增 CLI `buildinfo` 单一事实源。bootstrap 和 rotate 请求上报非敏感的 product/version/protocol；GUI、Python SDK 和独立 CLI 由各自发布构建固化 surface/version。
- 发布脚本拒绝 `dev` 或缺失版本；桌面构建同时检查三份现有产品元数据没有漂移。
- Hub 新增物理分离的 `compat` 与 loopback-only `trusted_proxy` listener。入口身份由 handler 注册固定，不读取客户端 header 推断。
- 成功 bootstrap 根据入口、公钥模式、实际私钥响应和客户端合同派生 `secure_bootstrap`、`legacy` 或 `unknown`，审计内容不保存 token 或密钥。
- admin SQLite 新增每 token 一行的迁移观测投影。原始 audit 与投影同事务写入，乱序事件不倒退最近状态。
- 新增登录后只读接口 `GET /admin/api/migration/readiness`，列出当前有效 token 的非敏感版本和分类。当前模式固定为 `observation_only`，campaign 和端到端证据未建立前始终 `ready=false`。
- 更新 Hub README 和部署 runbook，明确先部署双 listener、再切 Caddy upstream 的顺序。

## 边界

- 本次只修改本地代码、离线测试和公开操作文档；未部署 Hub，未 reload Caddy，未修改防火墙、token、WireGuard、Android 出口或任何生产状态。
- 未实现 campaign 成员表、批准版本 allowlist、端到端证据、静默窗口或强制拒绝；不能据此清理私钥或关闭 `18080`。
- 真实用户映射和生产证据不进入公开仓库；报告只使用稳定 token hash ID。
- 当前结论保持 `NO-GO`。

## 验证

- `go test ./clients/cli/internal/bootstrap ./clients/cli/internal/app`
- `go test ./hub ./hub/internal/auth`
- `go test ./hub/admin/...`
- `go test ./...`
- `go vet ./hub/... ./clients/cli/...`
- Python SDK `unittest`：5 项通过。
- Hub admin web `npm run check`：0 error / 0 warning。
- 编译期注入的 `version --json` 返回预期 product/version/protocol。
- Windows CLI、macOS CLI 和 Python SDK 发布脚本的 `dev` 快速失败检查通过；PowerShell AST 与 Bash 语法检查通过。
- `go generate ./hub/admin` 与 `npm run generate:api` 完成。
- 生产只读前置复核确认现有 Hub/Caddy/Android 出口服务健康、候选 loopback 端口未占用、Caddy 仍处于切换前状态；原始私有证据未写入仓库。
- `git diff --check` 通过；最终差异和敏感信息检查未发现生产秘密或本机私有路径进入源码/文档。
