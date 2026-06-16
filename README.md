# 纵横 VPN

纵横 VPN 是一个 Hub + 日本住宅出口 + Windows 客户端的代理网络项目。

## 目录

> 注:`clients/` 是终端用户客户端,`egress/` 是出口节点(基础设施侧)。安卓相关都在 `egress/` 下,**不是**终端客户端。

按角色分顶层(client / hub / egress),Go 代码统一在根 module `zongheng-vpn` 下。

```text
clients/              客户端(终端用户侧)
  cli/                CLI 客户端（原 frontend/zhvpn）
  desktop-gui/        Windows 桌面 GUI 客户端（Tauri，调用 CLI sidecar）

hub/                  Hub 服务端（授权 API，原 backend/zhhub）

egress/               出口节点(基础设施侧，非终端客户端)
  reverse/            Android 反向 TCP/yamux 出口数据面（zhreverse，当前生产路径）
  proxy/              旧跨平台 Go 出口代理（基于 sing-box；Android 上仅保留回滚）
  android-status/     安卓出口监控 App（原 android/zhandroid-status）
  android-control/    安卓出口远程控制+自愈（Go SSH 服务绑隧道 IP + 看门狗）

shared/               客户端与出口共用的 Go 包
  config/  paths/  proxy/

sdk/                  面向开发者的语言 SDK（调用 CLI，不重写核心控制面）
  python/             Python SDK

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
$env:GOOS="linux"; $env:GOARCH="arm64"; go build -o dist/reverse/zhreverse-linux-arm64 ./egress/reverse
```

## 当前 MVP

```text
zhvpn.exe login <授权码>
-> Hub 校验 token
-> Hub 返回运行配置
-> zhvpn.exe start
-> 本地代理 127.0.0.1:7890
-> 日本住宅出口
```

`zhvpn.exe` 是本机唯一控制面。桌面 GUI 和后续 Python SDK 都通过 CLI 的机器接口（`--json` 等）完成登录、连接、状态、换 IP、断开；SDK 不直接调用 GUI，也不重新实现 WireGuard / sing-box / Hub bootstrap 逻辑。

### Android 出口当前 POC

2026-06-14 起,Android `zhreverse` 正在跑双网络 POC:

- Android -> Hub reverse tunnel:优先绑定 `wlan0`,走住宅 WiFi/家宽 IPv4;连续失败后 fallback 到 `rmnet1` 蜂窝隧道。
- Android -> 目标网站 TCP/DNS:绑定 `rmnet1`,继续走手机蜂窝 IPv6/IPv4。
- Hub 入口仍是 `10.66.0.1:18081`,Hub reverse TCP 仍是 `36.50.84.68:39093`。
- 目标网站不会看到家宽 IP;WiFi 只承载 Android <-> Hub 隧道字节。

## 客户端命令

```powershell
zhvpn.exe login <授权码>
zhvpn.exe start            # 本地代理端口默认 7890
zhvpn.exe start --port 7891  # 端口被占用时换端口（也可用环境变量 ZHVPN_LOCAL_PORT）
zhvpn.exe status
zhvpn.exe stop
zhvpn.exe rotate-ip      # Android 手机出口换公网 IP
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
