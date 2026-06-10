# 2026-06-10 Pixel 7a 出口低速根因审计

## 背景

换 Pixel 7a 后信号显示变好（满格），但经 `dxreverse` 反向出口的代理速度仍然只有 ~1-4 Mbps，且大文件经常 `SSL_ERROR_SYSCALL` 失败。本次审计目标：确认是无线侧问题还是 VPN/Hub 设计问题。

## 方法

新增 `tools/cellbench`（Go，交叉编译 linux/arm64，与 dxreverse 同方式在 Magisk root 下运行，部署在手机 `/data/local/tmp/cellbench`），支持：

- `cellbench rtt <host:port> <n>`：TCP 建连 RTT。
- `cellbench down <url> [stallSec]`：带卡死检测的下载测速。
- `cellbench up <url> <MB> <n>`：POST 上传测速。
- 环境变量 `AF=4|6` 钉死地址族，`PIN_IP=<ip>` 跳过 DNS 直连指定 IP（保留 SNI）。

对照测试矩阵：手机↔Hub(36.50.84.68:80, librespeed)、手机↔Cloudflare(443)、PC↔Hub(80)，分别钉 IPv4/IPv6。

## 当时蜂窝状态

- Rakuten LTE Band 3、EARFCN 1500、20MHz、CA on。
- RSRP `-84 ~ -90`（level=4 满格）、RSRQ `-13 ~ -15`、**RSSNR `-1 ~ +1`**、CQI 9。
- rmnet1 双栈：IPv4 `10.20.212.249/32`（CGNAT 私网）+ 全局 IPv6 `240b:...`。MTU 1440。
- 注意：**格数只反映 RSRP，吞吐跟 SNR/RSRQ 走**。换机后 RSRP 变好（下行确实快了），SNR 仍 ≈0 dB。

## 测试结果

### 手机 → Cloudflare 443（钉 IP，同一时刻同一小区）

| 路径 | 下载 10MB | 上传 4MB x2 | 上传 8MB x2 |
| --- | --- | --- | --- |
| **IPv6 原生** | 13.9 Mbps 干净完成 | **17.3 / 16.5 Mbps 全 200** | **19.9 / 23.3 Mbps 全 200** |
| **IPv4 CGNAT** | 54.3 Mbps 干净完成 | **两次全部被 RST 掐死**（~15s 后 connection reset） | 未测 |

IPv6 TCP RTT 27-63ms。

### 手机 → Hub 36.50.84.68:80（仅 IPv4，Hub 无 AAAA）

- TCP 建连 RTT：基线 60-90ms，但间歇飙到 0.65s / 1.5s。
- 下载 2MB x4：7.2-16 Mbps 正常；**20MB 单流中途彻底僵死**（蜂窝口 18s+ 零字节，连接仍 ESTABLISHED）。
- 上传 4MB x9：**6 次约 1s 内被 RST**，3 次成功（9.3 / 13.1 / 27.1 Mbps——说明上行射频容量本身够）。
- DNS（UDP/53 → 1.1.1.1，IPv4）间歇性超时。

### PC → Hub 80（非乐天网络，对照）

- 下载 20MB：62.7 Mbps；上传 4MB x3：全 200，~46 Mbps。**Hub 本身完全健康。**

### dxreverse 客户端日志（39093 隧道口）

- 隧道空闲时稳定在线（keepalive 正常）。
- 启动期多次 `dial tcp 36.50.84.68:39093: i/o timeout`；`connections: 2` 时第二条几乎连不上——与 IPv4 路径掐流为同一现象。

## 根因结论

1. **主因：乐天 IPv4/CGNAT 路径会系统性掐持续上行流**（RST 注入或 CGNAT 策略），原生 IPv6 路径完全干净（上行 16-23 Mbps）。`dxreverse` 隧道（TCP/39093）目前 100% 跑在 IPv4 上，而本架构里**用户下载的每个字节都要经手机上行回传 Hub**——正好踩死在被掐的方向上。
2. **"信号变好了"是真的，但与代理速度无关**：RSRP 改善 → 下行 54-66 Mbps；代理速度受限于上行路径，上行在 IPv4 上被掐、SNR 也没变好。手机上直接跑测速 App 走的是 IPv6 → 显得很快；隧道走 IPv4 → 慢且断，两者并不矛盾。
3. **今天 worklog 里 3.73 Mbps 的孤例成功**≈ IPv4 路径两次掐流之间能挤出的吞吐；`SSL_ERROR_SYSCALL` = 隧道 TCP 被 RST 后 yamux 全部流瞬间陪葬。
4. **Hub 与反向隧道拓扑设计本身没有大错**（PC 对照证明 Hub 健康），但有以下放大器：
   - `connections: 1` 单 TCP 承载全部 yamux 流：一次 RST = 全体客户端连接同死。
   - yamux v0.1.2 默认 `MaxStreamWindowSize=256KB`：RTT 飙到 0.5-1.5s 时单流上限只有 1.4-4 Mbps；默认 `ConnectionWriteTimeout=10s` + `KeepAliveInterval=10s`，缓冲膨胀时会话自杀。
   - TCP-over-TCP 队头阻塞 + 双层拥塞控制。
   - `resolve: server` 强制 IPv4 解析目标 → 手机拨目标也全走 CGNAT v4。

## 行动建议（按优先级）

- **P0：把隧道搬上 IPv6。** 给 Hub VPS 启用 IPv6（或换支持 v6 的入口/中转），`client.server` 改 v6 字面量或 AAAA 主机名。预期用户侧下载从 ~1-4 Mbps 提到 **~15-20 Mbps** 且大幅减少中途死流。
- **P0b：复测 QUIC 传输（代码已支持）。** RST 注入掐不死 UDP 流；QUIC 配置里窗口已是 4-16MB。之前 QUIC 测试差是在旧手机+烂小区下做的，不能作为否决依据。QUIC over IPv6 是终态组合。
- **P1：隧道鲁棒性。** `connections: 2-3` 分散单点 RST 风险；调大 yamux `MaxStreamWindowSize`（如 4MB）和 `ConnectionWriteTimeout`；目标解析改 `resolve: client` + 手机 `address_family: ipv6`，让目标连接也走 v6。
- **P2：监控口径修正。** 出口健康检查应测"手机上行到 Hub"而不是下行/格数；信号记录以 SNR/RSRQ 为主。

## 工具留存

- `tools/cellbench/`（源码）、`dist/cellbench-linux-arm64`、手机 `/data/local/tmp/cellbench`。

---

## 重要修正（同日二次取证，推翻上文部分结论）

用户对"乐天掐 IPv4 持续上行流"提出质疑。用 tcpdump 在 rmnet1 抓包 + v4/v6 交错上传矩阵复核，原结论**不成立**，真实机制如下。

### 取证证据（pcap：手机 `/data/local/tmp/audit.pcap`）

1. **乐天对 80/443 端口的 TCP 做 F5 BIG-IP 透明全代理（TCP 终结），v4 和 v6 都被代理，与目标无关。**
   - 到 Hub:80 和到 Cloudflare:443 的 SYN-ACK 指纹完全相同：`win 41600, mss 1300, wscale 9`，v4 TTL 250 / v6 hlim 59（初始 255/64，仅 ~5 跳）。
   - 决定性证据：跨 v4/v6、跨不同"服务器"的 TCP Timestamp 是**同一个单调时钟**（2983478615 → 2983481029 → 2983496389 → 2983518059，~1000 tick/s）——同一个 TCP 协议栈。
2. **故障 RST 全部带 F5 诊断签名**，两种模式：
   - `BIG-IP: [0x2664c65:2574] Policy action`：TLS ClientHello 被 ACK 后永远等不到 ServerHello（上游/策略失败），连接死挂；Go 默认 15s TCP keepalive 探测触发该 RST → 之前"精确 15.3s 失败"的来源。
   - `BIG-IP: [0x2664c65:2574] No flow found for ACK`：**传输中途流表状态丢失**（hub 上传在 ~512KB 处死亡；20MB 下载僵死同机制）。
   - 故障**不分上下行**（20MB 下载也死），也**非确定性**（同窗口 hub:80 v4 上传 2/3 成功 12-13 Mbps）。
3. **同一时段 v6:443 经同一台 F5 全部成功**（4MB/8MB 上传 16-23 Mbps，3 轮交错零失败）。"优先 v6"的经验结论保留，但机制不是"绕开 CGNAT"，而是 F5 的 v6 侧当前健康、v4 侧高故障率。
4. **非标准端口不被 F5 终结，走裸 CGNAT 路径**：
   - 39093（dxreverse 隧道）入站 TTL=47（真实 Hub Linux 栈）→ **隧道本身不经 F5 代理**。
   - tcpbin.com:4242（美国 Linode）v4 2MB 双向回显干净跑完（真实指纹 win 28960/wscale 7、RTT 230ms）。

### 对生产故障的重新解释

经 Hub 代理下载 HTTPS = 隧道腿（39093，不经 F5，基本健康）+ **手机拨目标腿（443，必经 F5）**。`resolve: server` 强制目标腿走 v4 —— 正好撞上 F5 的 v4 高故障侧。**`SSL_ERROR_SYSCALL` 的主要来源很可能是目标腿被 F5 毙掉，而非隧道腿。** 隧道腿残留问题（偶发 dial i/o timeout、第二条 session 连不上）与 CGNAT/F5 流表状态相关。

### 修正后的行动优先级

- **P0（零 Hub 架构改动）：目标腿切 v6。** Hub `server.yaml` 改 `resolve: client`，手机 `client.yaml` 改 `address_family: ipv6`。目标连接落到 F5 健康的 v6 侧（实测 443 上行 16-23 Mbps）。
- **P0b：客户端 DNS 改走 v6**（代码硬编码 `1.1.1.1:53` UDP/v4，实测偶发超时；`resolve: client` 后此路径变关键）。
- **P1：隧道承载换 QUIC/UDP（并争取 Hub IPv6）**，消除 CGNAT TCP 流表对长连接的威胁；yamux 窗口/超时调优、`connections: 2` 作为韧性兜底。
- 测速方法论：今后对 80/443 的测速结果都隔着 F5，**不能**用来推断隧道路径；测隧道路径要用非标准端口。

---

## 当日执行记录（P0 + P0b + 手机侧 P1）

### 已部署

1. **Hub 配置**：`/etc/daxiang/dxreverse/server.yaml` `resolve: server -> client`（备份 `server.yaml.bak-20260610`），`systemctl restart dxreverse-hub.service`。
2. **手机配置**：`/data/adb/dxreverse/client.yaml` `address_family: auto -> ipv6`（备份 `client.yaml.bak-20260610`）。
3. **`egress/reverse` 代码改动**（手机端二进制已部署，备份 `bin/dxreverse.bak-20260610`）：
   - `publicResolver()`：DNS 服务器顺序改为 v6 优先（`2606:4700:4700::1111` → `1.1.1.1` → `2001:4860:4860::8888` → `8.8.8.8`）。Rakuten v4 UDP/53 实测间歇丢包。
   - `dialTarget()`：DNS（6s）与拨号（每地址 6s、最多 2 个地址）预算分离。原实现 DNS+全部拨号共享 10s ctx，DNS 慢时直接挤死拨号。
   - yamux 双端 `MaxStreamWindowSize 256KB -> 4MB`、`ConnectionWriteTimeout 10s -> 30s`（**Hub 端二进制尚未部署**，见下）。
   - 测试与示例配置同步（`connections: 1`、`address_family: ipv6`）。

### 部署后验证（Hub 经 10.66.0.1:18081，目标 Cloudflare）

| 测试 | 改造前 | 改造后 |
| --- | --- | --- |
| 出口地址 | v4 `133.106.34.62` | **v6 `240b:c010:421:d18c:0:42:e654:1701`** |
| 2MB x5 | 1/5 成功，最好 3.73 Mbps | **5/5 成功，4.0-10.4 Mbps** |
| 10MB | 全失败（SSL_ERROR_SYSCALL） | **2/2 成功，15.4 / 16.6 Mbps** |
| v4-only 目标（api.ipify.org） | 失败 | 3/3 成功（~0.5s） |

10MB 吞吐已贴近裸 v6 上行实测天花板（16-23 Mbps）。

### Hub 端二进制部署（同日完成）

用户确认后部署 `dist/reverse/dxreverse-linux-amd64` 到 `/opt/daxiang/dxreverse/dxreverse`（备份 `dxreverse.bak-20260610`），重启服务，隧道自动重连。

**双端部署后最终矩阵**（Hub 经 10.66.0.1:18081 → Cloudflare）：

| 测试 | 结果 |
| --- | --- |
| 2MB x3 | 全 200，3.8 / 13.4 / 15.0 Mbps |
| 10MB x2 | 全 200，**20.0 / 25.3 Mbps** |
| 20MB | 200，**28.9 Mbps**（5.8s；上午同测试 100% 失败） |

当日总变化：~1-4 Mbps + 高失败率 → **20-29 Mbps 零失败**。

### 注意事项与后续

- v4-only 目标现在走手机 v4 直拨（经 F5），小请求正常；大流量 v4-only 站点仍可能受 F5 v4 侧影响。主流大流量站点基本都有 v6。
- 出口 IP 形态变化：v6 站点看到手机 IPv6（仍是乐天移动网段），v4-only 站点看到 CGNAT v4 `133.106.x`。健康检查脚本如断言 v4 出口 IP 需同步调整。
- 回滚路径：两端配置和二进制都有 `.bak-20260610` 备份；Hub `resolve` 改回 `server`、手机 `address_family` 改回 `auto` 即恢复原行为。
- 后续可选：QUIC over v6（终态承载）、`connections: 2` 韧性、Hub VPS 启用 IPv6 让隧道腿也上 v6。
