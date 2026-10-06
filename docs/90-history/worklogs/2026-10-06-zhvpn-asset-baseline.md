# 2026-10-06 zhvpn 资产与授权事实基线

> 状态：READ-ONLY BASELINE。取证窗口：2026-10-06 09:40:52–09:45:06 JST（00:40:52–00:45:06 UTC）。这些是该窗口的事实，不保证以后仍相同。
> 实施工作树：`C:/Users/xuotq/.codex/worktrees/zhvpn-steelman-runtime/zongheng-vpn`；合流基线 `3c88880f37078c22b301f21e7b9a92e6fc3cc5bf`。本记录与[设备授权/撤销合同](../../30-implementation/device-auth-revocation-contract.md)是文档交付，没有新增授权模型或生产部署。

## 范围与取证纪律

- 使用现有密钥、`BatchMode=yes`、`StrictHostKeyChecking=yes` 完成 Hub、手机与 Mac 的只读 SSH；没有输入密码、授权新密钥或改远端状态。
- 读取进程身份、运行二进制、WG 公钥/allowed IP/endpoint/握手，对配置只提取非秘密字段，对 SQLite 只查询表名、计数、聚合。
- 没有输出 token、WireGuard 私钥、管理员密码/hash、完整环境或完整敏感配置；未测试真实 token，未调用 bootstrap/rotate，未跑 Jetstar。
- 本机 WG 公钥查询及 Mac `sudo -n wg show` 遇到权限不足后停止，没有提权或继续索取凭据。
- 公开记录只保留脱敏资产摘要。完整客户公钥、公网 endpoint 和用户身份不作为公开清册；本轮未导出完整数据库或秘密备份。

## 资产摘要

| 资产 | 窗口内直接证据 | 版本/功能缺口 |
| --- | --- | --- |
| Hub | Linux `x86_64`；`zhhub.service`、`zhreverse-hub.service` 均 active/running；当前运行路径分别为 `/opt/zongheng/zhhub/zhhub`、`/opt/zongheng/zhreverse/zhreverse` | 两个 ELF buildinfo 均为 `go1.26.5`、`linux/amd64`、`CGO_ENABLED=0`、module `(devel)`；没有可读取的 VCS revision，不能凭 buildinfo 宣称源码提交 |
| Hub 授权 API | 启动时间 2026-08-31 01:14:12 JST；运行二进制 SHA256 `c506ea7263dcd48117ea69682bf94097ac7134471b039a0262176edaf12bc1cb`；兼容入口 `0.0.0.0:18080`、可信入口 `127.0.0.1:18079`、Admin `127.0.0.1:18100`；租约参数 30 秒 | 进程内 `TokenStore` 只在启动时从 YAML 加载；本轮没有证明内存快照与当前磁盘配置逐项一致 |
| Hub reverse | 启动时间 2026-10-05 01:35:27 JST；SHA256 `16a2ab9e0dc3e82263b77c67da96a5437787fc3d7e84e748f8dd27f9a90160d3`，与 10-05 idle-preempt 部署记录一致；TCP `0.0.0.0:39093`，proxy `10.66.0.1:18081`；resolve=client；限额 96/48 | 配置文件没有显式 idle/preempt 字段，本轮从运行健康响应确认 `proxy_idle_timeout_ms=120000`、`proxy_preempt_idle_ms=10000`；不能把默认值推断代替该响应 |
| 手机控制地址 `10.66.0.101` | `getprop` 返回 Google、Pixel 7a、Android 16、`arm64-v8a`；已运行 `/data/adb/zhreverse/bin/zhreverse` 和 `/data/adb/zhandroid/bin/zhandroid-control`，由 `/proc/<pid>/exe` 确认 | 没有取得产品版本/源码 revision；存在旧 `dxreverse` 文件，但它不证明该文件正在运行，也不证明另一台手机的状态 |
| 本机 Windows RDP | 当前接口 `win-xuotq` 地址 `10.66.0.11`；`WireGuardTunnel$win-xuotq` Running/Auto；`TermService`、`UmRdpService` Running/Automatic | WG 公钥查询被拒绝；未执行 RDP 端到端连接测试 |
| 本机客户配置 | 当前 OS 用户默认 `LocalAppData/ZonghengVPN/config.yaml` 配置客户地址 `.40`；保存的客户公钥与 Hub 当前 `.40` peer 相符；proxy 指向手机入口 | 未发现 `zhvpn.exe`/`zhvpn-gui.exe` 当前进程、PATH 命令或所检查安装项；不能给出实际安装的 CLI/GUI 版本，也不能据配置存在宣称客户端正在运行 |
| Mac 管理/历史出口资产 | 现有 SSH 成功；`macOS 26.6.2`、build `25G83`、`arm64`；主机名与 Mac mini 记录一致；`utun0` 地址 `10.66.0.100`；实际运行 `wireguard-go` 和 `/opt/homebrew/bin/sing-box run -c /usr/local/etc/dxvpn/sing-box/mac-egress.json`；sing-box `1.13.12`，Go `1.26.3`；旧 `com.daxiang.dxvpn.*` LaunchDaemon 存在 | 没有管理员 WG 公钥读取权限；未测试此历史出口的代理功能；不能由旧 zongheng 路径或缺少同名 LaunchDaemon 推断整个 Mac 服务已停止 |
| Mac 客户配置 | `~/Library/Application Support/ZonghengVPN/config.yaml` 保存客户地址 `.30`；该配置中的客户公钥与 Hub 当前 `.30` 公钥不同；该 home 的 bin 目录为空，PATH 未发现 CLI | 配置可能过时，但原因/换钥归属未经证明；不能直接将 Hub `.30` 当前 peer 登记为这份配置的设备身份，客户 CLI/GUI 版本 missing |

手机运行二进制校验值：

- `zhreverse`：`0b28dda562afb69b5f8634fda8316a81335df7e1f4a84259c27943226aa2e67d`。
- `zhandroid-control`：`1be3e86931cbf481ba46364eed354f1f7ecc2a2c92ee8bf45de72bc13c6667fc`。

手机实际帮助输出支持 `transport=tcp|quic`、QUIC server-cert pin、2 个连接以及接口绑定/fallback 选项，没有直接启用 TCP TLS/mTLS 的参数证据。实际配置为 TCP、`connections=2`、IPv6 目标，隧道 `wlan0` 失败后 fallback 到 `rmnet1`，目标使用 `rmnet1`。QUIC pin 是全零占位值，不能视为 QUIC 回滚已准备好。Hub 健康响应有 2 条 reverse session，来源分别是住宅网和蜂窝网；这不证明链路聚合或 TLS 能力。

## 授权事实与 SQLite 实际归属

当前业务授权由启动时加载的 `tokens.yaml` / `auth.TokenStore` 决定。SQLite 目前是 Admin 管理会话、审计和观测/界面投影的持久化，不是设备授权或 WG 所有权的权威源。

磁盘 YAML 聚合结果：23 条记录、23 条 enabled、1 条到期日期早于取证 UTC 日期、21 条配置含非空 private-key 字段、0 条配置含非空 public-key 字段，客户地址没有重复。地址为 `.20`–`.41` 和 `.42`。本聚合只报告字段存在数量，没有输出任何密钥值；enabled 不能等同于当前有效授权，磁盘统计也不能等同于运行进程内的完整快照。

SQLite 用 `mode=ro`、`PRAGMA query_only=ON` 查询表名和聚合：

| 表 | 行数 | 当前职责 |
| --- | ---: | --- |
| `admin_users` | 1 | 管理员登录凭据；本轮只查计数 |
| `admin_sessions` / `admin_login_attempts` | 0 / 0 | 管理会话与登录限速记录 |
| `audit_events` | 50000 | 有保留上限的审计事件，不能作为完整撤销账本 |
| `client_migration_observations` | 9 | 成功 bootstrap 的 token 观测投影：6 legacy、1 secure_bootstrap、2 unknown |
| `egress_nodes` | 1 | Admin 出口投影 |
| `tokens_cache` / `token_leases` / `rotate_locks` | 0 / 0 / 0 | 存在 schema，不代表目前是授权、租约或锁的运行事实源 |
| `sqlite_sequence` | 2 | SQLite 内部序号，不是业务资产 |

没有 device、credential、peer ownership、durable outbox、revocation 或 campaign 成员表。当前 readiness 源码固定 `ready=false`、`campaign_configured=false`、`mode=observation_only`，有效 token 分母在每次请求从 `TokenStore` 重新计算。1 条 secure_bootstrap 观测不代表一个安装实例已 compliant。

源码对账位置：

- `hub/main.go`：加载 `TokenStore`，注册 compat/trusted listener。
- `hub/internal/auth/server.go`：bootstrap 接收客户公钥后直接调用 `wg set`；租约在进程内 map，按来源 IP 和 30 秒窗口判断；没有持久设备绑定/自动 peer 撤销。
- `hub/admin/internal/api/summaries.go`：lease/rotate 投影来自进程内快照，Admin 列表访问可更新 SQLite 投影；没有从投影恢复运行租约的代码。
- `hub/admin/internal/db/store.go`：成功 bootstrap 的 audit 与 migration observation 在同一 SQLite 事务提交；该事务不包含 WireGuard 应用动作。
- `hub/admin/internal/api/migration.go`：observation-only readiness，不是正式 campaign。

## WG peer 归属与导入边界

Hub 窗口内有 28 个 peer，27 个有 allowed IP，1 个为 `(none)`。握手/endpoint 只能证明近期链路活动，不能证明客户身份或废弃状态。

| runtime 地址/条目 | 能证明的关联 | 清册处置 |
| --- | --- | --- |
| `.11` | 本机接口地址与 RDP 服务、Hub peer 地址相互支持 | 保护为 RDP 基础设施；本机公钥链条尚 missing，不导入客户授权 |
| `.100` | 实际 Mac 地址和正在运行的历史基础设施组件 | 保护为 Mac 基础设施；不当成现有客户出口池或客户设备 |
| `.101` | 手机 SSH 实机身份、实际 reverse/control 进程 | 保护为手机控制面基础设施；reverse credential 与客户身份分别迁移 |
| `.12` | Hub 存在 peer；本轮没有读取对应终端身份 | owner/角色 unknown，保护并待核实，不凭历史用途或公网 endpoint 认领 |
| `.10` | Hub 存在 peer；没有当前终端归属证据 | unknown，保护并待核实 |
| `(none)` | 公钥条目存在但没有 allowed IP | unknown；不能因未握手或无 allowed IP 自动删除 |
| `.20`–`.41` | 当前 YAML 地址与 Hub peer 地址对应；`.40` 另有本机配置公钥匹配，`.30` 有 Mac 配置公钥不一致 | customer candidate 清单，不等于已完成 device ownership。逐安装实例核验；公钥冲突/历史替换先处置，不能自动认领 |
| YAML `.42` | 有配置地址，runtime 当前没有同地址 peer | config/runtime 差异；是否未激活、已过期或测试条目仍需业务证据，不自动补建 |

本轮没有逐 token 读取并披露人员清册，也没有建立 campaign `T0`。所有非 customer 或未知 peer 都不在未来 reconciler 的自动删除范围；即使未来数据库没有其记录也不能删除。

## 已发现的漂移与缺口

1. 之前 Motorola/dxreverse 的叙述与本窗口 `10.66.0.101` 实机证据不符。当前应以 Pixel 7a/zhreverse/zhandroid-control 为本次能力盘点对象；另一台设备的历史不能据此认定不存在。
2. Mac 仍运行旧 dxvpn 路径，不能按 zongheng 路径直接替换/清理；客户 `.30` 配置与 runtime 公钥不一致，需要独立归属核验。
3. 两个 Hub 二进制缺少源码 revision metadata，手机缺少产品版本；本轮只有运行 hash/部分 buildinfo，不足以形成完整发行 provenance。
4. SQLite 没有正式 campaign 或授权模型；完整历史 audit 已有容量上限。未来导入不能从最近的 9 条观测或仅当前有效 token 反推所有安装实例和历史处置。
5. 客户 `.40` 配置存在并匹配 runtime 公钥，但本机客户产物版本与运行证据 missing；RDP `.11` 是独立基础设施，不应因同一 Windows 主机混成客户身份。

## P0 后续门禁

- [ ] 核对各二进制 hash 与可恢复构建产物/源码，补足缺失版本与平台证据；未核实项保持 missing。
- [ ] 建立受限安装实例清册、客户 ownership 候选和受保护 peer 清单，核实 `.30` 冲突及 `.10`/`.12`/`(none)`。
- [ ] 记录 token 启动快照与磁盘配置的一致性证据，不以磁盘统计冒充内存授权快照。
- [ ] 在隔离数据上固定授权导入、campaign lineage、换钥、撤销、重启和安全回退合同；不得直接用生产 token 激活来补证。
- [ ] 继续遵循既有 observation-only/NO-GO、观察窗口和不增加 hosted CI 政策；本记录不授权生产变更。
