# 大象 VPN 文档入口

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
  +-- mac-mini       10.66.0.100:1080 -> 日本住宅出口
  |
  +-- jp-android-01  10.66.0.101:1080 -> 日本手机卡出口
```

当前两个出口都已验证 HTTP 和 SOCKS5 mixed 代理可用。

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
- [Hub API 部署](20-operations/runbooks/hub-api-deploy.md)
- [客户端配置示例](20-operations/configs/client/cn-client-01.yaml.example)
- [Android 出口配置示例](20-operations/configs/egress/android-egress-01.yaml.example)

## 30 Implementation

- [Android 出口节点实现](30-implementation/android-egress-agent.md)
- [Hub 授权 API MVP](30-implementation/auth-api-mvp.md)
- [CLI MVP 实现](30-implementation/cli-mvp-implementation.md)
- [dxvpn.exe 实现](30-implementation/dxvpn-exe-implementation.md)
- [服务端托管客户端配置](30-implementation/server-managed-client.md)

## 40 Security

- [安全 TODO](40-security/security-todo.md)
- [Hub 安全审查 2026-06-04](40-security/security-audit-2026-06-04.md)

## 90 History

- [2026-06-06 Android 出口节点上线](90-history/worklogs/2026-06-06-android-egress.md)

