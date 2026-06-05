# dxvpn.exe

Windows 客户端 CLI MVP。

## 构建

```powershell
go build -o ..\..\dist\dxvpn.exe .\cmd\dxvpn
```

## 使用

```powershell
.\dxvpn.exe login DX-DEV-TOKEN
.\dxvpn.exe start
.\dxvpn.exe status
.\dxvpn.exe stop
```

## Android 出口节点预备版

仓库里还新增了一个 Android root 出口节点守护进程入口：

```text
cmd/dxandroid-egress
```

它用于把 rooted 安卓手机作为 `egress` 节点接入 Hub，而不是给中国侧桌面用户提供本地代理界面。

相关文档：

- `docs/implementation/ANDROID_EGRESS_AGENT.md`
