# 服务器访问文档

> 敏感信息：本文档包含服务器登录凭据。请保持私有，不要提交到公开仓库。

## Hub 服务器

- 角色：流量 Hub / WireGuard 中转服务器
- 公网 IP：`36.50.84.68`
- SSH 用户：`root`
- SSH 密码：已省略。优先使用免密 SSH。
- SSH 登录命令：

```bash
ssh root@36.50.84.68
```

- 免密登录：已为开发机 `xuotq@mizuki`（公钥 `ssh-ed25519 ...xuotq@mizuki`）配置免密。
  - 公钥已写入 Hub 的 `~/.ssh/authorized_keys`，可直接 `ssh root@36.50.84.68` 无需密码。
  - 配置日期：2026-06-05。
  - 主机键指纹（ssh-ed25519）：`SHA256:wtFvvxp8XoiLYFJaka/dY5Jg4ciLwBksha6W33b8sYI`。
  - 如需在新机器复制：把该机公钥追加到 Hub `~/.ssh/authorized_keys` 即可。

## Hub 授权 API 服务（zhhub）

- 服务：`zhhub.service`（2026-06-11 从旧 `dxhub.service` 迁移完成，dx→zh 收尾）。
- 二进制 / tokens：`/opt/zongheng/zhhub/zhhub`、`/opt/zongheng/zhhub/tokens.yaml`。
- 监听：`0.0.0.0:18080`（HTTP）；提供 `/healthz`、`/api/client/bootstrap`、`/api/client/rotate-ip`。
- 关键 env：`ZHHUB_TOKENS`、`ZHHUB_LISTEN`、`ZHHUB_ANDROID_CONTROL_KEY=/root/.ssh/zhandroid_control_hub`、`ZHHUB_ANDROID_CONTROL_KNOWN_HOSTS=/root/.ssh/zhandroid_control_known_hosts`、`ZHHUB_ANDROID_CARRIER_CACHE_SECONDS=300`、`ZHHUB_TOKEN_LEASE_SECONDS=30`。
- 一键换 IP 依赖 `ZHHUB_ANDROID_CONTROL_KEY` 指向的私钥能登手机控制面 `10.66.0.101:2022`。
- 旧 dx 服务 / 目录 / key 已归档到 Hub `/root/dx-attic-20260611/`（可回滚，确认稳定后再彻底删）。

## 日本 Mac 出口节点（Deprecated）

- 角色：历史日本本地出口节点 / 管理内网对象
- 连接地址：`100.80.36.89`
- 说明：该地址是 Tailscale 地址，不是普通局域网地址。
- SSH 用户：`maruichao`
- SSH 密码：已省略。优先使用既有可信通道或密码管理器。
- SSH 登录命令：

```bash
ssh maruichao@100.80.36.89
```

当前状态：

> 2026-06-15 起,Mac `10.66.0.100:1080` 出口路线已弃用。它不再作为新客户端、自动调度、专项爬虫验证或长期出口池方案;只保留为历史配置、管理内网和必要时的只读诊断对象。新数据面默认使用 Android `zhreverse` Hub 入口 `10.66.0.1:18081`。

- 主机名：`maruichaodeMac-mini.local`
- 系统：macOS 26.5
- 本地网络接口：`en1`
- 本地 IP：`192.168.68.97`
- 当前公网出口 IP：`118.158.252.9`
- WireGuard 工具：已通过 Homebrew 安装
- WireGuard 配置：`/Users/maruichao/.zhvpn/wireguard/mac-mini.conf`
- WireGuard 固化配置：`/usr/local/etc/zhvpn/wireguard/mac-mini.conf`
- WireGuard 接口：`utun7`
- WireGuard IP：`10.66.0.100/24`
- 远端代理内核：`sing-box`
- 远端代理配置：`/Users/maruichao/.zhvpn/sing-box-mac-egress.json`
- 远端代理固化配置：`/usr/local/etc/zhvpn/sing-box/mac-egress.json`
- 远端代理监听：`10.66.0.100:1080`
- 远端代理类型：mixed，也就是同时支持 HTTP 和 SOCKS5
- WireGuard 开机启动：`/Library/LaunchDaemons/com.zongheng.zhvpn.wireguard.plist`
- 远端代理开机启动：`/Library/LaunchDaemons/com.zongheng.zhvpn.sing-box.plist`
- WireGuard 启动脚本：`/usr/local/sbin/zhvpn-wireguard-up.sh`
- 远端代理启动脚本：`/usr/local/sbin/zhvpn-sing-box-run.sh`
- 日志目录：`/usr/local/var/log/zhvpn`

当前验证结果：

- Hub 可以 ping 通 Mac 的 WireGuard IP：`10.66.0.100`
- Hub 可以访问 Mac 远端代理：`10.66.0.100:1080`
- Hub 直连公网 IP：`36.50.84.68`
- Hub 通过 Mac 代理访问公网 IP：`118.158.252.9`
- Hub 通过 Mac 代理访问 `https://www.yahoo.co.jp` 成功
- 以上验证结果仅作为历史路径诊断;新客户端和专项验证不应依赖该出口。

常用检查命令：

```bash
# Mac 上检查 WireGuard
sudo /opt/homebrew/bin/wg show

# Mac 上检查 LaunchDaemon
sudo launchctl print system/com.zongheng.zhvpn.wireguard
sudo launchctl print system/com.zongheng.zhvpn.sing-box

# Hub 上检查 Mac 远端代理出口
curl -x http://10.66.0.100:1080 https://api.ipify.org
curl --socks5-hostname 10.66.0.100:1080 https://api.ipify.org
```

## Android 手机出口节点

- 角色：Android 手机运营商出口节点（运营商名由手机侧上报，token 配置仅作兜底）
- 当前数据面设备：Google Pixel 7a（`lynx`）
- 控制面 WireGuard IP：`10.66.0.101`
- 控制面 SSH：`10.66.0.101:2022`（`zhandroid-control`,仅公钥,Hub 可登录）
- 控制面 TCP ADB：`10.66.0.101:5555`（仅允许 `tun0` / `10.66.0.0/24`）
- 数据面：`zhreverse` 反向 TCP/yamux（2026-06-14 双网络 POC:隧道优先绑 `wlan0`,失败 fallback 到 `rmnet1`;目标 TCP/DNS 绑 `rmnet1`）
- Hub 侧代理入口：`10.66.0.1:18081`
- Hub reverse TCP 监听：`0.0.0.0:39093/tcp`
- Android reverse endpoint：Android 主动连 Hub,无手机入站端口;当前 POC 正常态本机 socket 为 `192.168.3.3%wlan0 -> 36.50.84.68:39093`,fallback 态为 `%rmnet1 -> 36.50.84.68:39093`
- Hub 防火墙：UFW 允许 `wg0 -> 10.66.0.1:18081/tcp`
- 旧代理：`10.66.0.101:1080` / `zhandroid-egress` 已从 Android 生产入口拆除

当前部署：

| 项 | 路径 |
| --- | --- |
| Hub binary | `/opt/zongheng/zhreverse/zhreverse` |
| Hub config | `/etc/zongheng/zhreverse/server.yaml` |
| Hub token | `/etc/zongheng/zhreverse/token` |
| Hub QUIC cert/key（仅 QUIC 回滚用） | `/etc/zongheng/zhreverse/server.crt` / `/etc/zongheng/zhreverse/server.key` |
| Hub service | `/etc/systemd/system/zhreverse-hub.service` |
| Android binary | `/data/adb/zhreverse/bin/zhreverse` |
| Android config | `/data/adb/zhreverse/client.yaml` |
| Android token | `/data/adb/zhreverse/token` |
| Android service | `/data/adb/service.d/99-zhreverse-egress.sh` |
| Android control binary | `/data/adb/zhandroid/bin/zhandroid-control` |
| Android control watchdog | `/data/adb/zhandroid/watchdog.sh` |
| Android control service | `/data/adb/service.d/98-zhandroid-control.sh` |
| Android WG-only TCP ADB service | `/data/adb/service.d/97-zhadb-tcp-wg-only.sh` |
| Android authorized keys | `/data/adb/zhandroid/.ssh/authorized_keys` |

当前验证结果：

- `zhreverse-hub.service` 已启用并运行。
- Hub 监听 `39093/tcp` 和 `10.66.0.1:18081`。
- Pixel 7a 已安装官方 WireGuard App `1.0.20260315`,导入 `jp-android-01`,地址 `10.66.0.101/24`,MTU `1120`,远程控制开关已开启。
- Hub `wg0` 中 `10.66.0.101/32` 已切到 Pixel 新 peer;2026-06-11 验证最新握手、Hub ping、控制 SSH 均正常。
- `zhandroid-control` 生产路径等 `tun0` 地址就绪后绑定 `10.66.0.101:2022`,不使用 `IP_FREEBIND`,避免 Pixel 上 `accept4: invalid argument`。若 `tun0` 长时间缺失,watchdog 先发 WireGuard `SET_TUNNEL_UP`;持续异常超过 10 分钟后升级为 `SET_TUNNEL_DOWN` + `SET_TUNNEL_UP` bounce。
- Hub 可通过 `/root/.ssh/zhandroid_control_hub` 登录 `root@10.66.0.101:2022`;本机可用 `~/.ssh/zhandroid_control_local` 作为另一把授权钥匙。
- TCP ADB 已启用在 `5555`,并由 `97-zhadb-tcp-wg-only.sh` 用 iptables 限制为 WireGuard 内网来源。
- Android 当前 `transport: tcp`、`connections: 2`(2026-06-11 起,两条 yamux 会话互为兜底,单条隧道冻死不再连坐全部代理连接)、`address_family: ipv6`;`client.server_cert_sha256` 保留用于 QUIC 回滚。
- Android 当前双网络 POC 配置:`tunnel_bind_interface: wlan0`,`tunnel_fallback_interface: rmnet1`,`tunnel_fallback_after_failures: 3`,`tunnel_primary_retry_interval: 1m`,`target_bind_interface: rmnet1`。watchdog 默认不再强制关闭 Wi-Fi(`DISABLE_WIFI=0`),避免破坏 `wlan0` 主隧道。正常态 Hub `/debug/session-health` 应看到住宅公网 IPv4 `60.124.42.38:*`;经代理访问 `https://api6.ipify.org` 应回手机蜂窝 `240b:c010:...`。若 WiFi/家宽断开,Android -> Hub 隧道会在连续失败后 fallback 到蜂窝 `rmnet1`;目标网站出口仍为蜂窝。回滚为恢复 `/data/adb/zhreverse/client.yaml.bak-20260614-tunnel-fallback` 或移除绑定/fallback 字段,再 `pkill zhreverse`。
- Hub 当前 `resolve: client`(2026-06-10 起):目标域名在手机侧解析并优先 IPv6 直拨,绕开乐天 F5 BIG-IP 透明代理故障率高的 v4 侧,详见 `docs/90-history/worklogs/2026-06-10-pixel-7a-speed-audit.md`。
- Hub 当前 `max_proxy_connections=96`、`max_proxy_connections_per_client=48`,用于保护 Android 手机出口免受客户端突发并发拖死,同时避免误伤浏览器常驻连接。
- Hub 当前 `proxy_idle_timeout=2m`,用于回收 FAST/浏览器异常中断后残留的空闲 CONNECT 隧道,避免单客户端并发槽被长期占满。
- Hub 不作为出口兜底:`v4_only_direct` 已废弃并被服务端忽略。目标无 AAAA(或为 IPv4 字面量)时仍应交给手机出口;若 Rakuten IPv4/CGNAT/F5 路径故障,就如实表现为 IPv4 出口异常,不能改由 Hub VPS `36.50.84.68` 出口。
- UFW 已允许 WireGuard 客户端访问 `10.66.0.1:18081/tcp`。
- Hub 日志显示 Pixel Android 2 条 TCP reverse session 已连接(`connections: 2`)。
- Android 当前仅运行 `99-zhreverse-egress.sh` supervisor 和 `zhreverse client`。
- Hub 经 reverse proxy 出口 IP：以 `curl --proxy http://10.66.0.1:18081 https://api6.ipify.org`(v6 主路径)与 `https://api.ipify.org`(v4)实时结果为准。v6 应回 `240b:`(乐天);v4 走手机乐天 v4(`133.106.x` 或 `210.157.x` 等),**时好时坏是常态**(F5 v4 侧故障率时变),失败按 WARN 对待;但 v4 若回 Hub VPS `36.50.84.68` 即 hub-fallback 回归,按 FAIL 处理。2026-06-11 起手机端部署 TLS 首飞看门狗,v4 坏窗口下自动重拨重放(手机日志 `redialing` 可观测)。
- Android 客户端 token 当前应绑定 `egress.proxy_addr=10.66.0.1:18081`;旧 `10.66.0.101:1080` 不再分配给 Android 客户端。

常用检查命令：

```bash
systemctl status zhreverse-hub.service
journalctl -u zhreverse-hub.service -n 50 --no-pager
scripts/check-android-reverse-egress.sh   # v6 主路径 FAIL 级,v4 兜底 WARN 级
curl --proxy http://10.66.0.1:18081 https://api6.ipify.org   # v6 主路径,应回 240b:
curl --proxy http://10.66.0.1:18081 https://api.ipify.org    # v4 兜底,时好时坏
ssh -i ~/.ssh/zhandroid_control_local -p 2022 root@10.66.0.101 \
  'ps -A -o PID,PPID,ARGS | grep -E "zhreverse|zhandroid-egress|99-zh" | grep -v grep || true'
ssh -i ~/.ssh/zhandroid_control_local -p 2022 root@10.66.0.101 \
  'ss -ntp | grep zhreverse || true'   # POC:隧道应显示 %wlan0,目标连接应显示 %rmnet1
```

## 当前服务器状态

检查日期：2026-06-03。

- 主机名：`jp-proxy.ruichao.dev`
- 系统：Ubuntu 24.04.3 LTS
- 内核：Linux 6.8
- WireGuard 接口：`wg0`
- Hub 的 WireGuard 内网 IP：`10.66.0.1/24`
- WireGuard 监听端口：`51820/udp`
- IPv4 转发：已开启
- WireGuard 服务：`wg-quick@wg0`，已启用并正在运行
- 防火墙：`ufw` 已启用,默认拒绝入站;显式放行 SSH、WireGuard、zhhub bootstrap、zhreverse TCP 和 `wg0` 上的 `10.66.0.1:18081/tcp`
- Docker：正在运行 `linuxserver/librespeed`，占用 `80/tcp`

## 当前 WireGuard Peer

配置源文件：

- `/opt/jp-gateway/wireguard/wg0.conf`

运行时配置：

- `/etc/wireguard/wg0.conf`

管理脚本：

- `/opt/jp-gateway/scripts/setup.sh`
- `/opt/jp-gateway/scripts/add-peer.sh`
- `/opt/jp-gateway/scripts/remove-peer.sh`
- `/opt/jp-gateway/scripts/reload-wg.sh`
- `/opt/jp-gateway/scripts/status.sh`
- `/opt/jp-gateway/scripts/diagnostics.sh`

当前已配置的 Peer：

| 名称 | WireGuard IP | 状态 |
| --- | --- | --- |
| `windows-client-1` | `10.66.0.10/32` | 之前有握手记录，当前无法 ping 通 |
| `mac-mini` | `10.66.0.100/32` | 已握手，Hub 可 ping 通；出口数据面已弃用 |
| `pixel-7a-android-control` | `10.66.0.101/32` | 已握手，Hub 可 ping 通，控制 SSH/TCP ADB 可达 |
| `admin-innernet` | `10.66.0.40/32` | 管理专用内网 peer,只路由 `10.66.0.0/24`,不承载公网出口流量 |

## 当前模式

服务器当前是 Android `zhreverse` 数据面 + WireGuard 管理内网：

- 允许 `wg0` 内部 Peer 之间互相转发。
- 客户端当前只把 `10.66.0.0/24` 路由进 WireGuard。
- 新出口数据面应使用 Hub 侧 `10.66.0.1:18081` Android reverse proxy。
- Mac `10.66.0.100:1080` 只保留历史诊断,不再作为默认出口。

目标产品的下一步：

- 中国客户端连接到 Hub。
- 中国客户端把代理流量转发到 Android `zhreverse`。
- Android 手机出口把目标访问从手机运营商网络发出。
