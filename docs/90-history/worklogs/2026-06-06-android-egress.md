# 2026-06-06 工作记录：Android 手机出口节点上线

## 摘要

今天完成了大象 VPN 的第二个日本出口节点：root Android 手机出口。

当前系统已经具备：

- 1 台公网 Hub 服务器。
- 1 个 Mac mini 日本住宅出口。
- 1 个 Android 手机卡出口。
- 多个 WireGuard client peer。
- 至少 1 个本地 token 配置，可指向指定出口代理。

## 当前拓扑

```text
客户端
  |
  | WireGuard
  v
Hub: 36.50.84.68 / wg0: 10.66.0.1/24
  |
  +--> Mac mini 出口:     10.66.0.100:1080 -> 118.158.252.9
  |
  +--> Android 手机出口: 10.66.0.101:1080 -> 60.124.42.38
```

## Hub 状态

- 主机名：`jp-proxy.ruichao.dev`
- 公网 IP：`36.50.84.68`
- WireGuard 接口：`wg0`
- Hub WireGuard 地址：`10.66.0.1/24`
- WireGuard 监听端口：`51820/udp`
- Hub 直连公网出口：`36.50.84.68`
- 当前可通过开发机免密 SSH 登录。

## 出口节点状态

### Mac mini 出口

- 节点名：`mac-mini`
- WireGuard 地址：`10.66.0.100/32`
- 代理地址：`10.66.0.100:1080`
- 代理类型：mixed，支持 HTTP 和 SOCKS5
- 当前验证出口 IP：`118.158.252.9`
- 已有 LaunchDaemon 固化启动。

验证命令：

```bash
curl -x http://10.66.0.100:1080 https://api.ipify.org
curl --socks5-hostname 10.66.0.100:1080 https://api.ipify.org
```

### Android 手机出口

- 节点名：`jp-android-01`
- 设备：`XT2201_2 / hiphic`
- 手机端 WireGuard 地址：`10.66.0.101/24`
- Hub 侧 AllowedIPs：`10.66.0.101/32`
- 代理地址：`10.66.0.101:1080`
- 代理类型：mixed，支持 HTTP 和 SOCKS5
- 当前验证出口 IP：`60.124.42.38`
- 运行方式：`dxandroid-egress`
- 当前运行目录：`/data/local/tmp/dxandroid-egress-work`
- 自启动脚本：`/data/adb/service.d/99-dxandroid-egress.sh`

验证命令：

```bash
ping 10.66.0.101
curl -x http://10.66.0.101:1080 https://api.ipify.org
curl --socks5-hostname 10.66.0.101:1080 https://api.ipify.org
```

手机端日志：

```powershell
adb shell su -c "tail -100 /data/local/tmp/dxandroid-egress-work/egress.log"
```

## 今日完成事项

1. 绕开 Windows 驱动签名问题，改为通过 WSL / usbipd 路线处理设备。
2. 清理 Windows 驱动签名失败后的残留：
   - 删除临时目录 `C:\drv_usb`。
   - 删除临时目录 `C:\fbdrv`。
   - 移除失败 fastboot 设备节点。
   - 移除测试自签证书。
   - 保留 `usbipd-win`。
3. 确认 Android 设备已经 root：
   - `adb shell su -c id` 返回 `uid=0(root)`。
   - Magisk root 可用。
4. 构建 Android arm64 出口节点程序：
   - 本地构建产物：`dist/dxandroid-egress`
   - 设备部署路径：`/data/local/tmp/dxandroid-egress`
5. 配置 Hub 上的 Android peer：
   - 节点：`jp-android-01`
   - 地址：`10.66.0.101/32`
6. 写入手机端 Android egress 配置：
   - 配置路径：`/data/local/tmp/android-egress.yaml`
   - 手机端地址使用 `10.66.0.101/24`
7. 启动并验证 Android 出口：
   - `wg0` 成功创建。
   - mixed 代理监听成功。
   - Hub 到 Android 的 WireGuard 握手成功。
   - Hub 可以访问 Android 代理。
   - HTTP 和 SOCKS5 代理均验证通过。
8. 安装 Android 端 Magisk 自启动脚本：
   - `/data/adb/service.d/99-dxandroid-egress.sh`

## 关键踩坑与结论

### Windows 驱动签名不是主线

Windows 侧安装 fastboot 驱动失败的根因是驱动签名：

- ClockworkMod 驱动缺少 catalog 签名文件。
- 自制 fastboot 驱动证书链不被 Windows 信任。

最终不继续消耗在 Windows 驱动签名上，改用 WSL / usbipd 路线。

### WSL 设备挂接需要确认 Attached

`usbipd list` 中 `Shared` 只表示允许共享，不等于设备已经进入 WSL。

真正可用时应看到：

```text
STATE = Attached
```

并且 WSL 中 `fastboot devices -l` 能看到设备。

### fastbootd 与 bootloader fastboot 要区分

排查过程中设备曾出现在 `fastbootd` 模式。

刷写时需要按分区类型区分：

- bootloader fastboot：适合 boot/vendor_boot 等底层分区。
- fastbootd：适合部分动态分区。

今天后续不再继续刷机，转向 root 后的 egress 部署。

### ADB shell 多层引号会导致 su 后半段掉权限

最关键的坑是：

```powershell
adb shell su -c "cmd1; cmd2"
```

在这个环境里容易出现后半段命令实际以普通 `shell` 用户执行的问题，导致：

- `CAP_NET_ADMIN` 缺失。
- `/dev/tun` 打不开。
- `TUNSETIFF: operation not permitted`。

稳定做法是先把脚本推到手机，再执行：

```powershell
adb shell su -c /data/local/tmp/script.sh
```

这样 Magisk root 能拿到完整 capabilities，可以创建 TUN/WireGuard 接口。

### Android 配置应使用 /24，Hub AllowedIPs 使用 /32

手机端配置如果写 `10.66.0.101/32`，会导致回 Hub 的 `10.66.0.1` 路由不正确。

最终采用：

- 手机端：`10.66.0.101/24`
- Hub 侧：`10.66.0.101/32`

这样 Hub 能访问手机代理，手机也能正确回包。

## 当前 Token 状况

本地 `backend/dxhub/config/tokens.yaml` 中目前看到的实际 token：

- `DX-JP-TEST-001`

该 token 当前绑定：

- 出口：`mac-mini`
- 代理：`10.66.0.100:1080`
- 客户端 WireGuard 地址：`10.66.0.20/24`

Hub 上还存在多个 WireGuard peer：

- `10.66.0.10`
- `10.66.0.20`
- `10.66.0.21` 到 `10.66.0.29`
- `10.66.0.30`
- `10.66.0.100`
- `10.66.0.101`

其中部分 peer 暂未在本地 token 文件里产品化管理。

## WiFi 对照与出口性能优化

手机拔出 SIM 后切到 WiFi，Android 出口仍可工作：

- 手机到 Hub 的路由切到 `wlan0`。
- Hub 看到 Android peer endpoint 变为 WiFi 公网地址。
- Windows 客户端出口 IP 也变为 WiFi 公网地址。

对照结果：

- UQ 手机卡直连手机下载很快，但作为出口时速度受手机上行回 Hub 影响。
- WiFi 下 Hub 直连 Android 代理明显快于 UQ 蜂窝链路，说明 UQ 蜂窝上行/移动网络路径是主要瓶颈。
- Android 日志仍会出现 `sendmsg: message too long`，说明 sing-box WireGuard system endpoint 在 Android 上仍有 MTU/GSO 相关问题。

已完成的最小优化：

- `dxandroid-egress` 支持 `wireguard.mtu` 配置项。
- `dxandroid-egress` 支持 `wireguard.workers` 配置项。
- 手机当前实验参数：`mtu: 1200`，`workers: 4`。
- 重启后 Magisk service 能重新拉起 Android 出口。
- Hub 已为 Android peer 固化专用路由 MTU：
  - `ip route replace 10.66.0.101/32 dev wg0 mtu 1280`
  - 已写入 `/opt/jp-gateway/wireguard/wg0.conf` 和 `/etc/wireguard/wg0.conf` 的 `PostUp`。

复测结果：

- Hub 经 Android WiFi 出口 50MB 下载约 23.8 Mbps。
- Hub 经 Android WiFi 出口 20MB 多次测试存在波动，约 16-47 Mbps。
- Windows 本地代理重启后经 Android WiFi 出口约 12.4 Mbps。
- 新增 `scripts/measure-android-egress.ps1` 用于从 Hub 侧重复测量 Android 出口：
  - WiFi 场景 3 轮样本曾测得平均约 27.66 Mbps，最小 13.78 Mbps，最大 39.76 Mbps。

判断：

- 当前瓶颈已经不是 Hub 或手机 WiFi 下载能力。
- 蜂窝场景的低速主要来自手机上行到 Hub。
- WiFi 场景仍有波动，后续应优先评估替换 Android 侧 WireGuard 承载方式，而不是继续只调 MTU。
- Hub 侧 MSS 从 `1240` 临时降到 `1160` 对 Windows 同源测试没有明显收益，已恢复 `1240`。

## 待办

1. 把出口选择产品化：
   - 支持 token 指向 `mac-mini`。
   - 支持 token 指向 `jp-android-01`。
   - 后续支持客户端切换出口。
2. 更新 Hub / token 配置结构：
   - 把出口节点抽象为统一 inventory。
   - token 只引用出口节点名，而不是手写代理地址。
3. 增加 Android 出口健康检查：
   - WireGuard 握手时间。
   - 代理端口可达性。
   - 当前公网出口 IP。
   - 进程状态。
4. 增加 Android 运维能力：
   - 重启后自动验证。
   - 日志轮转。
   - 崩溃重启计数。
   - 电量、温度、网络类型采集。
5. 修复 `dxandroid-egress` 的路由规则清理：
   - 当前曾残留 `to 10.66.0.101/32 lookup main pref 9999`。
   - 启动脚本已临时清理，后续应在程序内处理。
6. 完善文档中的当前状态：
   - [服务器访问文档](../../20-operations/runbooks/server-access.md)里的 peer 表需要补上 Android 出口。
7. 继续优化 Android 出口性能：
   - 优先验证 Android 原生/内核 WireGuard + sing-box 仅做代理的方案。
   - 评估 Hysteria2 / TUIC / QUIC 作为手机到 Hub 的承载协议。
   - 继续追踪 `sendmsg: message too long` 是否来自 sing-box WireGuard endpoint 的 GSO 行为。
