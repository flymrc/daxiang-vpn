# Android 出口打磨计划（2026-06-11 起实施）

## 背景

2026-06-10 审计定位根因（乐天 F5 BIG-IP 透明代理终结全部 80/443 TCP，v4 侧高故障）并完成第一轮改造：Hub `resolve: client` + 手机 `address_family: ipv6` + DNS v6 优先 + yamux 4MB 窗口/30s 写超时。经代理 20MB 从 100% 失败提升到 28.9 Mbps 零失败。详见 [2026-06-10-pixel-7a-speed-audit.md](../90-history/worklogs/2026-06-10-pixel-7a-speed-audit.md)。

本文是后续打磨项的实施计划，按"先小后大"排序。当前生产基线：commit `04070a7`，两端二进制/配置均有 `.bak-20260610` 备份。

## 执行顺序总览

| 序 | 项目 | 预估 | 前置 |
| --- | --- | --- | --- |
| 1 | 手机端 DNS 缓存 | ~1h | 无 |
| 2 | 健康检查对齐 v6 形态 | ~30min | 无 |
| 3 | QUIC over UDP A/B | ~半天 | 无 |
| 4 | `connections: 2` 复测 | ~30min | 建议在 3 决策后 |
| 5 | WireGuard 控制面迁移到 Pixel | ~半天 | 无（运维兜底，越早越安心） |
| 6 | Hub VPS 启用 IPv6 | 待调研 | 若 3 切 QUIC 可降级 |

---

## 1. 手机端 DNS 缓存

**动机**：`resolve: client` 后每个 CONNECT 都做一次 DNS（v6 UDP 约 20-60ms，偶发更慢），浏览器并发开页时放大明显。

**方案**（`egress/reverse/main.go` 客户端侧）：

- `dialTarget` 解析前查进程内缓存：`map[string]dnsCacheEntry{ips []net.IPAddr, expires time.Time}` + `sync.Mutex`。
- TTL 60s；条目上限 ~1024（满了整体清空即可，不必上 LRU）；解析失败不缓存（或 5s 负缓存，可选）。
- 缓存存原始 `ips`，`orderIPs` 仍在拨号时排序（地址族偏好可能变）。
- FETCH 路径共用 `dialTarget`，自动受益。

**验收**：`go test ./egress/reverse/`（补一个缓存命中/过期单测）；同域名连续 CONNECT 第二次起无 DNS 延迟。

**部署**：重编 arm64 → 手机替换 → supervisor 拉起（流程见文末"已知坑"）。

## 2. 健康检查对齐 v6 形态

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

## 4. `connections: 2` 复测

之前第二条 session 连不上大概率是当时 v4 路径抖动，环境已变。若第 3 项切了 QUIC，直接在 QUIC 上测。

- 改 `client.yaml` `connections: 2`，重启 client，Hub 确认两条 session 都建立且保持。
- 跑同一速度矩阵对比 `connections: 1`；并发 8 流小文件测试看尾延迟。
- 任一条频繁断连 → 回滚到 1。改动只在手机配置，回滚成本为零。

## 5. WireGuard 控制面迁移到 Pixel

**状态（2026-06-11）**：已完成。Pixel 7a 已接管 `10.66.0.101` WireGuard 控制面，Hub 可 SSH 到 `10.66.0.101:2022`，TCP ADB 已限制在 `10.66.0.0/24 -> 10.66.0.101:5555`，watchdog 的 WireGuard UP 与 DOWN-wait-UP 自愈均已演练通过。

**动机**：Pixel 上 `10.66.0.101` 控制面未迁移，zhandroid-control/watchdog 自愈缺位，人不在手机旁出问题只能干等。数据面已稳，这是当前最大的运维风险敞口。

**步骤**（参照 [2026-06-08-android-wireguard-self-heal.md](../90-history/worklogs/2026-06-08-android-wireguard-self-heal.md) 与 [android-egress-agent.md](./android-egress-agent.md)）：

1. 已安装 WireGuard App `1.0.20260315` 并导入 `jp-android-01.conf`（10.66.0.101，Hub peer 已替换为 Pixel 新公钥）。
2. 已开启 WireGuard App “授权外部控制”，watchdog 可通过 root broadcast 拉起 `jp-android-01`。
3. 已部署 zhandroid-control（`10.66.0.101:2022`）+ watchdog + WG-only TCP ADB 脚本到 `/data/adb/`。
4. 已验收：Hub `ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101` 通；Hub ping 通；watchdog UP 与 DOWN-wait-UP 自愈演练通过。
5. 已更新 `server-access.md` / `diagnostics.md` 并新增 2026-06-11 工作日志。

## 6. Hub VPS 启用 IPv6（待调研）

- 查 VPS 商是否提供 IPv6；若有：netplan 配置 → `jp-proxy.ruichao.dev` 加 AAAA → UFW v6 规则（39093/39094、18081 wg0）→ 手机 `client.server` 改域名或 v6 字面量。
- 价值：隧道腿脱离 CGNAT v4 流表（偶发 dial timeout 之源）。**若 QUIC 已切生产，此项降级为"有空再做"。**

---

## 已知坑备忘（操作时照抄）

- **pkill 自匹配**：`pkill -f` 的模式若含 `zhreverse` 路径字符串，会把自己的 `su/sh` 命令行也杀掉（命令参数里有路径）。优先 `ps` 拿 PID 后 `kill <pid>`。
- **Text file busy**：运行中的二进制不能 `cp` 覆盖；先 `mv` 旧档改名，再 `cp` 新档进位，最后 kill 旧进程让 supervisor 拉起。
- **adb push 不带执行位**：push 后必须 `chmod 755`。
- **Git Bash 路径转换**：adb 的设备路径要 `MSYS_NO_PATHCONV=1`，否则 `/data/...` 被改写成 Windows 路径。
- **测速方法论**：80/443 的测速全部隔着 F5，只能代表"目标腿"；测隧道腿/裸路径用非标准端口（如 tcpbin.com:4242）。工具 `tools/cellbench`（`AF=4|6`、`PIN_IP=<ip>`），手机上现成：`/data/local/tmp/cellbench`。
- **root shell 裸 socket 不通**：Android 按 fwmark 分表路由，shell 里 ping 要 `-I rmnet1`，且乐天路径 ICMP 本身被过滤，用 TCP 探测代替。
