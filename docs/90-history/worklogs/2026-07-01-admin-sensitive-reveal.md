# 2026-07-01 Hub admin 敏感值 reveal

本次按 admin 管理界面使用习惯,为默认脱敏的授权码和出口 IP 增加眼睛开关。

## 实现

- `/admin/api/tokens` 仍只返回 `masked_token` 和稳定 hash `id`,不在列表响应中泄露完整 token。
- 新增 `GET /admin/api/tokens/{token_id}/secret`:登录管理员按需 reveal 单个完整 token。
- 新增 `GET /admin/api/egress/{egress_id}/exit-ip`:登录管理员按需通过该出口的 `proxy_addr` 探测公网出口 IP。
- 新增 `ZHHUB_ADMIN_EXIT_IP_CHECK_URL` 和 `ZHHUB_ADMIN_EXIT_IP_CHECK_TIMEOUT_SECONDS`,默认分别为 `https://api.ipify.org` 和 8 秒。
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
