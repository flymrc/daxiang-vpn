# 2026-06-08 工作记录：Android WireGuard external 模式自愈

## 背景

Hub 侧只读检查发现 Android 出口 `10.66.0.101` 当前不可达：

- `10.66.0.101:1080` 代理超时。
- `10.66.0.101:2022` 控制面超时。
- Android peer 握手不新鲜。
- Hub 侧 Android 专用 route MTU `1120`、TCPMSS `1080` 和 `ip_forward=1` 仍正常。
- Mac 出口对照可用。

因此这次问题更像 WireGuard App 隧道或手机网络离线,不是 Hub 配置或单纯吞吐低。

## 本次改动

给 `egress/android-control/watchdog.sh` 增加 external 模式下的 WireGuard App 隧道自愈：

- 检查本机是否存在 `10.66.0.101` 地址。
- 检查 Hub 内网 `10.66.0.1` 是否可 ping。
- 地址缺失时,最多每 120 秒发送一次 WireGuard Android `SET_TUNNEL_UP` broadcast intent。
- 地址存在但 Hub 内网 ping 失败时,先发送 `SET_TUNNEL_DOWN`,等待 3 秒后再发送 `SET_TUNNEL_UP`,强制 WireGuard App 重拨：

```sh
am broadcast \
  -a com.wireguard.android.action.SET_TUNNEL_UP \
  -n 'com.wireguard.android/.model.TunnelManager$IntentReceiver' \
  -e tunnel jp-android-01
```

WireGuard Android 官方 `TunnelManager.IntentReceiver` 会读取 extra `tunnel` 并把对应 tunnel 设置为 `UP`。

## 边界

- 手机无网、关机、没电时无法自愈。
- 若 Android 后台策略或 WireGuard App 权限阻止 intent 生效,仍需要 ADB/物理接触。
- 若后续实测 intent 自愈不稳定,下一步评估 root 直管内核 WireGuard / `wg-quick`。

## 验证

- `go test ./...` 在改动前已跑通,确认当前 Go 基线健康。
- `sh -n egress/android-control/watchdog.sh` 通过。
- `git diff --check` 通过。
- `go test ./...` 通过。
- ADB 部署新 watchdog 到 `/data/adb/dxandroid/watchdog.sh`,权限为 `root:root 700`。
- 手动触发 WireGuard App DOWN/UP 后,WireGuard App 日志显示仍在发 handshake 但未完成,说明 intent 生效但当时蜂窝 UDP/NAT 状态仍异常。
- 通过 `/data/adb/dxandroid/rotate-ip.sh 12` 做蜂窝重注册后恢复：
  - 手机公网 IP 变为 `133.106.32.168`。
  - 手机本机可 ping `10.66.0.1`。
  - Hub 侧 Android peer 握手恢复新鲜。
  - Hub 可 ping `10.66.0.101`。
  - Hub 经 `10.66.0.101:1080` 出口 IP 为 `133.106.34.14`。
  - `10.66.0.101:2022` 控制面端口可达。
  - 20MB 单流测速 `950298 B/s`,约 `7.6 Mbps`。

## 追加观察

本次恢复进一步支持以下判断：

- 当前低速/掉线主因不是 Hub 配置,而是 Android 蜂窝侧 UDP/WireGuard 握手路径或 NAT/小区状态。
- WireGuard App intent 可以触发重拨,但当蜂窝 UDP 状态卡死时,单纯 DOWN/UP 不一定足够;飞行模式重注册能恢复握手。
- 后续可把“连续多轮 intent 重拨仍失败 -> 飞行模式重注册”做成可配置的本地自愈升级,但这会主动断网,需要单独评估冷却和安全阀。

## 同日追加：本机控制面 key 与 admin-innernet peer

- 在本机生成 Android 控制面 SSH key:
  - private: `~/.ssh/dxandroid_control_local`
  - public: `~/.ssh/dxandroid_control_local.pub`
- 已把公钥追加到手机 `/data/adb/dxandroid/.ssh/authorized_keys`。
- 本机直连与经 Hub 跳板均可登录:
  - `ssh -i ~/.ssh/dxandroid_control_local -p 2022 root@10.66.0.101 id`
  - `ssh -J root@36.50.84.68 -i ~/.ssh/dxandroid_control_local -p 2022 root@10.66.0.101 id`
- 新增管理专用 WireGuard peer `admin-innernet`:
  - IP: `10.66.0.40/32`
  - 真实配置: `local/wireguard/admin-innernet.conf`
  - 路由:仅 `AllowedIPs = 10.66.0.0/24`,不接管默认路由,不承载公网出口流量。
- Hub 已通过 `/opt/jp-gateway/scripts/add-peer.sh` 添加 peer,并用 `wg syncconf` 热重载。

## 同日追加：常驻状态栏

- 新增 macOS 状态栏工具源码:
  - `clients/admin-menubar/DaxiangInnernetStatus.swift`
- 新增管理内网 helper 脚本与 LaunchAgent 模板:
  - `tools/macos/admin-innernet/dxvpn-admin-innernet-up.sh`
  - `tools/macos/admin-innernet/dxvpn-admin-innernet-down.sh`
  - `tools/macos/admin-innernet/com.daxiang.dxvpn.innernet-status.plist`
  - `tools/macos/admin-innernet/com.daxiang.dxvpn.admin-innernet.plist`
- 已安装到本机:
  - `~/.dxvpn/bin/dxvpn-admin-innernet-*.sh`
  - `~/.dxvpn/wireguard/admin-innernet.conf`
  - `local/apps/DaxiangInnernetStatus`
  - `~/Library/LaunchAgents/com.daxiang.dxvpn.innernet-status.plist`
- 已加载 LaunchAgent,状态栏进程正在运行。
- 本 Mac mini 已有 `10.66.0.100/24` 出口隧道常驻,因此不在本机强启第二条 `admin-innernet` 隧道,避免同一 `10.66.0.0/24` 路由重叠。
- 当前本机经现有常驻隧道可 ping Hub `10.66.0.1` 和 Android `10.66.0.101`。

## 同日追加：Android 出口加速研究

- 新增研究文档: `docs/30-implementation/android-egress-performance-acceleration.md`。
- 当前 Rakuten LTE serving cell 质量较差:
  - Band 3, 20MHz。
  - RSRP 约 `-101 dBm`, RSRQ 约 `-15 dB`, SNR `3`。
  - Android 估算上行约 `5.8 Mbps`,下行约 `9.8 Mbps`。
- 实测手机本机 20MB 单流下载约 `1.46-2.43 Mbps`。
- 实测 Hub 经 Android 出口 20MB 单流约 `0.001-2.0 Mbps`,波动极大。
- 8 并发流没有聚合收益,说明当前不是单流 TCP 未吃满,而是无线/运营商调度状态本身很差。
- 建议下一步优先切 UQ/au 做同位置矩阵测试,再评估 Hysteria2/TUIC/QUIC 承载实验。

## 同日追加：au/UQ 切卡复测

切回 au/UQ 后,初始数据面未稳定:

- APN 为 `uqmobile.jp`。
- 手机公网 IP 一开始取不到。
- 手机无法 ping Hub 公网和 WG 内网。
- WireGuard App 持续 `Handshake did not complete`。

执行 `rotate-ip.sh 12` 后恢复:

- 手机公网 IP: `59.132.5.127`。
- 手机可 ping `36.50.84.68` 和 `10.66.0.1`。
- Hub 可经 `10.66.0.101:1080` 拿到出口 IP `59.132.5.127`。

复测信号:

- KDDI / UQ, Band 1, 20MHz。
- RSRP `-117`, RSRQ `-15`, SNR `-4`, CA=false。

复测吞吐:

- 手机本机下载 20MB:约 `8.91-12.15 Mbps`。
- 手机本机上传 10MB 粗测:约 `0.16-0.42 Mbps`,全部超时。
- Hub 经 Android 出口下载 20MB:约 `0.04-0.53 Mbps`。
- Hub 经 Android 出口 8 并发:聚合约 `0.60 Mbps`。

判断:

- au/UQ 当前下行明显优于 Rakuten 当前状态。
- 但上行极差,且高延迟/弱信号严重;作为出口时仍被手机上行回 Hub 卡死。
- 下一步应先改善无线条件,例如移动位置、重选小区、外置/更好设备,再做协议层 Hysteria2/TUIC 实验。

## 同日追加：Rakuten + iPhone USB 交叉验证

为排除 Android 设备因素,使用 Rakuten SIM + iPhone 通过 USB 连接 Mac mini 做同位置对照。

Mac 侧定位:

- `iPhone USB` 服务存在,设备为 `en8`。
- 初始 `en8` 为 `inactive`,没有 IPv4。
- 管理员手动执行 `ifconfig en8 up` 后,接口变为 `UP/RUNNING/active`,但 DHCP 仍未获得租约。
- 手动配置 `172.20.10.2/28` 后,可 ping 通 iPhone 热点网关 `172.20.10.1`,约 `0.8-1.2 ms`。
- 给 `1.1.1.1` 添加临时 host route 到 `172.20.10.1` 后可 NAT 出网,出口 IP 为 `133.106.34.64`。

测速:

- iPhone USB 下载 20MB:
  - `962,443 B/s` (`7.70 Mbps`)
  - `661,621 B/s` (`5.29 Mbps`)
  - `2,037,012 B/s` (`16.30 Mbps`)
- iPhone USB 上传 10MB:
  - `164,025 B/s` (`1.31 Mbps`,仅上传约 6.5MB)
  - `269,285 B/s` (`2.15 Mbps`)
  - `264,502 B/s` (`2.12 Mbps`)
- iPhone USB 到 `1.1.1.1` ping:
  - min `35.4 ms`,avg `245.7 ms`,max `555.2 ms`。
- iPhone USB 8 并发下载 5MB:
  - 聚合 `1,732,518 B/s` (`13.86 Mbps`)。

判断:

- iPhone + Rakuten USB 热点网关和 NAT 实际可用,问题不是手机/SIM 完全不工作。
- Mac 当前没有自动从 `iPhone USB` 获取 DHCP/默认路由,需要单独修复或写静态临时配置脚本。
- 与 Android/Rakuten 当前状态相比,iPhone 形态更容易跑出可用吞吐;Android 设备、系统策略或 WireGuard App external 链路仍可能是额外损耗源。

## 同日追加：双手机同时连接拓扑

当前 Mac mini 同时连接两台手机:

- Android: au/UQ SIM,ADB 设备 `ZY22FMLNFK`,型号 `XT2201_2`;当前不是 Mac 网络服务。
- iPhone: Rakuten SIM,Mac 网络服务为 `iPhone USB/en8`,MAC `16:35:b7:2f:1f:63`。

因此两条链路必须分开看:

- Android/au:手机本机蜂窝 + WireGuard `tun0=10.66.0.101` + Hub/代理出口。
- iPhone/Rakuten:Mac 通过 `en8` 走 USB 热点;本机 DHCP 异常时需静态地址/临时 route 验证。

同时间 Android/au 状态:

- 运营商: `KDDI / UQ mobile`。
- 手机公网 IP: `59.132.5.127`。
- Android 到 `1.1.1.1` ping: min `59.9 ms`,avg `106.5 ms`,max `212.0 ms`。

Android/au 本机 20MB 下载复测:

- run1:45 秒超时,下载 `8,588,433 bytes`,`190,856 B/s` (`1.53 Mbps`)。
- run2:45 秒超时,下载 `5,750,034 bytes`,`127,779 B/s` (`1.02 Mbps`)。
- run3:45 秒超时,下载 `4,158,541 bytes`,`92,409 B/s` (`0.74 Mbps`)。

判断:

- Android/au 当前状态比前一轮 au/UQ 复测明显变差,说明蜂窝小区/时段/调度波动非常大。
- iPhone/Rakuten 的 `en8` DHCP 问题是 Mac 侧问题,不能与 Android/au 出口低速混为一个故障。

## 同日追加：Android/au 阳台复测

Android/au 放到阳台后,控制面保持在线:

- Mac 到 Hub `10.66.0.1` ping avg `14.6 ms`。
- Mac 到 Android `10.66.0.101` ping avg `78.1 ms`。
- Android `10.66.0.101:1080` 和 `2022` 均可达。
- Hub 上 Android peer endpoint 为 `59.132.5.127`。

信号:

- KDDI / UQ mobile,LTE,20MHz,no CA。
- ServiceState 主字段:EARFCN `100`,Band 1,RSRP `-113`,RSRQ `-15`,RSSNR `-2`。
- CellInfo 注册小区字段显示 EARFCN `1300`,Band 3,RSRP `-117`,RSRQ `-16`;telephony 输出有刷新不同步迹象,但整体仍是弱信号/低 SINR。

Android 本机下载 20MB:

- run1:`10,579,223 B/s` (`84.63 Mbps`)。
- run2:`4,712,789 B/s` (`37.70 Mbps`)。
- run3:`11,546,750 B/s` (`92.37 Mbps`)。

Android 本机上传 10MB:

- run1:45 秒超时,上传 `6,422,528 bytes`,`142,721 B/s` (`1.14 Mbps`)。
- run2:45 秒超时,上传 `5,177,344 bytes`,`115,047 B/s` (`0.92 Mbps`)。
- run3:45 秒超时,上传 `5,439,488 bytes`,`120,874 B/s` (`0.97 Mbps`)。

Hub/Mac 经 Android 出口下载 20MB:

- run1:45 秒超时,下载 `4,082,251 bytes`,`90,711 B/s` (`0.73 Mbps`)。
- run2:45 秒超时,下载 `5,074,360 bytes`,`112,761 B/s` (`0.90 Mbps`)。
- run3:45 秒超时,下载 `5,870,843 bytes`,`130,451 B/s` (`1.04 Mbps`)。

判断:

- 阳台位置显著改善 Android/au 下行,但没有改善上行到可做高速出口的程度。
- 当前 Android 出口下载速度与 Android 本机上行速度一致,瓶颈是蜂窝上行/回 Hub 的 WireGuard 回传路径。
- 继续调 MTU 或 SOCKS 端口不会突破 `~1 Mbps` 上行瓶颈;下一步应优先找上行更强的位置/设备/运营商。

## 同日追加：root 后是否破坏网络的只读审计

针对“root 后是否把 Android 网络搞坏”的怀疑,做了只读体检。

未发现明显人为限速证据:

- `tc qdisc` 未见自定义限速队列,`rmnet_data2` 仍为系统默认 `mq/pfifo_fast`。
- `iptables`/`nat`/`mangle` 未见自定义限速或异常转发规则;主要是 Android/Qualcomm/Motorola/ThinkShield/netd 默认链。
- `ip route get 1.1.1.1` 显示 root shell 公网流量走 `rmnet_data2`,没有错走 `tun0`。
- `cmd netpolicy get restrict-background` 为 disabled;后台流量限制未开启。
- Thermal Status 为 `0`,CPU/skin/battery 温度正常,未见热限频。
- Magisk modules 目录未发现额外模块;当前自启动脚本主要是 `98-dxandroid-control.sh` 和 `99-dxandroid-egress.sh`。
- `99-dxandroid-egress.sh` 主要做 doze disable、stay awake 和拉起 egress,未设置限速。

发现的风险点:

- 手机当前 `mIsPowered=false`,`USB powered=false`,阳台状态未在充电;长期部署需要补电。
- `dumpsys connectivity` 估计蜂窝 `LinkUpBandwidth>=4525Kbps`,但实测上行明显低于该估计。
- `egress.log` 中曾出现 `network: missing default interface`,随后更新为 `rmnet_data2`;这更像重注册/网络切换瞬态,不是持续限速。

直接回传验证:

- Mac 在 `10.66.0.100:39092` 临时启动 HTTP 接收器。
- Android 通过 WireGuard POST 10MB 到 Mac。
- 60 秒内 Android 仅上传 `4,980,736 bytes`,`83,012 B/s`。
- Mac 侧接收 `4,980,736 bytes`,耗时 `69.428s`,`71,740 B/s` (`0.57 Mbps`)。

判断:

- 目前没有证据支持“root 脚本/iptables/tc 把上行限死”。
- 当前更像是 au 蜂窝上行本身或 radio/小区调度状态弱;但要彻底排除 root/VPN,需要做一次“最小 root-bypass”物理验证。

## 同日追加：Fast.com 反证与同口径复测

用户在 Android/au 前台直接使用 Fast.com 测试:

- 下行 `80+ Mbps`。
- 上行 `10-20+ Mbps`。

这说明不能把当前低速简单归因为“au 蜂窝上行天然只有 1 Mbps”。随后改用多连接和 IPv4/IPv6 分层复测:

- root/curl 直连 Cloudflare 8 并发上传:
  - 聚合 `508,253 B/s` (`4.07 Mbps`)。
- root/curl 强制 IPv4 上传 5MB:
  - `492,788 B/s` (`3.94 Mbps`)。
  - `503,889 B/s` (`4.03 Mbps`)。
- root/curl 强制 IPv6 上传 5MB:
  - `465,638 B/s` (`3.73 Mbps`)。
  - `385,880 B/s` (`3.09 Mbps`)。
- Mac 经 Android SOCKS 上传到 Cloudflare:
  - run2:45 秒上传 `8,126,464 bytes`,`180,569 B/s` (`1.44 Mbps`)。
  - run3:45 秒上传 `10,485,760 bytes`,`233,009 B/s` (`1.86 Mbps`)。
- Mac 经 Android SOCKS 8 并发下载 5MB:
  - 聚合 `403,270 B/s` (`3.23 Mbps`)。

更新判断:

- Fast.com 前台测速证明 au/Android 的无线侧在某些目标/协议/多连接下可达到 `10-20 Mbps` 上行。
- root/curl/Cloudflare 与 SOCKS/WireGuard 回传路径仍显著低于 Fast.com,说明瓶颈已收敛到测试目标、协议、UID/应用网络路径、WireGuard 回传或代理路径差异。
- 下一步不能再只用 Cloudflare 单流判断;需要构造 Fast.com/Netflix 同口径测试,或做前台应用流量与 root/curl 流量的 A/B 对照。

## 同日追加：B 组 root/curl 复刻 Fast.com

用户已完成 A 组前台 Fast.com:

- Android/au 前台 Fast.com 下行 `80+ Mbps`。
- Android/au 前台 Fast.com 上行 `10-20+ Mbps`。

执行 B 组:Android root/curl 直接请求 Fast.com 当前 JS 使用的 Netflix OCA API。

Fast.com 当前参数:

- API:`https://api.fast.com/netflix/speedtest/v2`。
- token:`YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm`。
- `urlCount=5`。
- client:KDDI,IPv6,Tokyo/Japan。
- OCA targets:Tokyo / Osaka `*.oca.nflxvideo.net`。
- JS 配置最大连接数:8。

B 组下载:

- root/curl Netflix OCA 3 并发下载:
  - 聚合 `4,535,719 B/s` (`36.29 Mbps`)。
- root/curl Netflix OCA 8 并发下载:
  - 聚合 `2,099,885 B/s` (`16.80 Mbps`)。
  - 8 并发时所有流 30 秒超时,说明连接数过高反而触发拥塞/调度抖动。

B 组上传:

- Fast.com 上传逻辑不是 POST 到 `/speedtest`,而是把 URL 改成 `/speedtest/range/` 后上传。
- 直接 POST 5MB 到 `/speedtest/range/` 返回 `413`,payload 过大。
- 512KB probe 会收到 `405`,但请求上传进度可观测到约 `393,216 bytes` 实际发送。
- 持续 8 worker 小块上传 17 秒:
  - 总上传 `14,548,992 bytes`。
  - 折算 `855,823 B/s` (`6.85 Mbps`)。

更新判断:

- B 组 root/curl 复刻 Fast.com 后,速度显著高于 Cloudflare 单流和 SOCKS/WireGuard 回传路径,说明“只用 Cloudflare 单流判断 Android 上行”是不成立的。
- B 组仍低于前台 Fast.com A 组,说明前台浏览器/测速算法/连接调度/应用网络优先级仍可能带来差异。
- 当前最可疑瓶颈从“au 无线上行本身”进一步收敛为“WireGuard/SOCKS 回传路径或 root 后台进程路径跑不满 Fast.com 前台可用带宽”。

## 同日追加：ADB 恢复后的 Wi-Fi A/B 与 WireGuard 回传上限

用户重新连接 Android USB 并切换 USB 模式后,ADB 从空列表恢复:

- 设备:`ZY22FMLNFK`。
- model:`XT2201_2` / `XT2201-2`。
- 状态:`device`。

ADB 为空时的真实原因:

- Mac 没有枚举到 Android 的 ADB 接口。
- 不是单纯的授权未确认;如果只是未授权,`adb devices` 通常会显示 `unauthorized`。

Android 当前 Wi-Fi 状态:

- SSID:`JDCwifi_9BD3`。
- `wlan0=192.168.68.117/24`。
- root 默认公网路由:`1.1.1.1 via 192.168.68.1 dev wlan0`。
- Android 本机公网 IP:`118.158.252.9`。
- Wi-Fi 质量:RSSI 约 `-33` 到 `-40 dBm`,Link speed `650-780 Mbps`,5GHz。
- Android VPN `tun0=10.66.0.101/24`,VPN 底层网络为 Wi-Fi。

Android 本机 Wi-Fi 直连 Cloudflare:

| 项目 | run1 | run2 | run3 |
| --- | ---: | ---: | ---: |
| 下载 20MB | `6,196,431 B/s` (`49.57 Mbps`) | `7,455,342 B/s` (`59.64 Mbps`) | `7,680,987 B/s` (`61.45 Mbps`) |
| 上传 5MB | `6,895,251 B/s` (`55.16 Mbps`) | `6,523,843 B/s` (`52.19 Mbps`) | `6,929,142 B/s` (`55.43 Mbps`) |

Mac 经 Android SOCKS,同样在 Android Wi-Fi 出口下:

- 公网 IP 已变为 `118.158.252.9`,确认 SOCKS 复测走 Wi-Fi,不是 au 蜂窝。
- SOCKS 下载 20MB:
  - run1:`1,357,230 B/s` (`10.86 Mbps`)。
  - run2:`2,172,949 B/s` (`17.38 Mbps`)。
  - run3:`2,069,363 B/s` (`16.55 Mbps`)。
- SOCKS 8 并发下载 5MB:
  - 聚合 `2,108,893 B/s` (`16.87 Mbps`)。

Android 通过 WireGuard 直接 POST 10MB 回 Mac:

- Wi-Fi 下单次:`2,426,635 B/s` (`19.41 Mbps`)。
- 重复样本约 `2,257,967-2,570,899 B/s` (`18.06-20.57 Mbps`)。

MTU 临时扫测:

- 尝试通过 `ip link set dev tun0 mtu <value>` 调整 `1280/1200/1120/1080`。
- Android VPN 层返回 `Permission denied`,因此这组只是同一 MTU 下的重复样本,不能作为 MTU 对照结论。

更新判断:

- Android 本机 Wi-Fi 网络没有坏;直连上下行都能跑到 `50+ Mbps`。
- Mac 经 Android 出口的下载仍被 Android -> WireGuard -> Mac/Hub 回传路径限制在约 `17-21 Mbps`。
- 8 并发没有突破总吞吐,说明不是单 TCP 流窗口问题。
- `egress.log` 里的 `cellular-direct` 是历史 tag 命名,不是当前一定强制蜂窝;复测公网 IP 已证明 Wi-Fi 状态下 SOCKS 会走 Wi-Fi。
- 当前真实瓶颈分两层:
  - Wi-Fi 状态:WireGuard App `tun0` + `dxandroid-egress` 代理回传总吞吐约 `17-21 Mbps`。
  - au 蜂窝状态:在上述上限之外,还叠加蜂窝上行/小区调度/目标站点差异,实际可从 `~1 Mbps` 到 Fast.com 口径的 `6-20 Mbps` 波动。

## 同日追加：反向出口通道 Phase 0 试验

为验证“不要自己实现 WireGuard,而是绕开官方 WireGuard App 数据面”的方向,新增实验组件:

- 路径:`egress/reverse/`。
- 形态:TCP + `yamux` 反向 HTTP CONNECT 隧道。
- Hub 端:
  - 公网测试监听:`0.0.0.0:39093`。
  - 本地代理:`127.0.0.1:18081`。
- Android 端:
  - `/data/local/tmp/dxreverse client` 主动连接 Hub。
  - 不创建 VPN/tun,不依赖官方 WireGuard App 的数据面。
- DNS 处理:
  - 初版让 Android Go binary 直接 `net.Dial("host:port")`,CONNECT 返回 502。
  - 原因判断:纯 Go binary 在 Android root shell 下不可靠接入 Android netd DNS。
  - 修正:Hub 端先解析域名,把 `IP:port` 发给 Android;TLS SNI 仍由原始 HTTPS 流量携带。

测试过程:

- 临时打开 Hub UFW `39093/tcp`。
- 测试结束后已删除该 UFW 规则。
- 测试结束后已停止 Hub/Android 的 `dxreverse` 进程。

Android Wi-Fi 下的反向通道结果:

- 反向代理出口 IP:`118.158.252.9`,确认由 Android Wi-Fi 出口访问公网。
- Cloudflare 20MB 单流下载:
  - run1:`5,585,139 B/s` (`44.68 Mbps`)。
  - run2:`5,470,030 B/s` (`43.76 Mbps`)。
  - run3:`5,596,490 B/s` (`44.77 Mbps`)。
- Cloudflare 8 并发 5MB:
  - 聚合 `6,260,248 B/s` (`50.08 Mbps`)。

对比:

- 官方 WireGuard App `tun0` + `dxandroid-egress` 数据面在同一 Android Wi-Fi 下约 `17-21 Mbps`。
- Phase 0 反向通道达到 `44-50 Mbps`,已经明显突破 WG App 数据面上限。

结论:

- 方向成立:无需自己实现 WireGuard;更高收益路径是保留 WireGuard 作管理内网,把出口数据面迁到 Android 主动拨出的反向高速通道。
- TCP/yamux 只是验证原型;后续生产化可继续演进为 QUIC/Hysteria2/TUIC 类传输,以改善蜂窝抖动、丢包和队头阻塞。
- 下一步应在 au 蜂窝关闭 Wi-Fi 时复测 Phase 0,确认它对真实移动链路的改善幅度。

## 同日追加：反向通道 Phase 0 蜂窝复测

用户关闭 Android Wi-Fi 后继续测试。手机仍在室内,预期蜂窝速度偏低。

手机状态:

- Wi-Fi:`disabled`。
- 默认公网路由:`1.1.1.1 via 10.44.61.184 dev rmnet_data1`。
- Android 本机公网 IP:`59.132.8.186`。
- 反向通道 Hub 看到客户端来源:`59.132.8.186:45006`,确认走 au 蜂窝。

Android 本机直连 Cloudflare 2MB:

- run1:`355,442 B/s` (`2.84 Mbps`)。
- run2:`431,048 B/s` (`3.45 Mbps`)。
- run3:`515,680 B/s` (`4.13 Mbps`)。
- ping `1.1.1.1`:avg `141.5 ms`,max `213.3 ms`,0% loss。

旧 WireGuard App + `dxandroid-egress` SOCKS 蜂窝小包:

- run1:SOCKS5 连接失败。
- run2:45 秒超时,收到 `827,241 bytes`,`18,380 B/s` (`0.15 Mbps`)。
- run3:45 秒超时,收到 `1,125,303 bytes`,`25,004 B/s` (`0.20 Mbps`)。

Phase 0 TCP/yamux 反向通道:

- 20MB 大包:
  - 80 秒超时,收到 `2,637,571 bytes`,`32,968 B/s` (`0.26 Mbps`)。
- 2MB 小包:
  - run1:连接中途关闭,收到 `235,766 bytes`,`7,130 B/s` (`0.057 Mbps`)。
  - run2/run3:CONNECT 返回 `503`,因为反向客户端已掉线。
- Hub 日志:
  - `yamux: keepalive failed: i/o deadline reached`。
  - `reverse client disconnected` 后自动重连。

蜂窝复测结论:

- Phase 0 证明了“绕开官方 WireGuard App 数据面”在 Wi-Fi 下收益巨大,但 TCP/yamux 不适合弱蜂窝。
- 当前失败特征符合 TCP-over-TCP/单连接多路复用在移动弱网下的队头阻塞:Android 本机直连仍有 `2.8-4.1 Mbps`,但 TCP 隧道内 HTTPS 只有 `0.06-0.26 Mbps` 并断线。
- 下一步不应继续打磨 TCP/yamux;应实现 UDP/QUIC 数据面,优先验证:
  - 单请求单 QUIC stream 或多 QUIC connection。
  - 主动重连与连接迁移。
  - 小窗口/丢包下的并发下载恢复能力。
  - Hub 端本地 CONNECT 代理保持不变,只替换 Android-Hub 传输层。

清理:

- Hub 临时 `39093/tcp` UFW 规则已删除。
- Hub/Android `dxreverse` 测试进程已停止。

## 同日追加：反向通道 Phase 1 QUIC 蜂窝复测

在 `egress/reverse` 中新增 `--transport quic`,保留原 `--transport tcp`。QUIC 实现要点:

- 使用 UDP + QUIC bidirectional stream 替代 TCP/yamux。
- Hub 端仍暴露本地 HTTP CONNECT 代理 `127.0.0.1:18081`。
- Android 主动连接 Hub,无需手机入站端口。
- 先用临时自签 TLS 与 token auth;当前仍是实验组件。
- 新增 `--resolve server|client`:
  - `server`:Hub 解析目标域名,发送 `IP:port` 给 Android。
  - `client`:发送原始 `host:port`,Android 端用公共 DNS 解析,用于排除 Hub DNS/CDN 选点影响。

本机闭环:

- QUIC auth 通过。
- `curl --proxy http://127.0.0.1:18082 https://api.ipify.org` 成功。
- 5MB 下载约 `5,533,777 B/s` (`44.27 Mbps`)。

au 蜂窝室内,QUIC + server DNS:

- 反向代理出口 IP:`59.132.8.186`。
- 2MB 单流:
  - run1:50 秒超时,收到 `1,646,329 bytes`,`32,925 B/s` (`0.26 Mbps`)。
  - run2:50 秒超时,收到 `171,254 bytes`,`3,425 B/s` (`0.027 Mbps`)。
  - run3:50 秒超时,收到 `164,897 bytes`,`3,297 B/s` (`0.026 Mbps`)。
- 4 并发 2MB:
  - 0/4 完整成功。
  - 聚合约 `1.07 Mbps`,但存在 `Empty reply from server` 和多条 timeout。

同一时段 Android 本机直连复测:

- run1:`137,374 B/s` (`1.10 Mbps`)。
- run2:`168,501 B/s` (`1.35 Mbps`)。
- run3:`204,514 B/s` (`1.64 Mbps`)。

QUIC + client DNS:

- 反向代理出口 IP:`59.132.8.186`。
- 2MB 单流:
  - run1:50 秒超时,收到 `321,569 bytes`,`6,431 B/s` (`0.051 Mbps`)。
  - run2:50 秒超时,收到 `277,376 bytes`,`5,547 B/s` (`0.044 Mbps`)。
  - run3:50 秒超时,收到 `256,027 bytes`,`5,120 B/s` (`0.041 Mbps`)。
- 4 并发 2MB:
  - 0/4 完整成功。
  - 聚合约 `1.07 Mbps`,仍有 `Empty reply from server` 和 timeout。

Phase 1 判断:

- QUIC 解决了 TCP/yamux 的“很快 keepalive 掉线”问题,能稳定建立 UDP 会话。
- 但当前实现仍未把蜂窝吞吐拉到 Android 本机直连水平;单流明显差,并发只能接近 `~1 Mbps`。
- `client DNS` 没有改善,说明主要不是 Hub DNS/CDN 选点。
- 当前最可能的剩余问题:
  - 室内 au 蜂窝在测试时段本身已降到 `1.1-1.6 Mbps`。
  - CONNECT-over-QUIC-stream 仍是每个 HTTPS 连接一条可靠流,弱网下拥塞恢复慢。
  - 需要多 QUIC connection/连接池,而不是所有 stream 压在一个 QUIC connection 上。
  - 需要针对移动弱网调小请求块、增加并发调度和失败重试,而不是照搬单流 curl。

清理:

- Hub 临时 `39093/udp` UFW 规则已删除。
- Hub/Android `dxreverse` 测试进程已停止。

## 同日追加：反向通道 Phase 3 Android 侧 FETCH

继续验证“像 Tailscale 一样只走内网控制面,大流量出口由 Android 主动反连回 Hub”的数据面路线。

本轮给 `egress/reverse` 新增实验性 `/fetch` 端点:

- Hub 本地代理新增 `GET /fetch?url=<absolute-http-or-https-url>`。
- Hub 通过反向 QUIC stream 发送 `FETCH <base64url(url)>`。
- Android 侧直接执行 HTTP GET,再把 `STATUS`、响应头和 body 流式传回 Hub。
- `/fetch` 不是透明代理替代品,而是用于测试可恢复、可分块的 Android 侧应用层拉取。

实现过程中修复两个 Android root 环境问题:

- Go 默认 DNS 在 Android root shell 中读到 `[::1]:53`,导致 `lookup speed.cloudflare.com ... connection refused`;已改为 `FETCH` 与 CONNECT 复用公共 DNS `dialTarget`。
- Go 静态二进制没有自动拿到 Android 系统 CA,导致 `x509: certificate signed by unknown authority`;已显式加载 `/system/etc/security/cacerts` 和 `/apex/com.android.conscrypt/cacerts`。

本机闭环:

- QUIC `--connections 2`。
- `/fetch` 200KB: `1,387,385 B/s`。
- CONNECT 200KB: `695,332 B/s`。

au 蜂窝室内,QUIC pool `--connections 4`:

- CONNECT 2MB:
  - run1:`50,522 B/s` (`0.40 Mbps`)。
  - run2:`71,988 B/s` (`0.58 Mbps`)。
  - run3:`84,665 B/s` (`0.68 Mbps`)。
- `/fetch` 2MB:
  - run1:`65,625 B/s` (`0.53 Mbps`)。
  - run2:`73,731 B/s` (`0.59 Mbps`)。
  - run3:`87,636 B/s` (`0.70 Mbps`)。
- 同时段 Android 本机直连 Cloudflare 2MB:
  - run1:`405,332 B/s` (`3.24 Mbps`)。
  - run2:`463,146 B/s` (`3.71 Mbps`)。
  - run3:`529,412 B/s` (`4.24 Mbps`)。
- 同时段 Android 直接 POST 2MB 到 Hub:
  - run1:`52,170 B/s` (`0.42 Mbps`)。
  - run2:`60,533 B/s` (`0.48 Mbps`)。
  - run3:`69,076 B/s` (`0.55 Mbps`)。

Phase 3 判断:

- `/fetch` 已跑通,但没有突破 CONNECT 的速度上限。
- Android 本机下行到 Cloudflare 明显高于反向出口,说明手机接收公网内容不是当前主瓶颈。
- Android 直接上传到 Hub 的裸速度与 CONNECT/FETCH 完全重叠,因此当前室内 au 极限被锁定在 Android -> Hub 回传上行路径。
- 短期继续改 CONNECT/FETCH 不会把 `~0.5 Mbps` 到 Hub 的上行变成高速出口。
- 下一步若继续“极致加速”,优先级应是:
  - 换更近/不同网络的 Hub 做 Android -> Hub 上行 A/B。
  - 做多手机/多运营商聚合,把多个 `0.5-2 Mbps` 弱上行拼起来。
  - 对可控下载实现 Android 侧 range 分块、失败重试和多出口调度。
  - 对透明代理场景做快速失败和出口切换,不要在单条弱上行上硬撑。

清理:

- Hub 临时 `39093/udp` 和 `39094/tcp` UFW 规则已删除。
- Hub/Android `dxreverse` 测试进程已停止。

## 同日追加：Android/au 拔 USB 后阳台远程复测

用户拔掉 Android USB,将手机单独放到阳台后,仅从 Hub 侧做远程复测,验证真实无人值守形态。

连通状态:

- Android peer endpoint:`59.132.8.186:49723`。
- WireGuard latest handshake:13 秒内。
- Hub 到 Android `10.66.0.101` ping:8/8 成功,0% loss。
- ping RTT:min `37.0 ms`,avg `109.0 ms`,max `197.4 ms`。
- Hub 经 Android SOCKS 出口 IP 三次均为 `59.132.8.186`,响应约 `0.71-0.83s`。

Hub 经 Android 出口下载 20MB 单流:

- run1:60 秒超时,下载 `9,089,099 bytes`,`151,482 B/s` (`1.21 Mbps`)。
- run2:60 秒超时,下载 `14,922,851 bytes`,`248,711 B/s` (`1.99 Mbps`)。
- run3:60 秒超时,下载 `10,796,491 bytes`,`179,938 B/s` (`1.44 Mbps`)。

Hub 经 Android 出口 8 并发:

- 8 路 5MB,60 秒后合计收到 `12,179,095 bytes`,聚合约 `1.62 Mbps`。
- 8 路 3MB,45 秒后合计收到 `7,126,182 bytes`,聚合约 `1.27 Mbps`。

判断:

- USB 拔掉后控制面和代理口仍保持在线,说明当前常驻形态可远程工作。
- 阳台位置相对室内 `~0.5 Mbps` 有提升,单流峰值接近 `2 Mbps`。
- 但 8 并发没有把聚合明显拉高,说明当前仍受 Android -> Hub 蜂窝上行预算和无线调度限制。
- 这组数据进一步支持:优化要优先做位置/上行链路/Hub 选点/多出口聚合,而不是继续在单手机单 Hub 的透明代理栈里硬榨。

## 同日追加：启动新版完全替代老版的数据面重构

用户明确要求“完全用新版重构,替代老版”。本轮开始把 `egress/reverse` 从实验组件提升为 Android 生产数据面:

- `dxreverse server/client` 新增 YAML `--config` 支持。
- 支持 `token_file`,避免在 systemd/Magisk 脚本中直接写 token。
- 默认生产传输改为 QUIC,client 默认 `connections=4`。
- 新增 Hub systemd 模板:
  - `egress/reverse/systemd/dxreverse-hub.service`
- 新增 Android Magisk service.d 模板:
  - `egress/reverse/service.d/99-dxreverse-egress.sh`
  - 默认停止旧 `99-dxandroid-egress` / `dxandroid-egress`,再常驻 `dxreverse client`。
- 新增配置示例:
  - `docs/20-operations/configs/egress/hub-reverse-server.yaml.example`
  - `docs/20-operations/configs/egress/android-reverse-client.yaml.example`
- `egress/android-control/watchdog.sh` 的出口保活目标从 `99-dxandroid-egress.sh` 切换到 `99-dxreverse-egress.sh`。
- Android 状态 App 探针从 `dxandroid-egress` PID、本地 `10.66.0.101:1080` 监听和旧日志,切换为:
  - `dxreverse` PID。
  - `ss -untp | grep dxreverse` 反连会话。
  - `/data/local/tmp/dxreverse-egress.log` 新版出口日志。
- 新增 Hub 侧新版健康检查:
  - `scripts/check-android-reverse-egress.sh`

架构语义更新:

- Android 出口主入口不再是手机内网 `10.66.0.101:1080`。
- 新主入口是 Hub 本地 `127.0.0.1:18081` HTTP CONNECT proxy。
- Android 手机不接受入站代理连接,只主动连接 Hub UDP `39093`。
- WireGuard App 仍作为控制面保留,用于 `10.66.0.101:2022` SSH 和 watchdog 自愈。
- 旧 `dxandroid-egress` 只作为回滚路径保留。

物理上线结果:

- Hub 已部署:
  - binary:`/opt/daxiang/dxreverse/dxreverse`
  - config:`/etc/daxiang/dxreverse/server.yaml`
  - token:`/etc/daxiang/dxreverse/token`
  - service:`/etc/systemd/system/dxreverse-hub.service`
  - UFW:`39093/udp` 已开放。
- Android 已部署:
  - binary:`/data/adb/dxreverse/bin/dxreverse`
  - config:`/data/adb/dxreverse/client.yaml`
  - token:`/data/adb/dxreverse/token`
  - service:`/data/adb/service.d/99-dxreverse-egress.sh`
  - old service:`/data/adb/service.d/99-dxandroid-egress.sh.disabled`
- 当前运行进程:
  - Android:`sh /data/adb/service.d/99-dxreverse-egress.sh`
  - Android:`dxreverse client --config /data/adb/dxreverse/client.yaml`
  - Hub:`dxreverse server --config /etc/daxiang/dxreverse/server.yaml`
- Hub 日志显示 4 条 QUIC reverse session 均已连接,来源 `59.132.8.186`。
- Hub 侧健康检查:
  - `scripts/check-android-reverse-egress.sh`
  - `PASS egress_ip=59.132.8.186`
  - 1MB probe:`144,016 B/s`,约 `1.15 Mbps`。

仍需后续接入:

- 如果现有客户端/Hub 出口调度还硬编码 `10.66.0.101:1080`,需要改为使用 Hub 本地 `127.0.0.1:18081`。
- Android 状态 App 已通过远程 ADB 完成最终部署:
  - 本机使用 Homebrew OpenJDK 17 + Android command-line tools 构建 `assembleDebug` 成功。
  - 通过控制 SSH 临时打开 `service.adb.tcp.port=5555`。
  - 本机 `adb connect 10.66.0.101:5555` 成功。
  - 旧状态 App 因签名不同先卸载,再安装新版 debug APK。
  - 已启动 `dev.daxiang.dxandroidstatus/.MainActivity`。
  - 安装结束后立即关闭 ADB TCP:`service.adb.tcp.port=-1`,确认 `5555` 无监听。
  - 当前状态 App 进程 `dev.daxiang.dxandroidstatus` 正在运行。

## 同日追加：反向通道 Phase 2 QUIC 连接池

按“别等了,搞起来”的要求,继续把 `egress/reverse` 从单 QUIC connection 升级为连接池:

- Android client 新增 `--connections N`。
- server 端保留多个 reverse session。
- Hub 每个 CONNECT 请求轮询选择一条 session。
- TCP/yamux 和 QUIC 都复用同一套 session pool;本轮实测使用 QUIC。

本机闭环:

- `--transport quic --connections 4` 成功建立 4 条 QUIC 连接。
- 4 并发 2MB 代理下载:4/4 成功,约 `41.96 Mbps`。

au 蜂窝室内,QUIC pool `--connections 4`:

- 反向代理出口 IP:`59.132.8.186`。
- 2MB 单流:
  - run1:完整成功,`68,513 B/s` (`0.55 Mbps`)。
  - run2:完整成功,`78,972 B/s` (`0.63 Mbps`)。
  - run3:完整成功,`69,695 B/s` (`0.56 Mbps`)。
- 4 并发 2MB:
  - 0/4 完整成功,但每条均收到约 `0.91-1.10MB`。
  - 聚合仍约 `1.07 Mbps`。
- 8 并发 1MB:
  - 0/8 完整成功,每条收到约 `0.25-0.32MB`。
  - 聚合仍约 `1.07 Mbps`。

Phase 2 判断:

- 连接池改善了单流完成度:Phase 1 单流经常 50 秒只收到 `0.16-1.65MB`;Phase 2 单流 2MB 三次均完整完成。
- 连接池没有提升当前室内蜂窝总吞吐;4/8 并发仍卡在约 `1.07 Mbps`。
- 当前瓶颈更像是该位置/时段的蜂窝总上行/回传预算,而不是单一 QUIC session 数。
- 下一步工程方向:
  - Android 端做应用层分块下载/失败重试,把大响应拆成可恢复的小块。
  - 对真实用户请求不能透明拆 TLS;所以生产方案要么用于可控 HTTP 下载,要么继续做多出口/多手机聚合。
  - 如果仍要透明代理,重点应放在连接保活、快速失败、自动切换出口,而不是追求单手机弱网下强行满速。

清理:

- Hub 临时 `39093/udp` UFW 规则已删除。
- Hub/Android `dxreverse` 测试进程已停止。
