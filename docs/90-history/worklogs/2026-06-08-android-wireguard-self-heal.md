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
