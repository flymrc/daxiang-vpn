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

大象 VPN:Hub + 日本住宅出口 + Windows 客户端的代理网络。

```text
客户端 --WireGuard--> Hub(36.50.84.68 / wg0 10.66.0.1/24)
  +--> Mac mini 出口:    10.66.0.100:1080
  +--> Android 手机出口: Hub 本地 dxreverse proxy 127.0.0.1:18081
```

按角色分顶层,Go 代码统一在根 module `daxiang-vpn` 下:

- `clients/` — **客户端**(终端用户侧)。`clients/cli/` = CLI 客户端;`clients/desktop-gui/` = mac/windows PC 单一跨平台 GUI(🅿️ 预留)。
- `hub/` — **Hub 服务端**(授权 API)。
- `egress/` — **出口节点**(基础设施侧,非终端客户端)。`egress/reverse/` = Android 反向 QUIC 出口数据面(`dxreverse`,当前生产替代路径,Android 主动连 Hub);`egress/proxy/` = 旧 sing-box 出口代理(Android 上仅保留回滚,Mac/PC 出口🅿️预留);`egress/android-status/` = 安卓出口监控 App;`egress/android-control/` = 安卓出口远程控制+自愈(自研 Go SSH 服务 `dxandroid-control` 绑隧道 IP 10.66.0.101:2022、仅公钥 + 看门狗)。
- `shared/` — 客户端与出口共用的 Go 包(`config`、`paths`、`proxy`)。
- `scripts/` — 运维脚本(如 `check-android-egress-health.ps1`、`measure-android-egress.ps1`)。

> 重要:安卓相关都在 `egress/` 下,是**出口**不是终端客户端。新增组件先归到对的角色目录。

## 操作纪律

- **生产主机**(Hub `root@36.50.84.68`、Mac、Android)上的命令优先只读;改状态(重载/重启/改 peer)前先确认。
- **不要 dump 含私钥的配置**(如 WireGuard `.conf`)到日志/对话;只读取需要的非密钥字段。
- ADB 走 root 时 `su -c "cmd1; cmd2"` 易丢权限,复杂操作先推脚本再 `su -c /path/script.sh`(详见 worklog)。
