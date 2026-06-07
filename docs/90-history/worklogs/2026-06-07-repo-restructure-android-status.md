# 2026-06-07 仓库重排与 Android 出口状态探针

## 背景

今天把仓库按角色重排，目标是让终端客户端、Hub、出口节点和共用 Go 包的边界更清楚，避免 Android 出口继续混在客户端目录里。

## 仓库结构调整

新的顶层角色目录：

```text
clients/              终端用户客户端
  cli/                Windows CLI 客户端
  desktop-gui/        mac/windows 跨平台 GUI 客户端预留

hub/                  Hub 授权 API

egress/               出口节点
  proxy/              跨平台 Go 出口代理，Android 当前使用 dxandroid-egress
  android-status/     Android 出口状态 App

shared/               客户端和出口共用 Go 包
```

Go module 移到仓库根目录，module 名为 `daxiang-vpn`。

## Android 出口保活探针

今天在 Android 出口侧补了保活探针：

- Android 出口端按 25 秒周期向 Hub peer 发 WireGuard keepalive。
- Hub 侧可以据此判断 Android 出口是否仍在线。
- 该机制用于后续出口健康状态、可用性判断和自恢复策略。

代码对应点：

- `egress/proxy/internal/egressproxy/singbox.go` 在 embedded/sing-box WireGuard 模式下渲染 `persistent_keepalive_interval: 25`。
- 也就是 Android 出口到 Hub peer 的 WireGuard keepalive 周期为 25 秒。
- 当前推荐的 `wireguard.mode: external` 不由 `dxandroid-egress` 渲染 WireGuard endpoint，保活由 WireGuard App/系统隧道配置负责。

`egress/android-status` 仍是手机本机状态界面和前台通知，不等同于向 Hub 报活的保活探针。

## 文档同步

同步修正了重排后的路径：

- README 顶层目录说明和构建命令。
- Hub 部署 runbook 的构建入口。
- Hub / CLI / Android egress 实现文档里的旧 `backend/`、`frontend/`、`cmd/` 路径。
- Android 出口保活探针今天已补充记录：它是出口端 25 秒一次的 WireGuard keepalive。

## 验证

本地验证：

```powershell
go test ./...
go test -tags with_gvisor ./...
go build ./clients/cli
go build ./hub
go build ./egress/proxy
go build -tags with_gvisor ./clients/cli
go build -tags with_gvisor ./egress/proxy
```

以上 Go 检查均通过。
