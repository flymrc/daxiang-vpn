# 2026-07-02 Hub admin 授权码表头排序

本次调整管理控制台 `/admin/#/tokens` 的授权码表格:

- `/admin/api/tokens` 的 `TokenSummary` 增加 `last_active_at`,使用当前活跃 token lease 的 `seen_at` 作为最近活跃时间,保持列表默认不返回 token 明文。
- 授权码页默认按最近活跃倒序排序,当前在线/刚活跃的 token 会排在前面。
- 授权码、客户端、状态、出口、WG 地址、到期、最近活跃表头支持点击切换排序方向。
- 排序在分页前执行,切换表头排序时重置到第 1 页。

## 验证

- `npm run check` in `hub/admin/web`:通过。
- `go test ./hub/admin ./hub/internal/auth ./hub`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-esux67Q-.js` 和 `index-Qm6uIKdS.css`。
- `go test ./...`:通过。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过,本地 SHA256:`54fe410d8b29f38123e9371fc1e8e0d7f8709d09e712d417c1190a6e2c032343`。

## 生产部署

- 上传到 Hub `/tmp/zhhub-admin-token-table-sorting`,远端 SHA256 校验为 `54fe410d8b29f38123e9371fc1e8e0d7f8709d09e712d417c1190a6e2c032343`。
- 备份旧二进制与 systemd unit 到 `/root/zongheng-backups/20260702004801-admin-token-table-sorting`。
- 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。
- 部署后 `/opt/zongheng/zhhub/zhhub` SHA256 为 `54fe410d8b29f38123e9371fc1e8e0d7f8709d09e712d417c1190a6e2c032343`。

## 部署后核查

- `zhhub.service` active。
- `http://127.0.0.1:18080/healthz` 返回 ok。
- `http://127.0.0.1:18100/admin/api/health` 返回 ok。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/admin/` 返回 200,页面引用新资源 `index-esux67Q-.js` 和 `index-Qm6uIKdS.css`。
- `https://jp-proxy.ruichao.dev/admin/assets/index-esux67Q-.js` 返回 200,包内包含 `last_active_at`。
- `https://jp-proxy.ruichao.dev/` 仍返回 404。
- 未登录访问 `https://jp-proxy.ruichao.dev/admin/api/tokens/not-real/secret` 返回 401。
- `HEAD https://jp-proxy.ruichao.dev/api/client/bootstrap` 返回预期 405。
- `journalctl -u zhhub.service --since '2026-07-02 00:48:01'` 未发现 `panic|fatal|failed|error`。
