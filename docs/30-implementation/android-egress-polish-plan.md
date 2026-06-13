# Android 出口打磨计划（2026-06-11 起实施）

## 背景

2026-06-10 审计定位根因（乐天 F5 BIG-IP 透明代理终结全部 80/443 TCP，v4 侧高故障）并完成第一轮改造：Hub `resolve: client` + 手机 `address_family: ipv6` + DNS v6 优先 + yamux 4MB 窗口/30s 写超时。经代理 20MB 从 100% 失败提升到 28.9 Mbps 零失败。详见 [2026-06-10-pixel-7a-speed-audit.md](../90-history/worklogs/2026-06-10-pixel-7a-speed-audit.md)。

本文是后续打磨项的实施计划，按"先小后大"排序。当前生产基线：commit `04070a7`，两端二进制/配置均有 `.bak-20260610` 备份。

## 执行顺序总览

| 序 | 项目 | 预估 | 前置 |
| --- | --- | --- | --- |
| 1 | 手机端 DNS 缓存 | ~1h | 无 | ✅ 已完成 2026-06-11 |
| 2 | 健康检查对齐 v6 形态 | ~30min | 无 | ✅ 已完成 2026-06-11 |
| 3 | QUIC over UDP A/B | ~半天 | 无 | 🔜 2026-06-12 与 IPv6 并行 |
| 4 | `connections: 2` 复测 | ~30min | 建议在 3 决策后 | ✅ 已完成 2026-06-11 |
| 5 | WireGuard 控制面迁移到 Pixel | ~半天 | 无（运维兜底，越早越安心） | ✅ 已完成 |
| 6 | Hub VPS 启用 IPv6 | 待运维操作 | 机房需先分配 IPv6 | 🔜 2026-06-12 运维执行 |
| 7 | Hub session 健康优先调度 | ~1h | `connections: 2` | ✅ 已部署 2026-06-14 |
| 8 | Hub session 健康观测接口 | ~30min | 7 | ✅ 已部署 2026-06-14 |
| 9 | reverse tunnel micro-benchmark | ~45min | 8 | ✅ 已部署 2026-06-14 |

---

## 1. 手机端 DNS 缓存 ✅ 已完成 2026-06-11

按下述方案实现并部署（`dnsCacheGet/dnsCachePut` + `TestDNSCacheHitAndExpiry`/`TestDNSCacheCapacityReset`，二进制 SHA256 `e622f386...aefb33`，手机备份 `zhreverse.bak-20260611-dnscache`）。详见 [2026-06-11-zhreverse-tls-first-flight-retry.md](../90-history/worklogs/2026-06-11-zhreverse-tls-first-flight-retry.md)。

**动机**：`resolve: client` 后每个 CONNECT 都做一次 DNS（v6 UDP 约 20-60ms，偶发更慢），浏览器并发开页时放大明显。

**方案**（`egress/reverse/main.go` 客户端侧）：

- `dialTarget` 解析前查进程内缓存：`map[string]dnsCacheEntry{ips []net.IPAddr, expires time.Time}` + `sync.Mutex`。
- TTL 60s；条目上限 ~1024（满了整体清空即可，不必上 LRU）；解析失败不缓存（或 5s 负缓存，可选）。
- 缓存存原始 `ips`，`orderIPs` 仍在拨号时排序（地址族偏好可能变）。
- FETCH 路径共用 `dialTarget`，自动受益。

**验收**：`go test ./egress/reverse/`（补一个缓存命中/过期单测）；同域名连续 CONNECT 第二次起无 DNS 延迟。

**部署**：重编 arm64 → 手机替换 → supervisor 拉起（流程见文末"已知坑"）。

## 2. 健康检查对齐 v6 形态 ✅ 已完成 2026-06-11

`check-android-reverse-egress.sh` 与 `check-android-egress-health.ps1` 均已改为：v6 主路径（`api6.ipify.org`，FAIL 级，`240b:` 前缀标注 Rakuten）+ v4 兜底（`api.ipify.org`，失败仅 WARN；**等于 Hub IP `36.50.84.68` 则按 hub-fallback 回归 FAIL**）。v4 出口前缀不做断言（实测有 `133.106.x` 与 `210.157.x` 等多段）。`measure-android-egress.ps1` 用 api64 仅作展示无断言，未动。`server-access.md` 常用检查命令已同步。

**动机**：`scripts/check-android-reverse-egress.sh` 默认 `TEST_URL=https://api.ipify.org`（v4-only）——现在测的是 F5 v4 最差路径，不代表生产主路径（v6）。

**改动**：

- 改为双测并分别报告：`https://api6.ipify.org`（主路径，FAIL 级）+ `https://api.ipify.org`（v4 兜底，降级为 WARN 级）。
- 出口形态断言更新：v6 期望 `240b:`（乐天移动网段）开头；v4 期望 CGNAT 出口 `133.106.x`。
- 顺带检查 `check-android-egress-health.ps1`、`measure-android-egress.ps1` 是否有 v4/出口 IP 假设；`server-access.md` 常用检查命令同步。

## 3. QUIC over UDP A/B

**动机**：隧道腿脱离 TCP 后，F5/CGNAT 的 TCP 处理（Policy RST、流表丢失）对 UDP 无效；QUIC 自带 0-RTT 重连；代码与 4-16MB 窗口配置均已就绪。之前 QUIC 测试差是旧手机+烂小区下的结果，不作数。

**步骤**：

1. Hub 起第二个 zhreverse 实例（不动生产 TCP）：
   - `transport: quic`，listen `:39094`（UFW `allow 39094/udp`），proxy `10.66.0.1:18082`（UFW 对 wg0 放行），同 token，`tls_cert_file/key` 用现有 `/etc/zongheng/zhreverse/server.crt|key`。
   - 算证书指纹：`openssl x509 -in /etc/zongheng/zhreverse/server.crt -outform DER | sha256sum`。
   - Hub 侧 UDP buffer：quic-go 建议 `sysctl -w net.core.rmem_max=8388608 net.core.wmem_max=8388608`（手机侧 service 脚本已调过）。
2. 手机临时手动跑第二个 client（不动生产配置）：
   `zhreverse client --server 36.50.84.68:39094 --transport quic --server-cert-sha256 <fp> --token-file /data/adb/zhreverse/token --address-family ipv6`
3. A/B 矩阵（Hub 侧）：经 `18081`(tcp) vs `18082`(quic) 各跑 2MB x5 / 10MB x2 / 20MB x1，记录成功率与速度；再各跑一轮长连接稳定性（撑 30min 看会话存活）。
4. **决策标准**：QUIC 吞吐 ≥ TCP 且失败率不升 → 生产切 `transport: quic`（两端改配置即可，`server_cert_sha256` 字段早已预留）；TCP 配置保留为回滚路径。

**风险**：乐天对 UDP 的 QoS/限速未知——实测说话；CGNAT UDP 映射超时——`KeepAlivePeriod: 5s` 已在 `quicConfig()` 配好。

## 4. `connections: 2` 复测 ✅ 已完成 2026-06-11

看门狗部署后用户仍报 v4 不稳，Hub 日志确认隧道腿（39093，单 yamux 会话）当天掉线 57 次，`connections: 1` 时一条冻死则当时所有代理连接连坐全死。已改 `client.yaml` `connections: 2`（备份 `client.yaml.bak-20260611-conn2`），重启后手机两行 `connected`、Hub 侧两条 session（`:17733` + `:6650`），ip138 8/8 成功。示例配置与 `TestLoadReverseConfigExamples` 已同步断言 2。详见 [2026-06-11-zhreverse-tls-first-flight-retry.md](../90-history/worklogs/2026-06-11-zhreverse-tls-first-flight-retry.md)。

- 回滚：`cp client.yaml.bak-20260611-conn2 client.yaml && pkill zhreverse`。改动只在手机配置，回滚成本为零。
- 后续若切 QUIC（第 3 项），在 QUIC 上复跑同一速度矩阵确认两条 session 都稳。

## 7. Hub session 健康优先调度 ✅ 已部署 2026-06-14

**动机**：`connections: 2` 只能提供两条隧道会话;如果 Hub 盲目轮询,新 CONNECT 仍可能落到更忙、RTT 更高或刚失败过的 session。健康优先调度把已有双连接收益吃满,不增加手机端连接数和额外耗电。

**实现**（`egress/reverse/main.go` Hub 侧）：

- 每条 reverse session 维护 active stream 数、连续失败、最近失败时间和 command RTT EWMA。
- 新 CONNECT / FETCH 优先选择 active 少、RTT 低、近期无失败的 session。
- stream 关闭时释放 active 计数;命令阶段失败仍沿用旧逻辑剔除该 session 并重试其它 session。
- 单测覆盖低 RTT 优先、空闲 session 优先和 stale session 剔除。

**验收**：`go test ./egress/reverse` 与 `go test ./...` 通过。Hub 已部署新二进制,备份 `/opt/zongheng/zhreverse/zhreverse.bak-20260614-health-picker`;部署后 Android 双 session 重连、健康检查 PASS、20MB x2 平均约 `24.98 Mbps`。后续继续观察 Hub 日志中 stale session 剔除频率、客户端测速和浏览器并发打开时的尾延迟。

## 8. Hub session 健康观测接口 ✅ 已部署 2026-06-14

**动机**：健康优先调度上线后,需要能直接看到 Hub 当前如何评价每条 Android reverse session,否则下一步调 `connections`、超时或弱网重试只能靠日志和测速反推。

**实现**（`egress/reverse/main.go` Hub 侧）：

- 新增 `GET /debug/session-health`,复用 Hub 侧 `10.66.0.1:18081` proxy listener,仍受 `allowed_proxy_cidrs` 保护。
- JSON 返回 `session_count`、每条 session 的 `remote_addr`、`active_streams`、`consecutive_failures`、`ewma_command_rtt_ms`、`last_failure_ago_ms` 和 `scheduler_score_ms`。
- 同时返回 `active_proxy_connections` 和按客户端 IP 统计的并发数,便于判断浏览器爆发并发是否压满护栏。
- `check-android-reverse-egress.sh` 和 `check-android-egress-health.ps1` 接入该接口;旧二进制不支持时只 WARN,不影响基础健康检查。

**验收**：`go test ./egress/reverse` 与 `go test ./...` 通过。Hub 已部署新二进制（SHA256 `cb17ae63321f578d24f81c36f2ce6eaf7f1bd422ecf034245f9123a50bfde0f6`）,备份 `/opt/zongheng/zhreverse/zhreverse.bak-20260614-session-health-debug`;部署后 `zhreverse-hub.service` active,Android 双 session 重连,`/debug/session-health` 返回 `session_count=2` 且两条 session `consecutive_failures=0`。

## 9. reverse tunnel micro-benchmark ✅ 已部署 2026-06-14

**动机**：真实网页/下载测速会混入 DNS、目标站 TLS、CDN、Rakuten v4/F5 等变量。要判断是否值得做更底层的 Go 多流分片,必须先单独测 Android -> Hub reverse tunnel 回传能力。

**实现**（`egress/reverse/main.go`）：

- Hub 新增 `GET /debug/tunnel-bench?bytes=<total>&streams=<n>`,复用 `10.66.0.1:18081` proxy listener 并受 `allowed_proxy_cidrs` 保护。
- Hub 按 `streams` 把总字节数平均拆分,并发向 Android 打开 reverse stream,发送 `BENCH <bytes>` 命令。
- Android 收到 `BENCH` 后只写回合成字节流,不访问公网目标。
- JSON 返回总 `bytes_read`、总 Mbps、每条 stream 的请求字节数、实际读取字节数、命令 RTT 和 Mbps。
- 参数上限:总字节数最大 `100000000`,streams 最大 `8`,避免诊断接口被误用成持续压测器。

**验收**：`go test ./egress/reverse` 与 `go test ./...` 通过。Hub 和 Android 均已部署新二进制：

- Hub SHA256 `6d395a4fd2522b8fb56692e066198595c8df99e6430359eae5c18e41976a0f19`,备份 `/opt/zongheng/zhreverse/zhreverse.bak-20260614-tunnel-bench`。
- Android SHA256 `77f6bc6a7a5da3968a3480653513ff31e753a620bb253635e308abec2c25acb4`,备份 `/data/adb/zhreverse/bin/zhreverse.bak-20260614-tunnel-bench`。

部署后实测:

```bash
20MB streams=1: 22.74 Mbps
20MB streams=2: 46.57 Mbps
20MB streams=4: 48.84 Mbps
```

`streams=2` 相对单流几乎翻倍,说明当前 Android -> Hub 隧道腿存在明显并行收益;`streams=4` 只小幅高于双流,说明收益主要来自两条 reverse session,继续堆流开始接近总链路上限。下一步可评估 striped CONNECT / 多流分片,但应优先设计为按需启用,避免给普通小请求增加复杂度。

## 5. WireGuard 控制面迁移到 Pixel

**状态（2026-06-11）**：已完成。Pixel 7a 已接管 `10.66.0.101` WireGuard 控制面，Hub 可 SSH 到 `10.66.0.101:2022`，TCP ADB 已限制在 `10.66.0.0/24 -> 10.66.0.101:5555`，watchdog 的 WireGuard UP 与 DOWN-wait-UP 自愈均已演练通过。

**动机**：Pixel 上 `10.66.0.101` 控制面未迁移，zhandroid-control/watchdog 自愈缺位，人不在手机旁出问题只能干等。数据面已稳，这是当前最大的运维风险敞口。

**步骤**（参照 [2026-06-08-android-wireguard-self-heal.md](../90-history/worklogs/2026-06-08-android-wireguard-self-heal.md) 与 [android-egress-agent.md](./android-egress-agent.md)）：

1. 已安装 WireGuard App `1.0.20260315` 并导入 `jp-android-01.conf`（10.66.0.101，Hub peer 已替换为 Pixel 新公钥）。
2. 已开启 WireGuard App “授权外部控制”，watchdog 可通过 root broadcast 拉起 `jp-android-01`。
3. 已部署 zhandroid-control（`10.66.0.101:2022`）+ watchdog + WG-only TCP ADB 脚本到 `/data/adb/`。
4. 已验收：Hub `ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101` 通；Hub ping 通；watchdog UP 与 DOWN-wait-UP 自愈演练通过。
5. 已更新 `server-access.md` / `diagnostics.md` 并新增 2026-06-11 工作日志。

## 6. Hub VPS 启用 IPv6（🔜 2026-06-12 运维执行）

**目标**：隧道腿（手机→Hub:39093）现在跑在手机 IPv4/CGNAT 上，是隧道反复冻死/dial timeout 的根源。给 Hub 配上全局 IPv6 后，手机经原生 v6（乐天 `240b:...`，实测健康）连 Hub，隧道腿落到 F5 健康的 v6 侧，绕开 CGNAT TCP 流表。

### 勘察结论（2026-06-11，SSH 只读确认）

- Hub `36.50.84.68`（Ubuntu 24.04，提供商 **Hostsymbol Pte Ltd**，机房日本）**当前没有任何全局 IPv6**：eth0 仅 link-local `fe80::216:3eff:fe22:5ee`，无 GUA、无 v6 默认路由。
- 链路上**有 IPv6 路由器存在**：`ping6 ff02::2%eth0`（all-routers）得到 `fe80::d6ba:5b56:d1b6:94fb` 响应（4ms）。说明机房网络支持 v6，只是没主动下发 RA → **静态分配**模式。
- 网络配置走 **ifupdown**（`/etc/network/interfaces`，eth0 静态 v4），**不是 netplan**（本计划旧版误写 netplan，已订正）。systemd-networkd 在跑但 eth0 标 unmanaged。

### 前置（运维先做，卡点）

登 **Hostsymbol 控制面板** → 该 VPS 的 IPv6 / Networking 栏，取：

- 分配的 **IPv6 地址或前缀**（如单个 `2xxx:...::2/64` 或一个 `/64` 段）；
- **IPv6 默认网关**（很可能就是上面探到的 `fe80::d6ba:5b56:d1b6:94fb`，但以面板/工单为准）。

面板查不到就开工单：「Does this VPS have an IPv6 allocation? Please provide the IPv6 address/prefix and gateway.」**没有这个分配，下面任何命令都不要执行**（猜地址不通且可能冲突）。

### Hub 侧配置（拿到分配后，`<ADDR>`/`<GW>` 替换为实际值）

1. 备份：`cp /etc/network/interfaces /etc/network/interfaces.bak-20260612-ipv6`。
2. 在 `iface eth0` 段后追加 v6：
   ```
   iface eth0 inet6 static
       address <ADDR>          # 如 2xxx:...::2
       netmask 64
       gateway <GW>            # 网关若是 link-local 需带 %eth0
   ```
   网关为 link-local 时 ifupdown 可能需改用 `post-up ip -6 route add default via <GW> dev eth0`。
3. 生效：`ifdown eth0 && ifup eth0`（**有断网风险，用面板 VNC/串口兜底，勿纯 SSH 执行**），或更安全地先 `ip -6 addr add <ADDR> dev eth0 && ip -6 route add default via <GW> dev eth0` 临时验证，确认连通再落盘。
4. 验证 Hub 自身 v6 出网：`ping6 -c2 2606:4700:4700::1111`、`curl -6 https://api6.ipify.org`。
5. 防火墙：放行 v6 入站隧道端口。现网用 nftables/iptables（**先 `nft list ruleset` 看实际，本计划勿假设 UFW**）；至少放行 `tcp dport 39093`（QUIC 若上则加 `udp dport 39094`），wg0 上的 `18081` 已是内网无需动。
6. DNS：给 `jp-proxy.ruichao.dev` 加 **AAAA** 记录指向 `<ADDR>`（手机用域名连时才需要）。

### 手机侧切换（Hub v6 通了之后）

- `client.yaml` `server:` 改成 `[<ADDR>]:39093` 或 `jp-proxy.ruichao.dev:39093`（带 AAAA）；备份 `client.yaml.bak-20260612-v6tunnel`。
- 重启 client，Hub 侧确认 `reverse tcp client connected from [240b:...]`（来源变成手机 v6 而非 `210.157.x` v4）。
- 验证隧道稳定性：撑 30min 看掉线次数对比 v4 基线（今天 57 次/天）。
- 回滚：`server:` 改回 `36.50.84.68:39093`，零成本。

### 与 QUIC（第 3 项）的关系

两者解决**同一个隧道腿问题**，但路径不同：

- **IPv6** 绕开的是 CGNAT v4 流表，仍是 TCP；**依赖机房分配**（外部卡点）。
- **QUIC over UDP** 绕开的是 F5/CGNAT 对 TCP 的处理（RST 注入对 UDP 无效），**不依赖机房**、代码已就绪。

建议明天**两条并行**：IPv6 等运维查面板分配（可能要工单等），QUIC 当场就能起第二实例 A/B（见第 3 项），哪条先验证通就先上哪条。理想终态是 **QUIC over IPv6** 叠加。

---

## 已知坑备忘（操作时照抄）

- **pkill 自匹配**：`pkill -f` 的模式若含 `zhreverse` 路径字符串，会把自己的 `su/sh` 命令行也杀掉（命令参数里有路径）。优先 `ps` 拿 PID 后 `kill <pid>`。
- **Text file busy**：运行中的二进制不能 `cp` 覆盖；先 `mv` 旧档改名，再 `cp` 新档进位，最后 kill 旧进程让 supervisor 拉起。
- **adb push 不带执行位**：push 后必须 `chmod 755`。
- **Git Bash 路径转换**：adb 的设备路径要 `MSYS_NO_PATHCONV=1`，否则 `/data/...` 被改写成 Windows 路径。
- **测速方法论**：80/443 的测速全部隔着 F5，只能代表"目标腿"；测隧道腿/裸路径用非标准端口（如 tcpbin.com:4242）。工具 `tools/cellbench`（`AF=4|6`、`PIN_IP=<ip>`），手机上现成：`/data/local/tmp/cellbench`。
- **root shell 裸 socket 不通**：Android 按 fwmark 分表路由，shell 里 ping 要 `-I rmnet1`，且乐天路径 ICMP 本身被过滤，用 TCP 探测代替。
