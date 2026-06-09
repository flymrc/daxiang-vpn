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
.\dxvpn.exe rotate-ip --down-seconds 12 --wait-seconds 90
```

默认由 Hub 代为触发 Android 控制面,客户端不需要 Android SSH 私钥。`--wait-seconds` 是最大等待时间,CLI 会轮询到出口恢复或超时。管理员需要临时直连控制面排障时可使用 `--direct`:

```powershell
.\dxvpn.exe rotate-ip --direct --phone 10.66.0.101 --port 2022 --key "$HOME\.ssh\dxandroid_control"
```

普通 `dxvpn.exe start` 使用用户态 WireGuard,不会给 Windows 系统添加 `10.66.0.0/24` 路由。管理员直连时若本机不能直连 `10.66.0.101:2022`,使用 Hub 跳板:

```powershell
.\dxvpn.exe rotate-ip --direct --jump root@36.50.84.68 --key "$HOME\.ssh\dxandroid_control"
```

`--direct` 必须使用已写入 Android `/data/adb/dxandroid/.ssh/authorized_keys` 的控制面私钥。普通客户机不要使用 `--direct`。

## 出口节点说明

Android root 出口节点守护进程已经拆到 `egress/proxy`，安卓状态 App 在 `egress/android-status`。

相关文档：`docs/30-implementation/android-egress-agent.md`
