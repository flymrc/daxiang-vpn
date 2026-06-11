# 2026-06-11 Pixel 7a 控制面迁移

## 背景

Android 出口数据面已经迁到 `zhreverse` 反向 TCP/yamux,生产入口是 Hub 内网 `10.66.0.1:18081`。剩余风险是 Pixel 7a 的 WireGuard 控制面尚未接管 `10.66.0.101`,无人值守时缺少 SSH 控制、watchdog 自愈和受限 TCP ADB。

## 已完成

- 本机安装 Google 官方 Android SDK Platform-Tools `37.0.0-14910828`,后续 ADB 使用 `C:\Users\marui\.local\android-platform-tools\platform-tools\adb.exe`。
- Pixel 7a 安装 WireGuard App `1.0.20260315`。
- Hub 为 Pixel 新生成 `jp-android-01` WireGuard peer,并把 `10.66.0.101/32` 从旧 peer 切到 Pixel 新公钥。
- Pixel WireGuard App 导入 `jp-android-01.conf`,地址 `10.66.0.101/24`,MTU `1120`,Endpoint `36.50.84.68:51820`,AllowedIPs `10.66.0.0/24`。
- WireGuard App 高级设置已开启“授权外部控制”。
- 部署 `zhandroid-control` 到 `/data/adb/zhandroid/bin/zhandroid-control`。
- 部署 watchdog 到 `/data/adb/zhandroid/watchdog.sh`,开机脚本 `/data/adb/service.d/98-zhandroid-control.sh`。
- 部署 WG-only TCP ADB 脚本 `/data/adb/service.d/97-zhadb-tcp-wg-only.sh`,当前 `service.adb.tcp.port=5555`,iptables 只允许 `tun0` / `10.66.0.0/24`。
- Hub 控制钥匙 `/root/.ssh/zhandroid_control_hub`、本机控制钥匙 `~/.ssh/zhandroid_control_local` 均写入 Pixel `/data/adb/zhandroid/.ssh/authorized_keys`。

## 重要修正

原 watchdog 依赖 `IP_FREEBIND` 让 `zhandroid-control` 在 `tun0` 未出现前提前绑定 `10.66.0.101:2022`。Pixel 7a 上实测会进入坏状态:

```text
accept tcp 10.66.0.101:2022: accept4: invalid argument
```

修正为:

- watchdog 等 `tun0` 已有 `10.66.0.101` 后再启动 `zhandroid-control -freebind=false`。
- `control_up()` 不只看进程,还检查 `/proc/net/tcp` 中 `10.66.0.101:2022` 是否处于 LISTEN。
- bounce 自愈从 `DOWN; sleep 3; UP` 改为 `DOWN` 后等待 `10.66.0.101` 地址消失,再 `UP`;否则可能出现有 `tun0` 但 Hub 无新握手的半坏状态。

## 验证结果

- Pixel `tun0`: `10.66.0.101/24`,MTU `1120`。
- Hub `wg show wg0 latest-handshakes` 中 Pixel peer 最新握手新鲜。
- Hub ping `10.66.0.101`:2/2 成功。
- Hub SSH:

```bash
ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101 'id'
```

返回 `uid=0(root)`。

- Hub TCP ADB 探测:

```bash
timeout 3 bash -lc '</dev/tcp/10.66.0.101/5555' && echo adb-tcp-open
```

返回 `adb-tcp-open`。

- WireGuard 自愈:
  - UI 关闭隧道后,root `SET_TUNNEL_UP` intent 可重新拉起,Hub 握手恢复。
  - `SET_TUNNEL_DOWN` 后等待 `tun0` 地址消失,再 `SET_TUNNEL_UP`,Hub ping 和 SSH 均恢复。

- Android 出口健康检查:

```powershell
.\scripts\check-android-egress-health.ps1 -HubIdentityFile "$env:USERPROFILE\.ssh\daxiang_server"
```

结果:

- reverse proxy 可用,出口 IPv6 `240b:c010:421:d18c:0:42:e654:1701`。
- Hub 到 `10.66.0.101/32` 路由 MTU `1120`。
- TCPMSS 规则存在。
- WireGuard handshake fresh。

## 当前状态

Pixel 7a 当前同时承担:

- Android 数据面:`zhreverse client`,主动连接 Hub `39093/tcp`,Hub proxy `10.66.0.1:18081`。
- Android 控制面:WireGuard App `jp-android-01`,`10.66.0.101`。
- 远程控制:`zhandroid-control` on `10.66.0.101:2022`。
- 远程 ADB:adbd on `10.66.0.101:5555`,仅 WireGuard 内网可达。

