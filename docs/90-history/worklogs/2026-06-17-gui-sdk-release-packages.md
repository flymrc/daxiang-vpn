# 2026-06-17 GUI / Python SDK 发包

## 背景

Android 换 IP 并发保护已合入,需要给同事分发新版桌面 GUI 和 Python SDK。

## 改动

- 桌面 GUI 版本号: `0.4.9` -> `0.4.10`。
- Python SDK 版本号: `0.1.0` -> `0.1.1`。
- `clients/desktop-gui/build.ps1` 构建 GUI sidecar 时注入 `zhvpn` 版本号,避免随包 CLI 显示 `dev`。
- `sdk/python/README.md` 安装/升级命令更新为 `zongheng_vpn-0.1.1-py3-none-win_amd64.whl`。

## 产物

- GUI 安装包:
  - `dist/for-colleagues/纵横 VPN_0.4.10_x64-setup.exe`
- Python SDK 包:
  - `dist/for-colleagues/zongheng-vpn-python-sdk-0.1.1-win_amd64.zip`
  - zip 内含 `README.md` 和 `zongheng_vpn-0.1.1-py3-none-win_amd64.whl`。

## 验证

- `.\clients\desktop-gui\build.ps1 -Target amd64`:通过,产出 NSIS x64 installer。
- GUI sidecar `zhvpn-x86_64-pc-windows-msvc.exe version --json`:返回 `0.4.10`。
- `.\sdk\python\build.ps1 -Target amd64 -Version 0.1.1 -Wheel`:通过,产出 SDK wheel。
- 临时 venv 安装 `zongheng_vpn-0.1.1-py3-none-win_amd64.whl`:通过,`Client().version().version` 返回 `0.1.1`。
- `go test ./hub/... ./clients/cli/...`:通过。
- `python -m unittest discover sdk/python/tests`:通过。
- `npm run check` in `clients/desktop-gui`:0 error,保留既有 `Cannot find type definition file for 'node'` warning。

## 备注

GUI 构建中的 `npm install` 仍提示当前依赖存在 7 个 audit vulnerabilities,本次未处理依赖升级。
