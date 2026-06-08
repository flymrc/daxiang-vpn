# Android 出口极致加速研究

## 结论摘要

当前 Android 出口的速度瓶颈首先不是 Hub、CPU、供电或 `dxandroid-egress` 代理进程,而是 Android 蜂窝无线链路本身,尤其是手机作为出口时的数据方向需要走蜂窝上行回 Hub。

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
  -> dxandroid-egress mixed proxy
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

- `dxandroid-egress` 常驻,内存占用很小。
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

## 参考

- WireGuard performance notes: https://www.wireguard.com/performance/
- Hysteria2 protocol: https://www.hy2.io/docs/developers/Protocol/
- sing-box Hysteria2 inbound: https://sing-box.sagernet.org/configuration/inbound/hysteria2/
- Android always-on VPN: https://developer.android.com/work/dpc/network-telephony
- Android VPN API / always-on behavior: https://developer.android.google.cn/develop/connectivity/vpn?hl=en
