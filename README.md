# 大象 VPN

大象 VPN 是一个 Hub + 日本住宅出口 + Windows 客户端的代理网络项目。

## 目录

> 注:`clients/` 是终端用户客户端,`egress/` 是出口节点(基础设施侧)。安卓相关都在 `egress/` 下,**不是**终端客户端。

按角色分顶层(client / hub / egress),Go 代码统一在根 module `daxiang-vpn` 下。

```text
clients/              客户端(终端用户侧)
  cli/                CLI 客户端（原 frontend/dxvpn）
  desktop-gui/        🅿️ 预留：mac/windows PC 单一跨平台 GUI 客户端

hub/                  Hub 服务端（授权 API，原 backend/dxhub）

egress/               出口节点(基础设施侧，非终端客户端)
  reverse/            Android 反向 TCP/yamux 出口数据面（dxreverse，当前生产路径）
  proxy/              旧跨平台 Go 出口代理（基于 sing-box；Android 上仅保留回滚）
  android-status/     安卓出口监控 App（原 android/dxandroid-status）
  android-control/    安卓出口远程控制+自愈（Go SSH 服务绑隧道 IP + 看门狗）

shared/               客户端与出口共用的 Go 包
  config/  paths/  proxy/

docs/
  README.md           文档总入口
  00-overview/        现状、MVP、客户端指南
  10-architecture/    架构设计、出口选型
  20-operations/      运维、部署、示例配置
  30-implementation/  具体实现方案
  40-security/        安全审查和安全 TODO
  90-history/         工作记录、阶段复盘

dist/
  windows-amd64/      Windows x64 客户端发布包
  windows-arm64/      Windows ARM64 客户端发布包
```

## 构建

```powershell
# Windows 客户端
clients/cli/build.ps1
# Hub 服务端
go build -o dist/hub ./hub
# 安卓出口代理（arm64）
$env:GOOS="linux"; $env:GOARCH="arm64"; go build -o dist/reverse/dxreverse-linux-arm64 ./egress/reverse
```

## 当前 MVP

```text
dxvpn.exe login <授权码>
-> Hub 校验 token
-> Hub 返回运行配置
-> dxvpn.exe start
-> 本地代理 127.0.0.1:7890
-> 日本住宅出口
```

## 客户端命令

```powershell
dxvpn.exe login <授权码>
dxvpn.exe start            # 本地代理端口默认 7890
dxvpn.exe start --port 7891  # 端口被占用时换端口（也可用环境变量 DXVPN_LOCAL_PORT）
dxvpn.exe status
dxvpn.exe stop
dxvpn.exe rotate-ip      # Android 手机出口换公网 IP
```

## 关键文档

- [文档总入口](docs/README.md)
- [当前 MVP 计划](docs/00-overview/mvp-plan.md)
- [总体架构](docs/10-architecture/system-architecture.md)
- [出口方案选型](docs/10-architecture/egress-strategy.md)
- [运维诊断命令手册](docs/20-operations/runbooks/diagnostics.md)
- [Android 出口远程控制操作手册](docs/20-operations/runbooks/android-remote-control.md)
- [服务器访问与当前状态](docs/20-operations/runbooks/server-access.md)
- [安全与抗封改进 TODO](docs/40-security/security-todo.md)
- [Hub 安全审查报告 2026-06-04](docs/40-security/security-audit-2026-06-04.md)
