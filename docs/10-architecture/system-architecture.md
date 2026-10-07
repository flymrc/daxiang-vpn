# 纵横 VPN 架构设计

## 目标

构建并维护一套流量分发系统，包含：

- 一个公网 Hub 服务器。
- 一个或多个日本本地出口设备，当前生产方向为 Android 手机出口；Mac mini 出口路线已弃用。
- 多个手机 IP 出口；住宅网络可用于承载隧道腿，但不作为当前目标网站出口。
- 多个中国侧客户端。
- 可维护的服务端代码和客户端 CLI。
- 中国客户端优先使用简单的本地代理地址，例如 `127.0.0.1:7890`，通过日本出口访问日本网站。

Hub 负责协调 Peer、路由、健康状态和出口分配。日本节点负责提供真实的本地公网出口。中国客户端可以通过 Hub 选择或接收分配好的日本出口。

相关文档：

- [出口方案选型](./egress-strategy.md)

## 网络模型

### 角色

| 角色 | 示例 | 职责 |
| --- | --- | --- |
| Hub | VPS `36.50.84.68` | 公网 WireGuard 入口、Peer 注册、路由控制、健康状态汇总 |
| 出口节点 | Android 手机出口 | 主动连接 Hub，把目标访问从手机运营商网络发出 |
| 客户端 | 中国侧桌面端、服务器、移动设备 | 连接 Hub，通过本地代理或 TUN 把流量发往选定出口节点 |
| 控制 CLI | `zhvpn` | 本机唯一控制面：导入配置、启动本地代理、同步配置、检查健康状态、切换出口，并提供 `--json` 机器接口 |
| Hub 管理控制台 | `zhhub admin` | 面向运维的公网 HTTPS 控制面板：由 Caddy 终止 HTTPS 并反代到本机,应用内管理员登录保护,查看 token、租约、出口健康和审计日志，并触发 Android 换 IP |
| 桌面 GUI | `纵横 VPN`（Tauri，Windows） | 面向终端用户的图形客户端：把 `zhvpn` 作为 sidecar 调用（登录/连接/换 IP/状态），用户态模式下自动设/还原 Windows 系统代理。详见 [桌面 GUI 实现方案](../30-implementation/desktop-gui.md) |
| Python SDK | `sdk/python` (`zongheng-vpn`) | 面向 Python 程序的薄封装：调用 `zhvpn` CLI 机器接口控制连接，并提供代理辅助。详见 [Python SDK 实现方案](../30-implementation/python-sdk.md) |

### 客户端控制面原则

`zhvpn` CLI 是客户端本机唯一控制面。GUI、Python SDK、后续其它语言 SDK 都只负责适配用户界面或语言生态，不直接实现 WireGuard、sing-box、Hub bootstrap、PID 管理、换 IP 控制等核心逻辑。

```text
GUI / Python SDK / automation
    |
    | subprocess + --json
    v
zhvpn CLI
    |
    | Hub bootstrap + local runtime management
    v
local proxy 127.0.0.1:7890
```

这样可以保持登录、连接、状态、换 IP、断开等行为只有一份实现，避免 GUI 和 SDK 产生第二套状态机。

2026-10-06 开发分支的首批重构把实例认证、操作锁与停止控制放在 `shared/proxy` 的独立 runtime 文件中，由 CLI 使用；PID 仅用于诊断。GUI 系统代理仍暂用独立可恢复 journal，迁入 CLI 的用户级租约尚未完成。精确合同与平台验证边界见[客户端实例与代理恢复合同](../30-implementation/client-runtime-safety-contract.md)，不能把本分支实现当作已发行客户端。

本地后续切片以 `shared/contracts/source.go` 同源生成 Go/JSON Schema/TypeScript/Python 的 CLI 合同，CLI 与引擎身份使用其类型；消费者语义见 [CLI JSON v1](../30-implementation/cli-json-contract-v1.md)。2026-10-07 本地已接 v2 设备签名 API、后台到期/启动对账与精确 WG adapter，入口默认关闭、要求与 legacy writer 分离的客户 interface；未导入现有 TokenStore 或接管当前 wg0。CLI 代理租约已在真实 phase/lifetime gate 接线，GUI 消费同一控制面；reverse 新增显式 tcp-tls 双端认证，生产 raw TCP 尚未迁移。实现、监督与验收边界见[运行时接线](../30-implementation/steelman-runtime-integration.md)，线上仍使用现有 YAML/WG 路线。

### 基础拓扑

```text
中国客户端
    |
    | WireGuard
    v
公网 Hub VPS
    |
    | WireGuard Peer 间转发
    v
日本出口节点
    |
    | NAT
    v
手机运营商 IP 网络
```

Hub 不能作为最终公网出口或兜底出口。Hub 的职责是中转中国客户端和日本出口节点之间的 WireGuard 流量;若出口节点的 IPv4 路径故障,应如实暴露为 IPv4 出口异常,不能改由 Hub VPS 出口。

2026-06-15 决策:Mac mini `10.66.0.100:1080` 出口路线已弃用。它不再作为新客户端、自动调度、专项爬虫验证或长期出口池方案;只保留为历史配置、管理内网和必要时的只读诊断对象。新数据面应默认使用 Android `zhreverse` Hub 入口 `10.66.0.1:18081`。

### 当前 Android 出口数据面

2026-10-06 09:40–09:45 JST 的只读记录：当时 `10.66.0.101` 观察到 Pixel 7a/Android 16，运行 `/data/adb/zhreverse/bin/zhreverse` 与 `/data/adb/zhandroid/bin/zhandroid-control`。当时手机配置为 TCP、双会话，二进制未提供 TCP TLS/mTLS 参数；QUIC pin 全零不能当作可用回滚凭据。见[该日资产基线](../90-history/worklogs/2026-10-06-zhvpn-asset-baseline.md)。2026-10-07 用户提供的 AGENTS.md 标明生产 Motorola 使用 `dxreverse/dxandroid-control` 兼容实现；这需要新一轮实机只读对账，不以旧记录推定当前手机型号或 supervisor 路径。

Hub 服务端保留 2026-10-05 上线的 `proxy_preempt_idle=10s`：达到全局 96 或每客户端 48 条 CONNECT 上限时，把已进入转发且最久无流量、空闲至少 10 秒的隧道让位给新 CONNECT；每客户端上限只抢占该客户端，拨号中会话不参与。原 2 分钟空闲回收仍保留；本次基线检查未改变服务、端口或出口策略。部署记录见[工作日志](../90-history/worklogs/2026-10-05-zhreverse-idle-preempt.md)。

Android 手机出口已从“手机在 WireGuard 内网监听 `10.66.0.101:1080`”迁到反向数据面:

```text
Hub egress router/client
    |
    | HTTP CONNECT
    v
Hub/WireGuard zhreverse proxy 10.66.0.1:18081
    |
    | TCP/yamux reverse tunnel, Android actively dials Hub TCP :39093
    | current POC: tunnel socket bound to wlan0 / residential WiFi
    v
Android zhreverse client
    |
    | current POC: target TCP/DNS sockets bound to rmnet1 / cellular
    v
public target
```

2026-06-14 当前生产 POC 使用 root/Linux `SO_BINDTODEVICE` 做 socket 级分流:`tunnel_bind_interface: wlan0` 让 Android -> Hub 隧道腿优先走住宅 WiFi IPv4,`target_bind_interface: rmnet1` 让 Android -> 目标网站仍走手机蜂窝出口。隧道腿启用 fallback:`wlan0` 连续失败后临时改走 `rmnet1`,并定期探测 `wlan0` 是否恢复;目标网站拨号不参与 fallback,始终绑定蜂窝。

WireGuard App 仍负责内网控制面,例如 `10.66.0.101:2022` SSH 运维、`10.66.0.101:5555` WG-only TCP ADB 和 watchdog 自愈。Android 客户端 token 的 `egress.proxy_addr` 应指向 Hub 的 WireGuard 地址 `10.66.0.1:18081`,不要再指向手机旧入站代理。旧 `zhandroid-egress` / `10.66.0.101:1080` Android 数据面已从生产入口拆除。

### Hub 管理控制台

2026-07-01 起新增 Hub 管理控制台实现。控制台作为 `zhhub` 同一二进制内的第二个 HTTP listener 运行:

```text
public browser
    |
    | HTTPS
    v
Cloudflare proxy (DNS currently orange-cloud)
    |
    | HTTPS to origin
    v
Caddy :443 / jp-proxy.ruichao.dev
    |
    | reverse_proxy
    v
zhhub admin 127.0.0.1:18100
```

控制台 API 固定在 `/admin/api/*`,前端静态资源由 Go `embed` 托管在 `/admin/`。Caddy 根路径 `/` 和未知路径都返回 404。应用内还有管理员登录、HttpOnly/Secure session cookie、CSRF token、登录限速和 SQLite 审计日志。公网只开放 Caddy 的 `80/443`;`18100/tcp` 只监听 localhost,不得直接暴露。原 Docker `librespeed` 测速页已停止并取消自动重启,由 Caddy 接管 `jp-proxy.ruichao.dev`。

### 客户端授权 API TLS 迁移

P0 安全目标是让中国客户端只经 HTTPS 访问 Hub bootstrap/rotate API:

```text
zhvpn client
    |
    | HTTPS /api/client/*
    v
Caddy :443 / jp-proxy.ruichao.dev
    |
    | reverse_proxy; listener identity is fixed by Hub registration
    v
zhhub trusted proxy API 127.0.0.1:18079

legacy clients --------------------> zhhub compat API 0.0.0.0:18080
```

客户端默认 API base 是 `https://jp-proxy.ruichao.dev`，仍保留 `ZHVPN_API_BASE` 作为测试/回滚覆盖。Hub 的两个客户端 listener 复用同一业务实现，但 ingress 身份由注册 handler 固定派生，不信任客户端提供的转发头：Caddy HTTPS 路由只进入 loopback `18079`，公网 `18080` 固定记为兼容入口。Hub 支持客户端上报 `wireguard_public_key`，由 Hub 用 `wg set` 应用 peer，新协议响应不再下发 `wireguard.private_key`。剩余迁移顺序是：先部署 observation-only 双 listener 并验证观测链，再发布可审计客户端，确认 bootstrap/rotate 正常，完成 campaign 和静默窗口，之后才清理 legacy 私钥并关闭公网 `18080/tcp`。在收口前，`18080/tcp` 和 legacy 私钥响应只是老客户端兼容路径，不应视为目标安全状态。

2026-08-28 已完成 observation-only 双 listener 生产部署和 Caddy `18079` 切换。兼容 listener、防火墙和 legacy 私钥保持原状；campaign `T0` 尚未设置，最终安全收口仍是 NO-GO。

## 分阶段设计

### P0：Hub 内网互通

当前状态。

- Hub 运行 WireGuard，接口为 `wg0`。
- Peer 分配 `10.66.0.0/24` 网段地址。
- Hub 允许 `wg0 -> wg0` 转发。
- 客户端可以通过 WireGuard 内网 IP 访问其他 Peer。

验收标准：

- Hub 的 `51820/udp` 可以访问。
- 每个 Peer 都能看到最近握手。
- 客户端防火墙允许时，Peer 之间可以 ping 通。

### P1：单个日本出口

当前实现目标。早期 Mac 出口验证已结束,不要再把 `10.66.0.100:1080` 作为新默认出口。

- Android 手机作为出口节点主动反连 Hub。
- Hub 暴露 `10.66.0.1:18081` HTTP CONNECT proxy。
- 中国客户端启动本地代理，例如 `127.0.0.1:7890`，把浏览器或应用流量经 WireGuard/Hub 转发到 Android `zhreverse`。

有两种路由模式：

| 模式 | 客户端 `AllowedIPs` | 使用场景 |
| --- | --- | --- |
| 全隧道 | `0.0.0.0/0` | 客户端全部流量从日本出口出去 |
| 分流 | 指定 CIDR | 只有指定目标网段从日本出口出去 |

第一版建议优先做“本地代理模式”，而不是直接改系统全局路由：

```text
浏览器 / 应用
    |
    | HTTP/SOCKS5 代理
    v
127.0.0.1:7890 / 127.0.0.1:7891
    |
    v
zhvpn CLI
    |
    v
WireGuard -> Hub -> Android zhreverse
```

这样普通用户只需要设置代理地址，不需要理解复杂路由。

重要路由说明：

- WireGuard 的 `AllowedIPs` 同时是路由选择器和 Peer 选择器。
- Hub 配置里不能随意让多个 Peer 拥有同一个目标 CIDR，否则会产生路由归属冲突。
- 出口路由应该由明确的路由编排来做，不要随便在一个接口上给多个出口节点都加 `0.0.0.0/0`。

推荐的 P1 做法：

- Hub 上 Peer 身份路由继续保持 `/32`。
- 中国客户端把代理流量送进 WireGuard。
- Hub 把来自客户端的 CONNECT 流量送到 `10.66.0.1:18081`。
- Android `zhreverse` 负责目标 TCP/DNS,目标网站应看到手机运营商出口。

### P2：多个出口节点

添加多个日本出口节点：

- `jp-mobile-01`
- `jp-mobile-02`
- 后续可加入 OpenWrt/工业蜂窝路由器出口。

每个出口节点需要上报：

- WireGuard 握手状态。
- 当前公网出口 IP。
- 到 Hub 的延迟。
- 本地 WAN 接口。
- NAT 状态。
- 可选的运营商或 ISP 标签。
- 容量和可用状态。

客户端可以通过两种方式分配出口：

- 手动分配：`zhvpn client assign --client cn-laptop --egress jp-mobile-01`
- 自动分配：根据健康状态、延迟、地区、IP 类型、容量等策略选择。

### P3：控制平面

从纯 Shell 脚本逐步演进为可维护的服务。Hub 管理控制台 v1 已按合同优先路线启动:OpenAPI 合同、SQLite schema、Go/TypeScript codegen、Svelte/Vite/Tailwind 前端和 Go embed 静态托管。

控制平面需要管理：

- Peer 清单。
- 密钥生成或公钥注册。
- WireGuard 配置渲染。
- 服务重载。
- 健康检查。
- 出口分配。
- 审计日志。

第一版可以先做成本地优先，在 Hub 上直接运行。后续再考虑增加带认证的 API。

## 仓库结构

推荐结构：

```text
zongheng-vpn/
  README.md
  README.md
  10-architecture/
  20-operations/
  docs/
    operations.md
    wireguard-routing.md
    security.md
  configs/
    hub/
      wg0.conf.template
    egress/
      macos-pf.conf.template
      linux-nftables.conf.template
    client/
      client.conf.template
      proxy-client.yaml.template
  server/
    zhvpn_server/
      __init__.py
      config.py
      peers.py
      wireguard.py
      routing.py
      health.py
      api.py
    tests/
  cli/
    zhvpn_cli/
      __init__.py
      main.py
      commands/
        peer.py
        hub.py
        egress.py
        client.py
        status.py
    tests/
  scripts/
    bootstrap-hub.sh
    bootstrap-egress-linux.sh
    bootstrap-egress-macos.sh
  state/
    example.inventory.yaml
```

## 状态模型

建议用声明式清单作为事实来源。

示例：

```yaml
hub:
  name: jp-hub-01
  public_ip: 36.50.84.68
  wg_interface: wg0
  wg_subnet: 10.66.0.0/24
  wg_ip: 10.66.0.1
  listen_port: 51820

peers:
  - name: cn-windows-01
    role: client
    wg_ip: 10.66.0.10
    public_key: "..."
    assigned_egress: jp-mobile-01

  - name: jp-mobile-01
    role: egress
    wg_ip: 10.66.0.101
    public_key: "..."
    proxy_addr: 10.66.0.1:18081
    egress_type: mobile
    enabled: true
```

服务端应该从这个清单渲染配置，而不是长期依赖手工追加配置。

## 服务端组件

### Peer 管理器

职责：

- 添加、删除、查看 Peer。
- 校验 Peer 名称、IP、公钥。
- 防止重复密钥或重复 IP。
- 自动分配下一个可用 WireGuard IP。
- 把 Peer 变更写入清单。

### WireGuard 管理器

职责：

- 渲染 `wg0.conf`。
- 同步渲染结果到 `/etc/wireguard/wg0.conf`。
- 使用 `wg syncconf` 或受控的 `wg-quick` 重载来应用变更。
- 读取 `wg show` 的运行时状态。

### 路由管理器

职责：

- 管理 Hub 转发规则。
- 必要时管理策略路由。
- 跟踪客户端到出口节点的分配关系。
- 生成客户端路由配置。

### 出口管理器

职责：

- 生成 Mac/Linux 出口节点安装和配置说明。
- 验证出口节点上的 NAT 和转发。
- 跟踪当前公网出口 IP。
- 标记出口节点健康或不健康。

### 健康检查管理器

职责：

- 检查 WireGuard 最近握手。
- 检查收发包计数。
- 在可行时 ping WireGuard 内网 IP。
- 让出口节点上报 WAN 公网 IP。
- 给 CLI 和日志输出状态。

## CLI 设计

CLI 名称：`zhvpn`。

推荐命令：

```bash
zhvpn login --hub 36.50.84.68 --token <令牌>
zhvpn import ./cn-client-01.yaml

zhvpn status
zhvpn ip
zhvpn test https://www.yahoo.co.jp

zhvpn hub status
zhvpn hub diagnostics

zhvpn peer list
zhvpn peer add --name jp-mobile-01 --role egress --ip 10.66.0.101 --public-key ...
zhvpn peer remove --name jp-mobile-01

zhvpn egress list
zhvpn egress assign --client cn-windows-01 --egress jp-mobile-01
zhvpn egress health --name jp-mobile-01

zhvpn client config --name cn-windows-01
zhvpn client route --name cn-windows-01 --mode full --egress jp-mobile-01
zhvpn client route --name cn-windows-01 --mode split --cidr 1.2.3.0/24 --egress jp-mobile-01

zhvpn proxy start --egress jp-mobile-01
zhvpn proxy start --auto
zhvpn proxy switch jp-phone-01
zhvpn proxy stop
zhvpn proxy status

zhvpn apply
zhvpn diff
```

普通中国客户端的极简使用方式：

```bash
zhvpn import cn-client-01.yaml
zhvpn proxy start --auto
zhvpn ip
```

默认本地代理地址：

```text
HTTP 代理：  127.0.0.1:7890
SOCKS5代理：127.0.0.1:7891
```

测试访问：

```bash
curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp
```

CLI 原则：

- 破坏性变更前先支持 `diff`。
- `apply` 负责渲染并部署配置。
- 涉及私钥输出时需要显式参数，例如 `--show-private-key`。
- 所有操作都应该记录操作对象、内容和时间。

## Mac 出口节点要求（已弃用）

Mac 出口路线已于 2026-06-15 标记为弃用。下面要求仅保留历史参考,不要再围绕 Mac 设计新出口池、token 默认值或专项验证路径。现有 `10.66.0.100:1080` 可用于只读诊断,但新数据面应使用 Android `zhreverse`。

历史 macOS 出口节点要求：

- 需要安装 WireGuard App 或 `wireguard-tools`。
- Peer 必须能和 Hub 完成握手。
- 必须开启 IP 转发。
- 需要通过 `pf` NAT 规则，把 WireGuard 来源流量 NAT 到 Mac 的 WAN 接口。
- 需要文档化持久化方案，因为 macOS 的网络配置可能在重启或接口变化后失效。

典型概念：

```text
WireGuard 接口：utunX
WireGuard IP：10.66.0.100/24
WAN 接口：en0 或桥接/手机网络适配器
NAT 来源：10.66.0.0/24
```

## Linux 出口节点要求

在 Linux 出口节点上：

- 必须安装 WireGuard。
- 必须开启 IP 转发。
- NAT 建议使用 nftables 或 iptables 配置。
- WireGuard 应由 systemd 保持在线。

典型概念：

```text
WireGuard 接口：wg0
WAN 接口：eth0/wlan0/wwan0
NAT 来源：10.66.0.0/24
```

## 安全规则

- 不要把服务器密码或私钥提交到 Git。
- 优先使用 SSH Key，不长期依赖 root 密码登录。
- WireGuard 私钥尽量只保存在所属设备上。
- 如果 Hub 代为生成客户端密钥，把配置交付给设备后应删除 Hub 上临时保存的客户端私钥。
- 如果后续增加管理 API，必须限制访问并加认证。
- Peer 和路由变更都应该记录审计日志。

## 近期下一步

2026-10-07 隔离开发新增共同 `httpboundary`、Admin runtime 校验及 canonical generator gate、共享身份公钥输入校验、离线恢复规划器和纯签名更新 verifier。HTTP 来源来自固定 listener/精确代理策略；Admin snapshot 有 generation/owner 防止权限失效后迟到响应复活。恢复规划器不执行 DB/WG 切换，update verifier 只返回 staging receipt；当前生产 listener、授权事实源、出口和 peer 没有因此变更。边界与未完成事项见[第二波合同](../30-implementation/steelman-http-admin-boundaries.md)及[运行时集成](../30-implementation/steelman-runtime-integration.md)。

同日第三波在兼容/trusted/Admin 昂贵入口共享有限 `Admission`，WG/SSH 由独立固定名额的 `processbudget.Runner` 执行。Linux 子树监督使用当前 Hub 的固定内部子命令，Windows 使用本次 Job；无新增生产端口或独立可启动服务。换 IP 启动后失败标记 process-local unknown，Admin 显式投影；它不构成跨重启 durable receipt，也不证明远端恢复完成。详见[兼容执行预算](../30-implementation/legacy-control-process-budget.md)。

第四波由CLI消费纯更新verifier：明确批准Policy与最近staging receipt保存在同一protected state，固定registration anchor和home操作锁约束初次登记/跨进程提交。没有在线更新服务、安装副作用或新的生产进程；同OS账户恢复旧状态仍在本地信任边界之外。[更新状态合同](../30-implementation/trusted-update-state.md)。

第五波在隔离 v2 authority 增加默认关闭的 proxy.bootstrap：固定受保护 profile 绑定 epoch/customer interface/精确 proxy `/32`，PoP 请求取得短期 TLS 配置投影。device bind 本地保存独立 X25519 私钥；device start 在同 home 锁下消费当前 binding/generation，generation 将完整 projection 和 sing-box 配置同时绑定到引擎 identity。实际 Windows CLI 经真实用户态 WG 访问 owned target、撤销后新请求失效和受保护 peer 正对照已贯通。它没有建立持续 session lease，没有更改生产端口、授权权威、wg0 或手机服务；Linux 产品后台启动仍未实现。见[设备启动合同](../30-implementation/v2-proxy-bootstrap.md)。

第六波在 Admin SQLite 增加显式人工批准的 provisional 固定清册、只追加 extra、单调历史、旧异常 metadata 负事实和持久 observer runs。auth bootstrap 已识别有效 token 的失败也落观察事务，sink 在挂载前先持久 prearm；关闭/换代期间请求直接拒绝，写失败或崩溃后重开保留缺口。Admin canonical readiness 升为 contract2，并新增只读迁移页面。TokenStore 仍是 legacy 授权事实源，声明 owner/installation 不是已验证 lineage，T0=null/ready=false 不变；生产旧 observer 与新源码分别记录，见[清册合同](../30-implementation/migration-inventory.md)。

第七波源码增加共享 `proxygate`、真实 reverse Admission 与 deviceapi 启动/续期 coordinator。Policy 固定受管 source、实际 listener 和 authority/profile摘要；Linux私有UDS closed ACK 后才做初始 Tick/最终 fenced WG+DB proof、短期绝对 grant、TLS Listen。普通/striped/fetch 在上游派发前登记，EOF/expiry使受管流失效；迟到 OpenStream 预约和 FIN 清理未知均阻断新 owner。protected/unknown scope 外流保持原限制，不整体关 WG。没有新增生产端口、部署或授权事实源切换；其他 WG 路径与同 IP 身份尚未隔离，见[屏障合同](../30-implementation/device-proxy-startup-barrier.md)。

1. 保持 Android `zhreverse` 作为默认数据面。
2. 确认新 token / bootstrap 配置默认指向 `10.66.0.1:18081`。
3. 增加基于清单的配置渲染。
4. 增加 Hub、客户端、Android 出口节点的健康状态报告。
5. 将 Mac 出口相关命令和文档继续收敛到 deprecated/historical 路径。
