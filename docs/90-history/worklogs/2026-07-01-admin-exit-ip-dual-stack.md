# 2026-07-01 admin 出口 IP 双栈显示修复

## 背景

管理台 `/admin/#/egress` 的“当前出口 IP”只显示单个 `exit_ip` 字段。生产 Android 出口当前 IPv6 正常,但页面把完整 IPv6 塞进窄文本容器并触发省略号;同时 IPv4 没有展示。

生产只读核查:

- `curl --proxy http://10.66.0.1:18081 https://api6.ipify.org` 返回 `240b:` Rakuten IPv6。
- `curl --proxy http://10.66.0.1:18081 https://api.ipify.org` 返回 `133.106.x` 手机侧 IPv4。

## 改动

- `GET /admin/api/egress/{egress_id}/exit-ip` 改为同时探测:
  - `ZHHUB_ADMIN_EXIT_IPV6_CHECK_URL`,默认 `https://api6.ipify.org`
  - `ZHHUB_ADMIN_EXIT_IPV4_CHECK_URL`,默认 `https://api.ipify.org`
- 响应保留兼容字段 `exit_ip`,并新增可选字段 `ipv6`、`ipv4`。
- 若 v6/v4 均失败,继续使用旧 `ZHHUB_ADMIN_EXIT_IP_CHECK_URL=https://api64.ipify.org` 作为兼容 fallback。
- 前端出口 IP 卡片改为双行展示 IPv6/IPv4,完整 IPv6 允许换行,不再使用 `secrettext` 省略号。

## 验证

- `go generate ./hub/admin`
- `go test ./...`
- `npm run check`
- `npm run build:embed`
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`

## 生产

- 已部署到 Hub `36.50.84.68`。
- 旧版备份目录:`/root/zongheng-backups/20260701140319-admin-exit-ip-dual-stack`。
- 当前 `/opt/zongheng/zhhub/zhhub` SHA256:`757c52d6dc2b82b1d00276dec6e844b5ee0807829e23fc3724f809e6c15599d1`。
- 公网检查:`https://jp-proxy.ruichao.dev/` 返回 404,`/admin/` 返回 200,`/admin/api/health` 返回 `{"status":"ok"}`。
- 生产 reveal API 返回完整 IPv6 `240b:c010:4c3:e044:0:3f:e0ad:d601` 和 IPv4 `133.106.150.247`。
- Playwright + Chrome 验证 `/admin/#/egress`:点击出口 IP 眼睛后页面显示 `IPv6`/`IPv4` 两行,按钮变为“隐藏出口 IP”,IP value 元素 `clientWidth == scrollWidth`,未发生横向溢出。
- 当前前端资源:`assets/index-DZhFhVDT.js`,`assets/index-DFpwoEtH.css`。
