# 2026-07-01 Hub admin 换 IP 锁交互

本次修复管理控制台出口节点页的换 IP 交互:当出口节点 `rotate_lock_until` 仍在未来时,点击「换 IP」不再弹出确认弹窗,而是直接显示 toast 提示“换 IP 正在进行中”和剩余等待时间,并刷新一次状态。锁空闲时仍保留原确认弹窗。

本次没有触发真实 Android 换 IP,也没有修改 token、WireGuard peer、Caddy 或 Android 侧配置。

## 验证

- `npm run check` in `hub/admin/web`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-BqZ5qsW6.js`。
- `go test ./...`:通过。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过。

## 生产部署

- 本机 Linux amd64 `zhhub` SHA256:`6e33908ed10e54326e1f23cb958b524af3e8bee7e83e891ba661582fb61da1d3`。
- 上传到 Hub `/tmp/zhhub-admin-rotate-lock-ui`,远端 SHA256 校验通过。
- 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701131306-admin-rotate-lock-ui`。
- 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。

## 部署后核查

- `zhhub.service` active。
- `http://127.0.0.1:18080/healthz` 返回 ok。
- `http://127.0.0.1:18100/admin/api/health` 返回 ok。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/admin/` 返回 200,页面引用新资源 `index-BqZ5qsW6.js`。
- `https://jp-proxy.ruichao.dev/` 仍返回 404。
- 线上 JS 已确认包含 `换 IP 正在进行中` 提示文案。
