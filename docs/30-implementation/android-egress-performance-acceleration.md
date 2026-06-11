# Android 出口极致加速研究

## 结论摘要

当前 Android 出口的速度瓶颈不是单点问题。最新 A/B 结果显示,Android 本机网络能力与 Android -> WireGuard -> Hub/Mac 回传能力必须分开看:

- Android 本机前台 Fast.com 可跑出下行 `80+ Mbps`、上行 `10-20+ Mbps`。
- Android 本机 Wi-Fi 直连 Cloudflare 可跑出上下行 `50+ Mbps`。
- 但 Mac 经 Android SOCKS 出口,即使 Android 已切到 Wi-Fi,总吞吐仍约 `17-21 Mbps`。
- 反向 TCP/yamux Phase 0 通道在同一 Android Wi-Fi 下可达单流 `44 Mbps`、8 并发 `50 Mbps`。
- au 蜂窝状态下还会叠加小区上行、目标站点和调度波动,因此可能掉到 `~1 Mbps` 级别。

因此优化方向应从“只责怪 au 蜂窝上行”升级为两层:先提高 WireGuard/SOCKS 回传路径的稳定上限,再针对蜂窝侧做位置、设备、运营商和前台/后台 UID 路径优化。

最新反向通道结论:不需要自己实现 WireGuard。更高收益的路线是保留官方 WireGuard App 作为管理内网/控制面,把大流量出口数据面迁移到 Android 主动拨出的反向高速通道。初版 TCP/yamux 已证明绕开 WG App 数据面后可在 Wi-Fi 下突破 `17-21 Mbps` 上限;但在室内 au 蜂窝下出现 keepalive timeout、CONNECT 503 和 TCP-over-TCP 队头阻塞。Phase 1 QUIC 能建立 UDP 会话并避免快速掉线,但在当前室内蜂窝下单流仍只有 `0.026-0.26 Mbps`,4 并发约 `1.07 Mbps`,未超过 Android 本机直连 `1.1-1.6 Mbps`。Phase 2 多 QUIC connection 连接池让 2MB 单流三次完整完成,约 `0.55-0.63 Mbps`,但 4/8 并发总吞吐仍约 `1.07 Mbps`。Phase 3 新增 Android 侧 `/fetch` 应用层直拉,修复了 root 环境 DNS 与 CA 根证书问题,2MB 三次完整成功约 `0.53-0.70 Mbps`;同一时刻 Android 裸机下行 Cloudflare 为 `3.2-4.2 Mbps`,但 Android 直接 POST 2MB 到 Hub 只有 `0.42-0.55 Mbps`。因此当前室内 au 上限被锁定在 Android -> Hub 回传上行路径,不是 CONNECT/FETCH 实现本身。下一步应从透明代理测速转向移动弱网可恢复分块调度、多出口/多手机聚合、就近 Hub/入口切换和快速出口切换。

本轮实时采样:

- 运营商: Rakuten LTE。
- Serving cell: Band 3, 20MHz。
- 信号质量: RSRP 约 `-101 dBm`, RSRQ 约 `-15 dB`, SNR `3`。
- Android 系统估算: `LinkUpBandwidth >= 5794 Kbps`, `LinkDnBandwidth >= 9808 Kbps`。
- 手机本机 20MB Cloudflare 单流下载:
  - `303799 B/s`, `265683 B/s`, `182638 B/s`
  - 折算约 `1.46-2.43 Mbps`。
- 手机本机 Cloudflare 上传粗测:
  - `258390 B/s`, `29955 B/s`, `43064 B/s`
  - 折算约 `0.24-2.07 Mbps`,且波动很大。
- Hub 经 Android `10.66.0.101:1080` 出口 20MB 单流:
  - `114843 B/s`, `249566 B/s`, `154 B/s`
  - 折算约 `0.001-2.0 Mbps`。
- Hub 经 Android 出口 8 并发流:
  - 聚合仅约 `0.01 Mbps`,基本没有并发聚合收益。
- 手机温度/供电:
  - AC powered, battery 100%,主要 thermal zone 约 `39-43 C`。
  - 未看到热失控或供电不足证据。

因此当前“极致加速”的最高收益路径不是继续微调 MTU,而是先提升无线侧基线:换卡/换小区/换设备/换承载方式。

## 现有链路

```text
公网目标
  -> Android 蜂窝下行
  -> zhandroid-egress mixed proxy
  -> WireGuard App tun0
  -> Android 蜂窝上行
  -> Hub wg0
  -> 中国客户端
```

对用户而言是“下载”,但对 Android 手机而言,关键一段是把数据上传回 Hub。因此手机卡测速 App 的下行快,不等于出口快。

## 当前已经排除/弱化的方向

### 1. Hub 配置

Hub 当前:

- Android peer 握手新鲜。
- `10.66.0.101/32` route MTU 为 `1120`。
- TCPMSS 为 `1080`。
- Mac 出口可正常跑高于 Android 的吞吐。

Hub 不是当前主瓶颈。

### 2. 手机 CPU / 供电 / 温度

实时采样中:

- `zhandroid-egress` 常驻,内存占用很小。
- 温度约 `39-43 C`。
- AC 供电,电池 100%。

当前瓶颈不优先怀疑 CPU/发热/供电。

### 3. 单流 TCP

如果只是单流 TCP 慢,8 并发通常能提高聚合吞吐。但本轮 8 并发几乎归零,说明无线侧或运营商调度状态已经很差,不是简单“多开连接”能解决。

## 加速路线优先级

### P0: 运营商/小区侧基线拉高

1. 同位置切回 UQ/au 卡复测。
   - 历史记录中 UQ 在本机下行曾到约 `50 Mbps`。
   - 当前 Rakuten serving SNR 低,且系统估算上行只有约 `5.8 Mbps`。
2. 每次测速前记录:
   - operator/APN。
   - Band / EARFCN / PCI。
   - RSRP / RSRQ / SNR。
   - 是否 CA。
   - 手机公网 IP。
3. 慢速时触发 `rotate-ip.sh` 飞行模式重注册。
   - 它不只是换 IP,也会重选小区和刷新蜂窝 NAT/UDP 状态。
4. 固定位置做 10 轮分布测试,记录中位数和 P90,不要只看单次峰值。

### P1: 把无线自愈做成策略

当前 watchdog 已支持:

- `tun0` 缺失 -> WireGuard App `SET_TUNNEL_UP`。
- `tun0` 存在但 Hub 不通 -> `SET_TUNNEL_DOWN` 后 `SET_TUNNEL_UP`。

下一步可做:

- 连续 N 次 DOWN/UP 后仍失败 -> 自动触发 `rotate-ip.sh`。
- 冷却至少 `10-15 min`,避免频繁断网。
- 每次自愈前后写入信号、IP、握手、测速摘要。

### P2: 承载协议实验

WireGuard 官方定位是高性能 UDP 隧道,但当前问题发生在 Android 蜂窝网络和 WireGuard App/UDP 路径上。可实验 QUIC 类承载:

- Hysteria2:
  - 基于 QUIC,设计目标包含 TCP/UDP proxy、速度和抗干扰。
  - 官方协议文档说明其运行在 QUIC + unreliable datagram 上。
  - sing-box 已支持 Hysteria2 inbound/outbound。
- TUIC:
  - 同样是 QUIC 类路线,可作为备选。

实验目标不是立刻替换全系统,而是回答一个问题:

> 在同一张卡、同一位置、同一时间,QUIC/Hysteria2 承载是否比 WireGuard App external 模式在抖动蜂窝链路上更稳?

最小实验:

```text
Hub: hysteria2 server on UDP/443
Android: sing-box/hysteria2 client or official client
Hub: 暴露 Android egress proxy over QUIC tunnel
```

注意:如果无线本身只有 2 Mbps,任何协议都不可能变成 50 Mbps;协议实验只能减少额外损耗、改善弱网恢复和拥塞控制。

### P3: 硬件形态升级

如果目标是长期稳定高速,优先级应高于继续榨这台 Moto:

1. Rakuten 认证 Android 机对照。
2. 支持更好频段/CA/5G 的手机。
3. OpenWrt 4G/5G 路由器或 USB modem。
4. 工业蜂窝路由器。

原因:出口节点本质是网络设备,手机 App/Doze/厂商系统/充电策略都不是长期高 SLA 形态。

## 建议的下一轮实验矩阵

| 变量 | 值 |
| --- | --- |
| SIM | Rakuten / UQ |
| 位置 | 当前固定位置 |
| 承载 | WireGuard App external |
| 测试 | phone down, phone up, Hub via Android down, 8-stream down |
| 轮数 | 每组 10 轮 |
| 记录 | IP, Band, PCI, RSRP, RSRQ, SNR, CA, median, min, max |

通过这个矩阵先确认“哪张卡/哪个小区”有资格进入协议优化阶段。

## 2026-06-08 au/UQ 切卡复测

切回 au/UQ 后,第一次状态不稳:

- APN 已变为 `uqmobile.jp`。
- 手机公网 IP 一开始取不到。
- 手机无法 ping Hub 公网 `36.50.84.68`。
- WireGuard App 日志持续 `Handshake did not complete`。
- Hub 无法访问 `10.66.0.101:1080/2022`。

执行 `rotate-ip.sh 12` 飞行模式重注册后恢复:

- 手机公网 IP: `59.132.5.127`。
- 手机可 ping Hub 公网。
- 手机可 ping WG 内网 `10.66.0.1`。
- Hub 可通过 Android 代理拿到出口 IP `59.132.5.127`。

复测时 serving cell:

- 运营商: KDDI / UQ。
- Band 1, EARFCN 100, 20MHz。
- RSRP 约 `-117 dBm`。
- RSRQ 约 `-15 dB`。
- SNR `-4`。
- CA: false。

测试结果:

| 测试 | run1 | run2 | run3 | 判断 |
| --- | ---: | ---: | ---: | --- |
| 手机本机下载 20MB | `1,246,881 B/s` (`9.98 Mbps`) | `1,113,729 B/s` (`8.91 Mbps`) | `1,518,926 B/s` (`12.15 Mbps`) | au 下行明显优于当前 Rakuten |
| 手机本机上传 10MB 粗测 | `20,596 B/s` (`0.16 Mbps`) | `52,426 B/s` (`0.42 Mbps`) | `46,808 B/s` (`0.37 Mbps`) | 上行极差,且全部超时 |
| Hub 经 Android 出口下载 20MB | `4,581 B/s` (`0.04 Mbps`) | `62,069 B/s` (`0.50 Mbps`) | `66,648 B/s` (`0.53 Mbps`) | 出口下载被手机上行/高延迟拖住 |
| Hub 经 Android 出口 8 并发 5MB | 聚合 `75,394 B/s` (`0.60 Mbps`) |  |  | 并发有少量改善,但远低于手机本机下行 |

本轮 au/UQ 结论:

- au/UQ 在当前位置的下行基线比当前 Rakuten 好很多。
- 但 serving cell 质量仍然很差: RSRP `-117`, SNR `-4`,Hub 到 Android ping 曾到 `1.3-1.5s`。
- Android 作为出口时关键瓶颈仍是手机上行回 Hub;当前 au/UQ 上行粗测只有 `0.16-0.42 Mbps`。
- 继续调 MTU/代理栈不会把 `0.4 Mbps` 上行变成高速出口。要提速,必须换更好的小区/位置/设备/天线/运营商状态。
- 当前最有价值的下一步是移动手机位置或继续重选小区,直到 SNR 明显改善,再重复同一矩阵。

## 2026-06-08 Rakuten + iPhone USB 交叉验证

为排除 Android 设备本身因素,使用 Rakuten SIM + iPhone 通过 USB 连接 Mac mini 做同位置交叉验证。

Mac 侧发现一个独立问题:

- macOS 识别到 `iPhone USB` 服务,设备为 `en8`。
- 初始状态 `en8` 为 `inactive`,无 IPv4 地址。
- 管理员执行 `ifconfig en8 up` 后,接口变为 `UP/RUNNING/active`,但仍未获得 DHCP 租约。
- 手动配置 `172.20.10.2/28` 后,可 ping 通 iPhone 热点网关 `172.20.10.1`,延迟约 `0.8-1.2 ms`。
- 添加临时 host route 到 `1.1.1.1` 后可通过 iPhone NAT 出网,Cloudflare trace 显示出口 IP 为 `133.106.34.64`,位置 `JP/NRT`。

这说明 iPhone + Rakuten 的 USB 热点网关和 NAT 本身可用;本机失败点是 macOS 没有自动从 iPhone USB 获取 DHCP/默认路由。

在静态地址和临时 host route 下测速:

| 测试 | run1 | run2 | run3 | 判断 |
| --- | ---: | ---: | ---: | --- |
| iPhone USB 下载 20MB | `962,443 B/s` (`7.70 Mbps`) | `661,621 B/s` (`5.29 Mbps`) | `2,037,012 B/s` (`16.30 Mbps`) | 单流波动较大,但可用 |
| iPhone USB 上传 10MB | `164,025 B/s` (`1.31 Mbps`,仅上传约 6.5MB) | `269,285 B/s` (`2.15 Mbps`) | `264,502 B/s` (`2.12 Mbps`) | 上行明显强于 au/UQ Android 当前结果 |
| iPhone USB 到 `1.1.1.1` ping | min `35.4 ms`, avg `245.7 ms`, max `555.2 ms` |  |  | 移动链路抖动明显 |
| iPhone USB 8 并发下载 5MB | 聚合 `1,732,518 B/s` (`13.86 Mbps`) |  |  | 多连接能显著拉高吞吐 |

交叉验证结论:

- Rakuten + iPhone 在当前位置并非完全不可用;8 并发可达约 `13.86 Mbps`。
- 单流下载和 ping 抖动大,符合移动网络调度/弱网排队特征。
- 与 Android/Rakuten 当前结果相比,iPhone 形态明显更容易跑出可用吞吐,说明 Android 设备/系统/WireGuard App external 链路仍可能引入额外损耗。
- 但本机 iPhone USB 自动 DHCP 异常需要单独处理,不能把这次“插上没网”归因于手机或 SIM。
- 若后续用 iPhone 做长期对照,需要先修复 macOS 的 `iPhone USB` DHCP/路由自动化,或写一个显式的临时静态配置脚本。

### 同时连接拓扑澄清与即时复测

当前 Mac mini 同时连接两台手机:

- Android: au/UQ SIM,通过 USB ADB 管理,不是 Mac 的 USB 网络接口。
- iPhone: Rakuten SIM,通过 `iPhone USB/en8` 暴露 USB 热点接口。

Mac 侧接口映射:

- `iPhone USB` -> `en8`,MAC `16:35:b7:2f:1f:63`。
- Android `XT2201_2` -> ADB 设备 `ZY22FMLNFK`,当前未作为 Mac 网络服务出现。

同时间 Android/au 本机复测:

- 运营商: `KDDI / UQ mobile`。
- 手机公网 IP: `59.132.5.127`。
- Android 路由:蜂窝 `rmnet_data2`,WireGuard `tun0=10.66.0.101`。
- Android 到 `1.1.1.1` ping: min `59.9 ms`,avg `106.5 ms`,max `212.0 ms`。

Android/au 本机下载 20MB:

| run | 结果 |
| --- | ---: |
| 1 | 45 秒超时,下载 `8,588,433 bytes`,`190,856 B/s` (`1.53 Mbps`) |
| 2 | 45 秒超时,下载 `5,750,034 bytes`,`127,779 B/s` (`1.02 Mbps`) |
| 3 | 45 秒超时,下载 `4,158,541 bytes`,`92,409 B/s` (`0.74 Mbps`) |

补充判断:

- Android/au 与 iPhone/Rakuten 是两条不同路径,不能把 Mac 的 `en8` DHCP 问题混到 Android 出口问题里。
- Android/au 当前本机直连已明显低于前一次 au/UQ 复测的 `8.91-12.15 Mbps`,说明当前位置/时段/小区状态波动极大。
- 当前更可靠的判断是:先用同时间、同位置、多轮矩阵比较设备/运营商;单次结果不足以证明协议栈优化有效。

### Android/au 放阳台复测

Android/au 移到阳台后,内网/VPN 保持在线:

- Mac 到 Hub `10.66.0.1` ping: avg `14.6 ms`。
- Mac 到 Android `10.66.0.101` ping: avg `78.1 ms`。
- `10.66.0.101:1080` 和 `10.66.0.101:2022` 均可达。
- Android peer endpoint 仍为 `59.132.5.127`。

Android/au 信号与注册:

- 运营商: KDDI / UQ mobile。
- LTE,20MHz,no CA。
- ServiceState 主字段: EARFCN `100`,Band 1,RSRP `-113`,RSRQ `-15`,RSSNR `-2`。
- CellInfo 中注册小区显示 EARFCN `1300`,Band 3,RSRP `-117`,RSRQ `-16`;telephony 输出存在双字段/刷新不同步,但整体仍属于弱信号/低 SINR。

阳台后 Android 本机下载 20MB:

| run | 结果 |
| --- | ---: |
| 1 | `10,579,223 B/s` (`84.63 Mbps`) |
| 2 | `4,712,789 B/s` (`37.70 Mbps`) |
| 3 | `11,546,750 B/s` (`92.37 Mbps`) |

阳台后 Android 本机上传 10MB:

| run | 结果 |
| --- | ---: |
| 1 | 45 秒超时,上传 `6,422,528 bytes`,`142,721 B/s` (`1.14 Mbps`) |
| 2 | 45 秒超时,上传 `5,177,344 bytes`,`115,047 B/s` (`0.92 Mbps`) |
| 3 | 45 秒超时,上传 `5,439,488 bytes`,`120,874 B/s` (`0.97 Mbps`) |

阳台后 Hub/Mac 经 Android 出口下载 20MB:

| run | 结果 |
| --- | ---: |
| 1 | 45 秒超时,下载 `4,082,251 bytes`,`90,711 B/s` (`0.73 Mbps`) |
| 2 | 45 秒超时,下载 `5,074,360 bytes`,`112,761 B/s` (`0.90 Mbps`) |
| 3 | 45 秒超时,下载 `5,870,843 bytes`,`130,451 B/s` (`1.04 Mbps`) |

阳台复测结论:

- 阳台位置显著改善 Android/au 下行,本机下载可达 `37.7-92.4 Mbps`。
- 但上行仍只有约 `0.9-1.1 Mbps`,这与经 Android 出口下载 `0.7-1.0 Mbps` 完全匹配。
- 因此当前出口慢的核心瓶颈不是 Android 本机下行。Wi-Fi 环境下,瓶颈是 Android -> WireGuard -> Hub/Mac 的回传路径,当前总吞吐约 `17-21 Mbps`。
- au 蜂窝环境下,瓶颈是同一回传路径再叠加蜂窝上行/小区调度/目标站点差异。
- 当前没有证据支持 root 脚本、iptables、tc 或 Android 本机 Wi-Fi 把网络整体限死。

## 参考

- WireGuard performance notes: https://www.wireguard.com/performance/
- Hysteria2 protocol: https://www.hy2.io/docs/developers/Protocol/
- sing-box Hysteria2 inbound: https://sing-box.sagernet.org/configuration/inbound/hysteria2/
- Android always-on VPN: https://developer.android.com/work/dpc/network-telephony
- Android VPN API / always-on behavior: https://developer.android.google.cn/develop/connectivity/vpn?hl=en
