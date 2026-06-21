# zhvpn CLI

Windows / macOS 客户端 CLI MVP。Windows 发布物名为 `zhvpn.exe`，macOS 发布物名为 `zhvpn`。

## 构建

```powershell
# Windows
.\build.ps1
```

```bash
# macOS
./build-macos.sh
```

也可手动构建当前平台：

```bash
go build -tags with_gvisor -o ../../dist/macos-arm64/zhvpn .
```

## 使用

```powershell
# Windows
.\zhvpn.exe login ZH-DEV-TOKEN
.\zhvpn.exe start
.\zhvpn.exe status
.\zhvpn.exe rotate-ip
.\zhvpn.exe stop
```

```bash
# macOS
./zhvpn login ZH-DEV-TOKEN
./zhvpn start
./zhvpn status
./zhvpn rotate-ip
./zhvpn stop
```

机器接口：

```powershell
.\zhvpn.exe login ZH-DEV-TOKEN --json
.\zhvpn.exe start --json
.\zhvpn.exe status --json --no-ip-check
.\zhvpn.exe status --json
.\zhvpn.exe rotate-ip --json
.\zhvpn.exe stop --json
.\zhvpn.exe logout --json
.\zhvpn.exe version --json
```

`login` / `start` 会写入本地状态缓存，供 `status` 和桌面 GUI 高频轮询读取；`status` 不会重复请求 Hub bootstrap。`start` 仍会重新 bootstrap 获取最新运行配置。状态缓存不持久化 WireGuard 私钥。

## Android 出口换 IP

CLI 可以让当前手机卡出口重注册并尝试更换公网出口 IP：

```powershell
.\zhvpn.exe rotate-ip
.\zhvpn.exe rotate-ip --down-seconds 12 --wait-seconds 90
```

`--wait-seconds` 是最大等待时间,CLI 会轮询到出口恢复或超时。

## 出口节点说明

Android root 出口节点生产数据面在 `egress/reverse`，安卓状态 App 在 `egress/android-status`。

相关文档：`docs/30-implementation/android-egress-agent.md`
