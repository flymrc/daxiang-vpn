# zhvpn.exe

Windows 客户端 CLI MVP。

## 构建

```powershell
go build -tags with_gvisor -o ..\..\dist\zhvpn.exe .
```

## 使用

```powershell
.\zhvpn.exe login ZH-DEV-TOKEN
.\zhvpn.exe start
.\zhvpn.exe status
.\zhvpn.exe rotate-ip
.\zhvpn.exe stop
```

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
