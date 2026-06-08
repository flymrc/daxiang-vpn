# dxvpn.exe

Windows 客户端 CLI MVP。

## 构建

```powershell
go build -tags with_gvisor -o ..\..\dist\dxvpn.exe .
```

## 使用

```powershell
.\dxvpn.exe login DX-DEV-TOKEN
.\dxvpn.exe start
.\dxvpn.exe status
.\dxvpn.exe rotate-ip
.\dxvpn.exe stop
```

## Android 出口换 IP

CLI 可以直接触发 Android 控制面上的 `rotate-ip.sh`，让手机网络飞行模式重注册并尝试更换公网出口 IP：

```powershell
.\dxvpn.exe rotate-ip
.\dxvpn.exe rotate-ip --down-seconds 12 --wait-seconds 45
```

默认读取当前配置的 `egress.management_addr`，端口默认 `2022`，私钥默认 `~/.ssh/dxandroid_control`。需要临时覆盖时：

```powershell
.\dxvpn.exe rotate-ip --phone 10.66.0.101 --port 2022 --key "$HOME\.ssh\dxandroid_control"
```

## 出口节点说明

Android root 出口节点守护进程已经拆到 `egress/proxy`，安卓状态 App 在 `egress/android-status`。

相关文档：`docs/30-implementation/android-egress-agent.md`
