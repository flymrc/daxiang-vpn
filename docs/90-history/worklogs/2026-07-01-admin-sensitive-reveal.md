# 2026-07-01 Hub admin 敏感值 reveal

本次按 admin 管理界面使用习惯,为默认脱敏的授权码和出口 IP 增加眼睛开关。

## 实现

- `/admin/api/tokens` 仍只返回 `masked_token` 和稳定 hash `id`,不在列表响应中泄露完整 token。
- 新增 `GET /admin/api/tokens/{token_id}/secret`:登录管理员按需 reveal 单个完整 token。
- 新增 `GET /admin/api/egress/{egress_id}/exit-ip`:登录管理员按需通过该出口的 `proxy_addr` 探测公网出口 IP。
- 新增 `ZHHUB_ADMIN_EXIT_IP_CHECK_URL` 和 `ZHHUB_ADMIN_EXIT_IP_CHECK_TIMEOUT_SECONDS`,初始默认分别为 `https://api.ipify.org` 和 8 秒。
- 前端授权码每行增加眼睛按钮;出口节点页“当前出口 IP”增加眼睛按钮。再次点击会重新隐藏。
- reveal 操作写入 `audit_events`,只记录 token hash id / egress id,不记录完整 token。

## 验证

- `go generate ./hub/admin`:通过。
- `npm run check` in `hub/admin/web`:通过。
- `go test ./...`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-DDykRb3F.js` 和 `index-CqPG-6b3.css`。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过。

## 生产部署

- 本机 Linux amd64 `zhhub` SHA256:`aa9216e9e467ce8f39866dc96c732de332622ea922ca9b77f49a8ab2843ea286`。
- 上传到 Hub `/tmp/zhhub-admin-sensitive-reveal`,远端 SHA256 校验通过。
- 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701132111-admin-sensitive-reveal`。
- 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。

## 部署后核查

- `zhhub.service` active。
- `http://127.0.0.1:18080/healthz` 返回 ok。
- `http://127.0.0.1:18100/admin/api/health` 返回 ok。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/admin/` 返回 200,页面引用新资源 `index-DDykRb3F.js`。
- `https://jp-proxy.ruichao.dev/` 仍返回 404。
- 未登录访问 `/admin/api/tokens/not-real/secret` 返回 401。
- 未登录访问 `/admin/api/egress/jp-android-01/exit-ip` 返回 401。
- 线上 JS 已确认包含“显示授权码”和“显示出口 IP”文案。

## 同日追加：reveal UI 反馈修复

用户反馈出口 IP 显示“未采集”,点击眼睛看起来没有响应,且在线客户端表的授权码没有眼睛入口。

修复:

- 在线客户端 lease summary 增加 `token_id`,在线客户端表的授权码列也可按需 reveal。
- 授权码/出口 IP 点击眼睛后立即显示“读取中...”或“探测中...”,不再等其它刷新触发视觉变化。
- 出口 IP 空状态文案从“未采集”改为“点击眼睛探测”,明确当前需要按需探测。
- 出口 IP 探测默认 URL 改为 `https://api64.ipify.org`,匹配 Android/Rakuten 当前更稳定的 IPv6 主路径。

验证:

- `go generate ./hub/admin`:通过。
- `npm run check` in `hub/admin/web`:通过。
- `go test ./...`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-csoHTLlm.js`。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过。
- Hub 通过 Android 代理访问 `https://api64.ipify.org` 成功,返回 `240b:` IPv6。
- 登录态验证:token 列表仍为脱敏;单项 token reveal 返回真实值;出口 IP reveal 返回 IPv6。

生产部署:

- 本机 Linux amd64 `zhhub` SHA256:`debfb1c17ddfab8b1fda8b1a2b470bf5507d007000e57f914ba1b3acb31d3168`。
- 上传到 Hub `/tmp/zhhub-admin-reveal-ui-fix`,远端 SHA256 校验通过。
- 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701133234-admin-reveal-ui-fix`。
- 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。

部署后核查:

- `zhhub.service` active。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/admin/` 返回 200,页面引用新资源 `index-csoHTLlm.js`。
- `https://jp-proxy.ruichao.dev/` 仍返回 404。
- 未登录访问 reveal API 仍返回 401。

## 同日追加：Svelte reveal 依赖追踪修复

继续排查用户反馈“点击眼睛没有反应,点右上角刷新后才显示”。用 Chrome/Playwright 复现确认:

- 点击授权码眼睛会发出 `GET /admin/api/tokens/{id}/secret`,响应 200。
- 点击出口 IP 眼睛会发出 `GET /admin/api/egress/jp-android-01/exit-ip`,响应 200。
- 但 DOM 文本没有随 `tokenSecrets` / `exitIPSecrets` 更新。

根因是模板里写成 `tokenValue(row.id, row.masked_token)` 和 `exitIP(node)`,真实依赖藏在函数内部。Svelte 编译器只追踪模板表达式里显式出现的变量,因此 `tokenSecrets` 或 `exitIPSecrets` 改变时不会重算这段文本;右上角刷新改了 `tokens/egress` 后才顺带重算。

修复:

- 将依赖显式传入模板表达式:
  - `tokenValue(row.id, row.masked_token, tokenSecrets[row.id], revealingTokenID)`
  - `exitIP(node, exitIPSecrets[node.id], revealingExitIPID)`
- 保留“读取中...”和“探测中...”即时反馈。

验证:

- `npm run check` in `hub/admin/web`:通过。
- `go test ./...`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-BE97mfWm.js`。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过。
- 生产部署 SHA256:`8cb90800e2c64f9731e51e95042ffc0af4324358a5782e449d1859b1245d7187`。
- 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701134256-admin-reveal-reactivity-fix`。
- Chrome/Playwright 登录生产页面后验证:
  - 授权码点击眼睛后立即有反馈,最终从脱敏值变为完整值。
  - 出口 IP 点击眼睛后立即有反馈,最终显示 IPv6。
  - 两个 reveal 请求均返回 200。
