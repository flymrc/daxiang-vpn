# 纵横 VPN 文档入口

这份目录按“先理解现状，再看架构，再做运维”的顺序整理。

## 先看这几份

1. [当前 MVP 计划](00-overview/mvp-plan.md)
2. [客户端使用指南](00-overview/client-guide.md)
3. [系统架构](10-architecture/system-architecture.md)
4. [出口方案选型](10-architecture/egress-strategy.md)
5. [2026-06-06 Android 出口上线记录](90-history/worklogs/2026-06-06-android-egress.md)

## 当前状态速览

2026-10-07 已完成[第八波兼容部署与本机新版 Windows CLI 连通验收](90-history/worklogs/2026-10-07-wave8-compat-deployment.md)。Hub/reverse/CLI 来源 `3b5a2c5`，既有手机双会话恢复、实际 HTTPS 与 IPv6 出口请求成功；v2授权、手机mTLS及正式发行仍未完成。

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

手机型号/进程名有待重新对账：2026-10-07 用户提供的 AGENTS.md 标明 Motorola 兼容 `dxreverse/dxandroid-control`；10-06 的 Pixel/zh 路径记录保留为历史证据，不作为当前部署命令。访问前先按[服务器记录](20-operations/runbooks/server-access.md)做只读核验。

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

- [Steelman 重构 checkbox 计划](30-implementation/zhvpn-steelman-refactor-plan.md)
- [2026-10-07 运行时接线与验证边界](30-implementation/steelman-runtime-integration.md)
- [Hub HTTP 与管理台边界](30-implementation/steelman-http-admin-boundaries.md)
- [设备 authority 离线恢复规划](30-implementation/device-authority-offline-restore-plan.md)
- [兼容控制面共享准入与原生执行预算](30-implementation/legacy-control-process-budget.md)
- [可信更新元数据 staging 验证](30-implementation/trusted-update-metadata-verifier.md)
- [离线CLI可信策略与更新水位](30-implementation/trusted-update-state.md)
- [设备 v2 CLI 消费者](30-implementation/device-client-v2.md)
- [设备 v2 到真实代理启动](30-implementation/v2-proxy-bootstrap.md)
- [设备代理接线 checkbox](30-implementation/v2-proxy-integration-plan.md)
- [固定迁移清册与观察缺口 checkbox](30-implementation/migration-inventory-plan.md)
- [Provisional 清册、单调历史与 observer 合同](30-implementation/migration-inventory.md)
- [真实代理启动屏障合同](30-implementation/device-proxy-startup-barrier.md)
- [真实代理启动屏障 checkbox](30-implementation/device-proxy-startup-barrier-plan.md)
- [受管上游派发fence checkbox](30-implementation/proxy-dispatch-fence-plan.md)
- [专用客户端引擎事件与日志健康](30-implementation/client-engine-log.md)
- [客户端日志实施checkbox](30-implementation/client-engine-log-plan.md)
- [桌面登录与全新安装边界](30-implementation/desktop-fresh-install-boundary.md)
- [客户端实例与代理恢复合同](30-implementation/client-runtime-safety-contract.md)
- [CLI JSON v1 维护源与消费者](30-implementation/cli-json-contract-v1.md)
- [P1 设备授权与撤销合同](30-implementation/device-auth-revocation-contract.md)
- [设备授权持久模型基础](30-implementation/device-auth-foundation.md)
- [CLI 系统代理租约基础（接线见后续合同）](30-implementation/system-proxy-lease-foundation.md)
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
- [客户端 HTTPS 与本地 WireGuard 密钥迁移计划](40-security/client-security-migration-plan.md)
- [Hub 安全审查 2026-06-04](40-security/security-audit-2026-06-04.md)

## 90 History

- [2026-10-07 提交main与清理其他分支](90-history/worklogs/2026-10-07-main-publish-branch-cleanup.md)

- [2026-10-07 第八波兼容部署与新版客户端连通](90-history/worklogs/2026-10-07-wave8-compat-deployment.md)

- [2026-10-07 运行时接线与独立审计](90-history/worklogs/2026-10-07-zhvpn-runtime-integration.md)
- [2026-10-07 第二波安全与管理台边界](90-history/worklogs/2026-10-07-zhvpn-security-boundaries.md)
- [2026-10-07 兼容控制面执行预算](90-history/worklogs/2026-10-07-legacy-control-process-budget.md)
- [2026-10-07 离线可信更新状态](90-history/worklogs/2026-10-07-zhvpn-trusted-update-state.md)
- [2026-10-07 设备真实 WG 代理链](90-history/worklogs/2026-10-07-v2-proxy-bootstrap.md)
- [2026-10-07 固定清册与观察缺口](90-history/worklogs/2026-10-07-migration-inventory.md)
- [2026-10-07 当前运行二进制只读复核](90-history/worklogs/2026-10-07-live-readonly-inventory.md)
- [2026-10-07 桌面独立复核](90-history/worklogs/2026-10-07-desktop-independent-review.md)
- [2026-10-06 CLI 合同、授权与代理租约基础](90-history/worklogs/2026-10-06-zhvpn-contracts-foundations.md)
- [2026-10-06 Steelman 客户端安全首切片](90-history/worklogs/2026-10-06-zhvpn-runtime-safety-slice.md)
- [2026-10-06 资产与授权事实基线](90-history/worklogs/2026-10-06-zhvpn-asset-baseline.md)
- [2026-10-06 Steelman 重构计划](90-history/worklogs/2026-10-06-zhvpn-steelman-refactor-plan.md)
- [2026-10-05 项目多维审计](90-history/worklogs/2026-10-05-zhvpn-project-audit.md)
- [2026-06-06 Android 出口节点上线](90-history/worklogs/2026-06-06-android-egress.md)
- [2026-06-11 Pixel 7a 控制面迁移](90-history/worklogs/2026-06-11-pixel-control-plane-migration.md)
