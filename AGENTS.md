# AGENTS.md

给所有在本仓库工作的 AI agent / 协作者的约定。**核心规则:改了架构或线上现状,必须同步更新文档。**

## 第一性原则:文档与现状保持一致

任何改动了**架构、拓扑、节点、端口、IP、出口、配置参数、运维流程**的工作,提交前必须更新对应文档,避免文档和实际跑的东西对不上。

| 改了什么 | 必须更新 |
| --- | --- |
| 拓扑 / 节点 / 出口 / IP / 端口 | [README.md](README.md)、[docs/10-architecture/system-architecture.md](docs/10-architecture/system-architecture.md) |
| 出口节点状态、peer、token 绑定 | [docs/20-operations/runbooks/server-access.md](docs/20-operations/runbooks/server-access.md) |
| 排查/运维命令、健康检查 | [docs/20-operations/runbooks/diagnostics.md](docs/20-operations/runbooks/diagnostics.md) |
| 具体实现方案 | `docs/30-implementation/` |
| 安全相关 | `docs/40-security/` |
| 任何一天的实质性工作 | 在 `docs/90-history/worklogs/` 新增 `YYYY-MM-DD-*.md` |

> 文档总入口见 [docs/README.md](docs/README.md)。排查时以 `wg show` 等实时结果为准,文档可能滞后——发现滞后就顺手修正。

## 项目速览

纵横 VPN:Hub + 日本住宅出口 + Windows 客户端的代理网络。

```text
客户端 --WireGuard--> Hub(36.50.84.68 / wg0 10.66.0.1/24)
  +--> Mac mini 历史出口: 10.66.0.100:1080（弃用；仍保护管理资产）
  +--> Android 手机出口: Hub/WireGuard zhreverse proxy 10.66.0.1:18081
```

按角色分顶层,Go 代码统一在根 module `zongheng-vpn` 下:

- `clients/` — **客户端**(终端用户侧)。`clients/cli/` = CLI 客户端;`clients/desktop-gui/` = mac/windows PC 单一跨平台 GUI。客户 Android App 必须放在 `clients/android/`；其既有受控 canary/未发行状态不因本轮桌面重构改变。
- `hub/` — **Hub 服务端**(授权 API)。
- `egress/` — **出口节点**(基础设施侧,非终端客户端)。`egress/reverse/` = Android 反向 TCP/yamux 出口数据面；2026-10-06 的 Pixel 7a/zhreverse 为历史只读证据；2026-10-07 用户提供的当前约定为 Motorola 兼容 dxreverse。16:14 JST 本轮仅部署新版 Hub reverse，既有手机未替换；QUIC 仅实验且历史全零 pin 不构成可用回滚。`egress/proxy/` = sing-box 出口代理(Mac/PC 出口🅿️预留,不再用于 Android 生产);`egress/android-status/` = 安卓出口监控 App;`egress/android-control/` = 安卓出口远程控制+自愈，历史 Pixel 7a 的 zhandroid-control 记录不代替当前用户提供的 Motorola dxandroid-control/watchdog 布局；本轮未登录手机重新取证。当前 Hub/Windows CLI hash 与真实连通见[10-07部署记录](docs/90-history/worklogs/2026-10-07-wave8-compat-deployment.md)。
- `shared/` — 客户端与出口共用的 Go 包(`config`、`paths`、`proxy`)。
- `scripts/` — 运维脚本(如 `check-android-egress-health.ps1`、`measure-android-egress.ps1`)。

> 重要:现有 `egress/android-*` 是**出口基础设施**，客户 Android App 放 `clients/android/`。新增组件先按角色归类，不能因同为 Android 就混在一起。

## 操作纪律

- **生产主机**(Hub `root@36.50.84.68`、Mac、Android)上的命令优先只读;改状态(重载/重启/改 peer)前先确认。
- **不要 dump 含私钥的配置**(如 WireGuard `.conf`)到日志/对话;只读取需要的非密钥字段。
- ADB 走 root 时 `su -c "cmd1; cmd2"` 易丢权限,复杂操作先推脚本再 `su -c /path/script.sh`(详见 worklog)。
