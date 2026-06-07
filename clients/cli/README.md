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
.\dxvpn.exe stop
```

## 出口节点说明

Android root 出口节点守护进程已经拆到 `egress/proxy`，安卓状态 App 在 `egress/android-status`。

相关文档：`docs/30-implementation/android-egress-agent.md`
