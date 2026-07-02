# 2026-07-02 admin 出口卡片布局修复

## 背景

`/admin/#/egress` 在出口 IP reveal 后出现卡片断裂:上一版让“当前出口 IP”卡片横跨 2 列,但整组指标仍按 4 列自动流式布局,导致最后 `management_addr` 单独掉到第三行,右侧留下大块灰底。

## 改动

- `hub/admin/web/src/App.svelte`:移除 `.ipcard { grid-column: span 2; }`,让 8 个指标重新按 4x2 铺满。
- 保留 IPv6/IPv4 双行展示与 `overflow-wrap:anywhere`,完整 IPv6 仍可显示;窄宽度时允许换行,不再省略。
- 重建前端 embed 资源:
  - `assets/index-BWT1KbMR.js`
  - `assets/index-uug4Z1vw.css`

## 验证

- `npm run check`
- `npm run build:embed`
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`
- 本地 Playwright + Chrome:注入长 IPv6/IPv4 后 `.kv.flat` 两行布局 `counts=[4,4]`,IP value `clientWidth == scrollWidth`。

## 生产

- 已部署到 Hub `36.50.84.68`。
- 旧版备份目录:`/root/zongheng-backups/20260702003327-admin-egress-card-layout`。
- 当前 `/opt/zongheng/zhhub/zhhub` SHA256:`53c1aba5fe06ed36c4860abf6bd7abac26c1ffb5774e8c892693cc2c8d8f3818`。
- 公网检查:`https://jp-proxy.ruichao.dev/` 返回 404,`/admin/` 返回 200,`/admin/api/health` 返回 `{"status":"ok"}`。
- 生产 Playwright + Chrome:点击出口 IP 眼睛后 `.kv.flat` 两行布局 `counts=[4,4]`,IP value `clientWidth == scrollWidth`,不再出现第三行孤块。
