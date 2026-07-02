# 2026-07-02 P0 新客户端发放包

## 背景

生产 Hub 已支持客户端本地生成 WireGuard 私钥、bootstrap 只上报公钥;新协议响应不再返回 `wireguard.private_key`。本次按 P0 收尾需要重建并发放新版客户端。

## 产物

发放目录:`dist/for-colleagues/`

- `纵横 VPN_0.4.11_x64-setup.exe`
- `zhvpn-cli-0.4.11-windows-amd64.zip`
- `zhvpn-cli-0.4.11-windows-arm64.zip`
- `zongheng-vpn-python-sdk-0.1.2-win_amd64.zip`
- `SHA256SUMS.txt`

SHA256:

```text
5d885d9ce77fb6ffc51ae7e61adac6c9a1286303ca46ed6210e55f131b52c6c6  zhvpn-cli-0.4.11-windows-amd64.zip
5a815c407dbf59f468cd567b934a580ca14a277302458b099c44a09445d76c53  zhvpn-cli-0.4.11-windows-arm64.zip
47b8273e517502caf817524c3f3f9f711d4252870f944ef9ba84762d136eb211  zongheng-vpn-python-sdk-0.1.2-win_amd64.zip
a43990ff2d63c9775a92d65df72b6b949913812c3cf8aa3d6454992093903777  纵横 VPN_0.4.11_x64-setup.exe
```

## 验证

- `go test ./clients/cli/...`:通过。
- `npm run check` in `clients/desktop-gui`:0 error,保留既有 `Cannot find type definition file for 'node'` warning。
- `python -m unittest discover -s sdk/python/tests`:通过。
- `.\clients\desktop-gui\build.ps1 -Target amd64`:通过,产出 NSIS x64 installer。
- `.\sdk\python\build.ps1 -Target amd64 -Version 0.1.2 -Wheel`:通过。
- standalone x64 CLI zip 解压后 `zhvpn.exe version --json` 返回 `0.4.11`。
- GUI sidecar `zhvpn-x86_64-pc-windows-msvc.exe version --json` 返回 `0.4.11`。
- SDK bundled CLI `sdk/python/src/zongheng_vpn/bin/zhvpn.exe version --json` 返回 `0.1.2`。
- 临时 venv 安装 `zongheng_vpn-0.1.2-py3-none-win_amd64.whl`:通过,`Client().version().version` 返回 `0.1.2`。

## 备注

- standalone CLI 发放包使用 `-X zongheng-vpn/clients/cli/internal/app.Version=0.4.11` 注入版本号,避免默认显示 `dev`。
- GUI 构建中的 `npm install` 提示 3 个 low severity audit vulnerabilities,本次未处理依赖升级。
- 新客户端发放后仍需真实用户侧验证 login/start 走 HTTPS、公钥上报和响应无私钥;确认稳定后清理生产 token legacy `wireguard.private_key`,再关闭公网 `18080/tcp`。
