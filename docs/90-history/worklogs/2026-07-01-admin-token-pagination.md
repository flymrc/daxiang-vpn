# 2026-07-01 Hub admin 授权码分页与眼睛对齐

本次修复管理控制台授权码页两个前端体验问题:

- 授权码脱敏值和 reveal 后完整值长度不同,导致眼睛按钮不在同一纵向列。
- `/admin/#/tokens` 一次展示全部授权码,列表过长。

## 实现

- 授权码页改为 10 条/页分页,底部显示当前范围、总数、上一页/下一页和页码。
- 授权码列改为固定文本列 + 固定眼睛图标列,完整 token 和脱敏 token 的眼睛按钮保持同一列。
- 在线客户端表沿用同一 `tokenline` 布局,避免后续同类错位。

## 验证

- `npm run check` in `hub/admin/web`:通过。
- `go test ./...`:通过。
- `npm run build:embed` in `hub/admin/web`:通过,生成 `index-shlmomTR.js` 和 `index-D7r-ShBJ.css`。
- `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`:通过。

## 生产部署

- 本机 Linux amd64 `zhhub` SHA256:`482bc633fad2619793018b4e35939abe806dc52b11b46fecb801b544aa6b35d4`。
- 上传到 Hub `/tmp/zhhub-admin-token-pagination`,远端 SHA256 校验通过。
- 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701135152-admin-token-pagination`。
- 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。

## 部署后核查

- `zhhub.service` active。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/admin/` 返回 200,页面引用新资源 `index-shlmomTR.js`。
- `https://jp-proxy.ruichao.dev/` 仍返回 404。
- Chrome/Playwright 登录生产 `/admin/#/tokens` 验证:
  - 第 1 页显示 10 行,分页文案为 `显示 1-10 / 21`。
  - 点击第 2 页后显示 10 行,分页文案为 `显示 11-20 / 21`。
  - 页内容发生变化。
  - 前 10 行眼睛按钮左坐标均为 `440`,确认图标列对齐。
