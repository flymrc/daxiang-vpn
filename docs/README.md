# 纵横 VPN 文档入口

这份目录按“先理解现状，再看架构，再做运维”的顺序整理。

## 先看这几份

1. [当前 MVP 计划](00-overview/mvp-plan.md)
2. [客户端使用指南](00-overview/client-guide.md)
3. [系统架构](10-architecture/system-architecture.md)
4. [出口方案选型](10-architecture/egress-strategy.md)
5. [2026-06-06 Android 出口上线记录](90-history/worklogs/2026-06-06-android-egress.md)

## 当前状态速览

```text
Hub: 36.50.84.68 / 10.66.0.1
  |
  +-- mac-mini       10.66.0.100:1080 -> deprecated / 历史 Mac 出口
  |
  +-- jp-android-01  zhreverse -> Hub/WG 10.66.0.1:18081 -> 手机运营商出口
```

2026-06-15 起,Mac mini `10.66.0.100:1080` 出口路线标记为弃用。它可作为历史诊断/管理内网对象保留,但不再作为新客户端、自动调度或专项爬虫验证出口。Android 出口数据面已迁到 `zhreverse` 反向 TCP/yamux,客户端经 WireGuard 访问 Hub 侧 `10.66.0.1:18081`;旧 `10.66.0.101:1080` 路径已从生产入口拆除,只在历史记录中保留。

Hub 只承担 WireGuard 入口、授权与反向出口中转职责,不作为备用公网出口。若手机 IPv4/Rakuten IPv4 路径异常,客户端应显示 IPv4 不可用或异常,不能把 Hub VPS `36.50.84.68` 当成兜底出口。

Android 控制面仍保留 WireGuard App:`jp-android-01` 使用 `10.66.0.101`,Hub 可通过 `10.66.0.101:2022` 登录 `zhandroid-control`,TCP ADB `10.66.0.101:5555` 仅允许 WireGuard 内网来源。

Hub 管理控制台 v1 作为 `zhhub` 第二个 listener 运行,本机监听 `127.0.0.1:18100`;公网入口由 Caddy 提供 `https://jp-proxy.ruichao.dev/admin/`,已替代原 `librespeed` 测速页。客户端授权 API 正在做 P0 TLS 迁移,生产 Caddy 已提供 `https://jp-proxy.ruichao.dev/api/client/*`;生产 Hub 已支持客户端本地生成 WireGuard 私钥、bootstrap 只上报公钥。公网 `18080/tcp` 和 legacy 私钥响应只应作为老客户端迁移期兼容路径。

## 目录说明

```text
00-overview/          给人看的现状、MVP、客户端指南
10-architecture/      架构设计、出口选型
20-operations/        运维手册、服务器访问、部署、示例配置
30-implementation/    具体功能实现方案
40-security/          安全审查和安全 TODO
90-history/           工作记录、阶段性复盘
```

## 00 Overview

- [当前 MVP 计划](00-overview/mvp-plan.md)
- [客户端使用指南](00-overview/client-guide.md)

## 10 Architecture

- [系统架构](10-architecture/system-architecture.md)
- [出口方案选型](10-architecture/egress-strategy.md)

## 20 Operations

- [服务器访问与当前基础设施状态](20-operations/runbooks/server-access.md)
- [运维诊断命令](20-operations/runbooks/diagnostics.md)
- [CLI 使用说明](20-operations/runbooks/cli-usage.md)
- [客户端 token 管理](20-operations/runbooks/client-tokens.md)
- [管理内网专用客户端](20-operations/runbooks/admin-innernet-client.md)
- [Hub API 部署](20-operations/runbooks/hub-api-deploy.md)
- [客户端配置示例](20-operations/configs/client/cn-client-01.yaml.example)
- [管理内网客户端配置示例](20-operations/configs/client/admin-innernet.conf.example)
- [Android reverse client 配置示例](20-operations/configs/egress/android-reverse-client.yaml.example)
- [Hub reverse server 配置示例](20-operations/configs/egress/hub-reverse-server.yaml.example)
- [Android 出口远程控制](../egress/android-control/README.md)

## 30 Implementation

- [Android 出口节点实现](30-implementation/android-egress-agent.md)
- [Android 出口极致加速研究](30-implementation/android-egress-performance-acceleration.md)
- [Hub 授权 API MVP](30-implementation/auth-api-mvp.md)
- [Hub 控制面板实现方案](30-implementation/hub-admin-panel.md)
- [CLI MVP 实现](30-implementation/cli-mvp-implementation.md)
- [zhvpn.exe 实现](30-implementation/zhvpn-exe-implementation.md)
- [zhvpn.exe 本地单例实现计划](30-implementation/client-singleton-plan.md)
- [桌面 GUI 客户端实现方案](30-implementation/desktop-gui.md)
- [Windows GUI 客户端优化 TODO](30-implementation/desktop-gui-client-todo.md)
- [Python SDK 实现方案](30-implementation/python-sdk.md)
- [服务端托管客户端配置](30-implementation/server-managed-client.md)
- [管理内网状态栏工具](../clients/admin-menubar/README.md)

## 40 Security

- [安全 TODO](40-security/security-todo.md)
- [Hub 安全审查 2026-06-04](40-security/security-audit-2026-06-04.md)

## 90 History

- [2026-06-06 Android 出口节点上线](90-history/worklogs/2026-06-06-android-egress.md)
- [2026-06-11 Pixel 7a 控制面迁移](90-history/worklogs/2026-06-11-pixel-control-plane-migration.md)
