# 2026-06-09 工作记录：Android 出口健康检查与 CLI 构建

## 背景

用户要求确认 Android 手机出口是否正常，并构建支持 Android 出口换 IP 的新版 CLI。

## 健康检查

本机默认 `ssh root@36.50.84.68` 无可用非交互凭据，改用本机已有 Hub 密钥 `~/.ssh/daxiang_server` 做只读检查。

检查结果：

- Hub 经 Android reverse proxy `127.0.0.1:18081` 可正常出公网。
- 当前出口公网 IP：`133.106.33.238`。
- Hub 到 Android 控制面路由存在：`10.66.0.101 dev wg0 scope link mtu 1120`。
- Hub TCPMSS 规则存在：`--set-mss 1080`。
- Android peer `10.66.0.101/32` 存在于 `wg show wg0 allowed-ips`。
- Android peer 最新握手时间新鲜：Hub 当前 `date +%s=1780966084`，Android peer handshake `1780965956`，约 `128s`。
- Android 控制面 `10.66.0.101:2022` 可达，返回 `SSH-2.0-Go`。

判断：Android 出口数据面、WireGuard 控制面路由、peer 握手和 SSH 控制面均正常。

## CLI 构建

当前 CLI 已内置 `rotate-ip` 命令：

```powershell
dxvpn.exe rotate-ip
dxvpn.exe rotate-ip --down-seconds 12 --wait-seconds 45
dxvpn.exe rotate-ip --phone 10.66.0.101 --port 2022 --key "$HOME\.ssh\dxandroid_control"
```

执行构建：

```powershell
.\clients\cli\build.ps1
```

产物：

- `dist/windows-amd64/dxvpn.exe`，约 `17.1 MB`。
- `dist/windows-arm64/dxvpn.exe`，约 `15.5 MB`。

## 验证

- `go test ./...` 通过。
- `clients/cli/build.ps1` 通过。
- `git status --short` 在构建前后均无 tracked 改动；`dist/` 产物为本地构建输出。
