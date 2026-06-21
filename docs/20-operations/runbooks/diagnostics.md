# 运维诊断命令手册

面向日常排查：**客户连不上 / 网速慢 / 出口 IP 不对 / 想确认流量是否在走**。
命令分 **Hub**、**Deprecated Mac 出口** 和 **Android 出口** 三块，每条都标注「看什么、怎么判断」。

> 登录凭据（IP / 用户 / 密码）见 [服务器访问文档](server-access.md)，本文不重复抄密码。
>
> 标 ⚠️ 的是会改状态的命令（重载 / 重启），平时排查用不到，确认要改再用。

---

## 0. 流量路径回顾

```text
客户端  --WireGuard-->  Hub(36.50.84.68, wg0/10.66.0.1)
   --WireGuard Peer 间转发 / Hub 本地 reverse proxy
   -->  出口节点(Android zhreverse; Mac 10.66.0.100 已弃用)
   --> 手机网络 --> 出口公网
```

- 客户端的 WG IP 由 Hub 按授权码分配（例如当前客户是 `10.66.0.20`）。
- Mac 出口 `10.66.0.100:1080` 已弃用,只保留历史/管理诊断;不要再作为新流量默认出口。
- Android 出口数据面是 Hub WireGuard 地址 `10.66.0.1:18081` 的 `zhreverse` proxy，公网 IP 随手机卡或 WiFi 网络变化。

---

## 1. Hub 服务器

> 系统 Ubuntu 24.04，`root` 登录：`ssh root@36.50.84.68`

### 1.1 看 WireGuard 隧道和流量（最常用 ⭐）

```bash
wg show
```

输出里每个 `peer` 看三样：

| 字段 | 含义 | 正常判断 |
| --- | --- | --- |
| `endpoint` | 对端真实公网 IP:端口 | 有值（NAT 后客户端会显示其出口 IP） |
| `latest handshake` | 最近一次握手 | **2 分钟内**＝隧道活着；几小时前＝已掉线/闲置 |
| `transfer` | 累计收 / 发字节 | 持续增长＝真的在过流量；只有几 KiB＝只握手没流量 |

**判断客户流量是否真的在走（关键技巧）**：
当前生产数据面看 Android `zhreverse` 的 `/debug/session-health`、`active_proxy_connections_by_peer` 和 Hub 上客户 peer 的 WireGuard transfer。早期“客户 peer 与 Mac peer 收发镜像”的判断只适用于已弃用的 Mac 出口路径。

机器可读版（适合脚本/快速看）：

```bash
wg show wg0 latest-handshakes   # 每个公钥的最近握手时间戳
wg show wg0 transfer            # 每个公钥的收/发字节
wg show wg0 endpoints           # 每个公钥的对端地址
```

Android 出口当前有一条 Hub 侧专用路由 MTU，用来降低 Hub 发往 Android peer 的包尺寸：

```bash
ip route show 10.66.0.101/32
```

正常应看到类似：

```text
10.66.0.101 dev wg0 scope link mtu 1120
```

### 1.2 看转发是否开启

```bash
sysctl net.ipv4.ip_forward      # 必须 = 1，否则 Hub 不转发流量
```

### 1.3 端到端验证：Hub 经 Android 出口出公网

```bash
curl -x http://10.66.0.1:18081 -s https://api64.ipify.org; echo
```

- 返回手机运营商 IP,且不是 Hub VPS `36.50.84.68` = Android 出口链路通、出口 IP 正确。
- 卡住/超时 = `zhreverse` Hub 服务、Android reverse session 或手机目标拨号路径异常。

Deprecated Mac 出口只在排查历史路径时使用：

```bash
curl -x http://10.66.0.100:1080 -s https://api.ipify.org; echo
```

### 1.4 看 WireGuard 服务和端口

```bash
systemctl status wg-quick@wg0   # 服务是否 active (running)
ss -lunp | grep 51820           # 51820/udp 是否在监听
ping -c 3 10.66.0.100           # Hub 能否 ping 通 Mac 的 WG 内网 IP
```

### 1.5 Hub 上的管理脚本（`/opt/jp-gateway/scripts/`）

```bash
/opt/jp-gateway/scripts/status.sh          # 综合状态
/opt/jp-gateway/scripts/diagnostics.sh     # 诊断
/opt/jp-gateway/scripts/add-peer.sh        # ⚠️ 新增 Peer（客户/出口）
/opt/jp-gateway/scripts/remove-peer.sh     # ⚠️ 删除 Peer
/opt/jp-gateway/scripts/reload-wg.sh       # ⚠️ 重载 WireGuard 配置
```

### 1.6 配置文件位置

```bash
cat /opt/jp-gateway/wireguard/wg0.conf     # 源配置（事实来源）
cat /etc/wireguard/wg0.conf                # 运行时配置
```

> 注意：`80/tcp` 被 Docker 的 `linuxserver/librespeed` 占用，属正常，不是异常。

---

## 2. Deprecated 日本 Mac 出口节点

> 系统 macOS（Apple Silicon），`maruichao` 登录：`ssh maruichao@100.80.36.89`
> 地址是 **Tailscale 地址**，本机要在同一 Tailnet 才连得上。

> 2026-06-15 起,Mac `10.66.0.100:1080` 已弃用。以下命令仅用于确认历史服务状态或管理内网连通性,不要把它作为新客户端、自动调度或专项验证出口。

### 2.1 看 WireGuard 隧道（最常用 ⭐）

```bash
sudo /opt/homebrew/bin/wg show
```

判断同 Hub 的 1.1：看对端（这里对端是 Hub）`latest handshake` 和 `transfer`。

```bash
ifconfig utun7                  # WireGuard 接口，应有 10.66.0.100
```

### 2.2 看 sing-box 代理内核

```bash
pgrep -fl sing-box                          # 进程在不在
sudo lsof -nP -iTCP:1080 -sTCP:LISTEN       # 1080 端口是否在监听
curl -x http://10.66.0.100:1080 -s https://api.ipify.org; echo   # 自测，应回 118.158.252.9
```

### 2.3 看日志（⭐）

```bash
ls -la /usr/local/var/log/zhvpn/
tail -n 80 /usr/local/var/log/zhvpn/*.log
tail -f  /usr/local/var/log/zhvpn/*.log     # 实时跟踪，边连边看
```

### 2.4 看开机自启服务（LaunchDaemon）

```bash
sudo launchctl print system/com.zongheng.zhvpn.wireguard
sudo launchctl print system/com.zongheng.zhvpn.sing-box
```

输出里重点看 `state = running` 和 `last exit code`（非 0 表示上次异常退出）。

⚠️ 需要重启服务时：

```bash
# 重启（先 kickstart -k 强制重拉）
sudo launchctl kickstart -k system/com.zongheng.zhvpn.wireguard
sudo launchctl kickstart -k system/com.zongheng.zhvpn.sing-box

# 彻底卸载 / 重新装载（改了 plist 才需要）
sudo launchctl bootout   system /Library/LaunchDaemons/com.zongheng.zhvpn.sing-box.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.zongheng.zhvpn.sing-box.plist
```

### 2.5 看出口公网 IP

```bash
curl -s https://api.ipify.org; echo         # Mac 实际出公网的住宅 IP
```

### 2.6 配置文件与启动脚本

```bash
# WireGuard 配置
cat /Users/maruichao/.zhvpn/wireguard/mac-mini.conf       # 工作配置
cat /usr/local/etc/zhvpn/wireguard/mac-mini.conf          # 固化配置

# sing-box 配置
cat /Users/maruichao/.zhvpn/sing-box-mac-egress.json      # 工作配置
cat /usr/local/etc/zhvpn/sing-box/mac-egress.json         # 固化配置

# 启动脚本
cat /usr/local/sbin/zhvpn-wireguard-up.sh
cat /usr/local/sbin/zhvpn-sing-box-run.sh
```

---

## 3. Android 出口节点

> 当前 Android 出口数据面已迁到 `zhreverse`:Android 主动反连 Hub,Hub 侧暴露 `10.66.0.1:18081` HTTP CONNECT proxy。
> WireGuard App 仍作为内网控制面使用,不是主要公网出口数据面。

### 3.1 Hub 侧验证 Android 出口

```bash
scripts/check-android-reverse-egress.sh
curl -s http://10.66.0.1:18081/debug/session-health
./scripts/measure-android-tail-latency.ps1 -Runs 50
curl -s 'http://10.66.0.1:18081/debug/tunnel-bench?bytes=20000000&streams=1'
curl -s 'http://10.66.0.1:18081/debug/tunnel-bench?bytes=20000000&streams=2'
curl --proxy http://10.66.0.1:18081 \
  --proxy-header 'X-ZH-Striped-Streams: 2' \
  -L -o /dev/null \
  -w "code=%{http_code} bytes=%{size_download} bps=%{speed_download} seconds=%{time_total}\n" \
  "https://speed.cloudflare.com/__down?bytes=50000000"
curl -x http://10.66.0.1:18081 -s https://api64.ipify.org; echo
curl -L --max-time 30 -x http://10.66.0.1:18081 -o /dev/null \
  -w "code=%{http_code} bytes=%{size_download} bps=%{speed_download} seconds=%{time_total}\n" \
  "https://speed.cloudflare.com/__down?bytes=50000000"
```

若 Hub 本机 curl 能通,但 Windows 客户端 `zhvpn.exe status` 获取出口 IP 失败,检查 UFW 是否允许 WireGuard 客户端访问 Hub proxy:

```bash
ufw status verbose
iptables -L ufw-user-input -n -v --line-numbers | grep 18081
```

正常应有类似规则:

```text
10.66.0.1 18081/tcp on wg0 ALLOW IN Anywhere
```

判断：

- 返回公网 IP 代表代理可用。
- `/debug/session-health` 返回 `session_count`、每条 session 的 `active_streams`、`consecutive_failures`、`ewma_command_rtt_ms` 和 `scheduler_score_ms`；两条 Android 反连都在线时 `session_count` 应为 2。新版本还返回 `active_proxy_connections_peak`、`active_proxy_connections_peak_by_peer` 和 `proxy_metrics`,用于观察最近 CONNECT 的 setup、Android target dial、首字节、总时长 p50/p95/p99、失败数和上下行字节。诊断接口受 `debug_allowed_cidrs` 保护,生产应收窄到 Hub 本机/管理员 peer,不要直接对普通客户端开放 `/debug/tunnel-bench`。
- `measure-android-tail-latency.ps1` 从 Hub 侧走 Android proxy 多轮请求小 HTTPS 目标,输出 curl 视角的 `appconnect`、`starttransfer`、`total` p50/p95/p99,并拉取 `/debug/session-health` 的 Hub 侧滚动指标;适合排查网页小请求尾延迟和发热前的并发峰值。
- `/debug/tunnel-bench` 只测 Android 到 Hub 的 reverse tunnel 回传吞吐,不访问公网目标;用 `streams=1` vs `streams=2` 判断多 session 并行是否真的提高隧道腿容量。
- `X-ZH-Striped-Streams: 2` 是实验性 per-request 开关,只用于验证大下载是否能吃到双 reverse stream 回传收益;默认代理请求不会启用。
- 手机卡场景速度主要受手机上行到 Hub 限制。
- WiFi 场景速度通常明显高于手机卡场景，但仍可能受 Android WireGuard 发包波动影响。

### 3.2 Windows 一键健康检查

`check-android-egress-health.ps1` 仍可检查 WireGuard 控制面和 Hub 侧 reverse proxy。Android 生产数据面优先在 Hub 上运行：

```bash
./scripts/check-android-reverse-egress.sh
```

脚本从 Hub 侧检查 Android 出口，不依赖 ADB。重点输出：

- 当前公网出口 IP。
- 1MB 下载探针速度。
- reverse proxy 是否能经 Android 出公网。
- Hub `zhreverse` 的 session 健康摘要（新二进制支持；旧版本只 WARN）。

需要顺手测速时：

```powershell
.\scripts\check-android-egress-health.ps1 -Benchmark
```

### 3.3 Windows 一键多轮测速

在仓库根目录运行：

```powershell
.\scripts\measure-android-egress.ps1 -Runs 5
```

脚本会从 Hub 侧走 `10.66.0.1:18081` 连续测速，并输出平均、最小、最大 Mbps。
该脚本不依赖 ADB，适合手机不在身边但 Android 出口仍在线时使用。

### 3.4 Windows 一键尾延迟测速

在仓库根目录运行：

```powershell
.\scripts\measure-android-tail-latency.ps1 -Runs 50
```

脚本默认测试 `api64.ipify.org`、Cloudflare 1KB 下载和 `cdn-cgi/trace`,每个目标顺序跑 50 次,输出 `appconnect_ms`、`starttransfer_ms`、`total_ms` 的 p50/p95/p99/max。它还会读取 Hub `proxy_metrics`,用于对照 Hub 看到的 `first_byte_latency_ms`、失败数和并发峰值。若要对比实验性 striped CONNECT,可加 `-Striped`,但日常网页体验默认看普通 CONNECT。

### 3.5 不用 ADB 远程控制 Android

目标方案见 [egress/android-control](../../../egress/android-control/README.md)：手机端由 Magisk `service.d` 拉起 watchdog，watchdog 保证 Go SSH 控制面 `zhandroid-control` 只监听 WireGuard 内网 `10.66.0.101:2022`，并且只允许密钥登录。

从 Hub 侧连接手机：

```bash
ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101
```

从已经通过 VPN/WireGuard 进入 `10.66.0.0/24` 的管理机直连手机：

```powershell
ssh -i $env:USERPROFILE\.ssh\zhandroid_control_local -p 2022 root@10.66.0.101
```

TCP ADB 也可以从 Hub 侧临时使用,端口只允许 WireGuard 内网来源：

```bash
timeout 3 bash -lc '</dev/tcp/10.66.0.101/5555' && echo adb-tcp-open
```

登录后常用控制命令：

```sh
ps -A -o PID,PPID,ARGS | grep zhreverse
tail -80 /data/local/tmp/zhreverse-egress.log
sh /data/adb/service.d/99-zhreverse-egress.sh
tail -80 /data/local/tmp/zhandroid-control.log
tail -80 /data/local/tmp/zhadb-tcp.log
/data/adb/zhandroid/sim-info.sh
```

安全边界：

- `zhandroid-control` 必须只绑定 `10.66.0.101:2022`，不要监听 `0.0.0.0`。
- 只用 SSH key 登录，禁止密码登录。
- Hub 控制面 SSH 默认使用 `/root/.ssh/zhandroid_control_known_hosts` 和 `StrictHostKeyChecking=accept-new`；若手机控制面 host key 重新生成,需要清理该 known_hosts 中对应记录或临时回滚 `ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY=no`。
- Hub bootstrap 里的 Android 运营商名探测默认缓存 300 秒(`ZHHUB_ANDROID_CARRIER_CACHE_SECONDS=300`),避免客户端高频 bootstrap 时每次都 SSH 手机。设为 `0` 可禁用动态探测。
- `zhandroid-control` 当前生产由 watchdog 等 `tun0` 地址就绪后以 `-freebind=false` 启动；如果日志出现大量 `accept4: invalid argument`，说明仍有旧进程或旧 watchdog，需要杀掉后重启 `/data/adb/zhandroid/watchdog.sh`。
- WireGuard App 的“授权外部控制”必须开启；watchdog 用 root broadcast 拉起 `jp-android-01`。`tun0` 长时间缺失时,watchdog 会从单纯 `SET_TUNNEL_UP` 升级为 `SET_TUNNEL_DOWN` + `SET_TUNNEL_UP` bounce；bounce 时应先等 `tun0` 地址消失再 UP，避免出现“有 `tun0` 但无新握手”的半坏状态。
- Android 双网络 POC 下 watchdog 默认 `DISABLE_WIFI=0`,不再强制关闭 Wi-Fi；若临时改成 `1`,会每 5 分钟尝试 `svc wifi disable`,可能破坏 `wlan0` 主隧道。
- 带内控制依赖 WireGuard 隧道在线；手机没电、关机、无网、隧道未起时仍需要物理接触或 ADB 兜底。

一键换 IP 若报 Hub 未能触发 Android 控制面,先确认是不是控制隧道本身没起来:

```bash
# Hub 上看 API 错误
journalctl -u zhhub.service --since '1 hour ago' --no-pager | grep rotate-ip | tail -40

# Hub 到 Android 控制面的当前连通性
ping -c 3 -W 1 10.66.0.101
nc -vz -w 2 10.66.0.101 2022

# 无副作用验证:控制面能 exec,rotate-ip 脚本语法 OK
ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101 'echo control-exec-ok'
ssh -i /root/.ssh/zhandroid_control_hub -p 2022 root@10.66.0.101 'sh -n /data/adb/zhandroid/rotate-ip.sh && echo rotate-script-syntax-ok'
```

若 Hub 日志是 `ssh: connect to host 10.66.0.101 port 2022: Connection timed out`,优先按 Android 本机检查 `tun0 / 10.66.0.101` 和 `/data/local/tmp/zhandroid-control.log`。日志中连续出现 `control deferred; 10.66.0.101 not present yet` 代表 WireGuard 控制隧道未建立,此时不是 `rotate-ip.sh` 本身坏了,而是 Hub 根本连不到手机控制面。

### 3.5 Android 本机检查

ADB 可用时：

```powershell
$adb="$env:LOCALAPPDATA\Android\Sdk\platform-tools\adb.exe"
& $adb shell su -c "ip route get 36.50.84.68"
& $adb shell su -c "ps -A -o PID,PPID,ARGS | grep zhreverse"
& $adb shell su -c "tail -80 /data/local/tmp/zhreverse-egress.log"
& $adb shell su -c "sed -n '1,40p' /data/adb/zhreverse/client.yaml"
& $adb shell su -c "ip addr show tun0 | grep 10.66.0.101"
```

重点看：

- `ip route get 36.50.84.68` 是走 `rmnet_data*` 还是 `wlan0`。
- 日志中是否有 `connected to reverse tcp server`。
- 当前 `connections` 是否为预期值（Pixel 当前生产为 2，两条反向隧道会话）。
- Hub `zhreverse-hub.service` 启动日志是否显示 `max_proxy_connections=96 max_proxy_connections_per_client=48`。
- WireGuard App 是否创建了 `tun0 / 10.66.0.101`。
- 若 `tun0` 缺失,watchdog 会最多每 120s 发一次 WireGuard App `SET_TUNNEL_UP` intent;若 `tun0` 存在但 Hub 内网 ping 失败,watchdog 会 `SET_TUNNEL_DOWN`,等待 `10.66.0.101` 地址消失,再 `SET_TUNNEL_UP` 强制重拨。可看 `/data/local/tmp/zhandroid-control.log` 中的 `wireguard unhealthy` 记录。

### 3.6 当前已知性能判断

- 手机 App 测到的高速下载不等于出口可用下载速度。
- 作为出口时，电脑下载需要手机把数据上传回 Hub，因此手机上行是关键瓶颈。
- 若仍看到 `zhandroid-egress`、`dxreverse` 或 `99-dxreverse-egress.sh.disabled` 进程,说明旧服务残留被误启动;当前默认应只有 `99-zhreverse-egress.sh` 和 `zhreverse client`。

---

## 4. 一分钟快速体检流程

按这个顺序走，能快速定位问题在哪一段：

```bash
# ① 在 Hub 上：客户和 Android 控制面/zhreverse 是否在线？流量在涨吗？
wg show
curl -s http://10.66.0.1:18081/debug/session-health

# ② 在 Hub 上：转发开着吗？端到端出口通吗？
sysctl net.ipv4.ip_forward
curl -x http://10.66.0.1:18081 -s https://api64.ipify.org; echo

# ③ 只有历史 Mac 路径要查时，再上 Mac 看 sing-box 和日志
sudo /opt/homebrew/bin/wg show
pgrep -fl sing-box
tail -n 50 /usr/local/var/log/zhvpn/*.log
```

| 现象 | 大概率原因 |
| --- | --- |
| Hub 上客户 peer 无握手 / 握手很久前 | 客户端没启动、网络不通、或客户端配置/密钥不对 |
| 客户有握手但 `transfer` 不涨 | 客户连上了但没真正走流量（浏览器没设代理？） |
| Hub `curl -x 10.66.0.1:18081` 超时 | Hub `zhreverse` 服务、Android reverse session 或手机目标拨号路径异常 |
| 仍有新 token 指向 `10.66.0.100:1080` | 配置落在已弃用 Mac 出口;应改为 Android `10.66.0.1:18081` |
| Android 手机卡直连快但代理慢 | 多半是手机上行到 Hub 慢，不是手机下行慢 |
| Android 日志大量 `message too long` | Android WireGuard/sing-box 发包路径仍需优化 |
| v4-only 站点经代理卡 15s 后 TLS 失败 | Rakuten IPv4/CGNAT/F5 侧故障;这是手机 IPv4 出口真实异常,不要改由 Hub 直拨 |
| v4-only 站点出口 IP 变成 `36.50.84.68` | 异常:Hub 不应作为出口兜底;检查 `zhreverse` 是否已部署忽略 `v4_only_direct` 的版本 |
| 一键换 IP 报「Hub 未能触发 Android 控制面换 IP」 | zhhub 找不到控制面私钥;查 `journalctl -u zhhub.service | grep rotate-ip` 的 `control key unavailable` 路径,核对 `ZHHUB_ANDROID_CONTROL_KEY` 指向真实存在的 `/root/.ssh/zhandroid_control_hub` |
| 一键换 IP 日志为 `ssh: connect to host 10.66.0.101 port 2022: Connection timed out` | Android 控制隧道未在线;查 Hub `ping/nc 10.66.0.101:2022`、手机 `ip addr show tun0`、`zhandroid-control.log` 是否连续 `control deferred` |
| 客户端提示授权码正在其他网络使用 | 同一 token 正在另一个公网来源 bootstrap，等待约 30 秒或先断开另一台设备 |
| `ip_forward = 0` | Hub 没开转发，流量到 Hub 就断 |

---

## 4.1 客户端重复与 token 冲突排查

本机是否启动了两个客户端，优先在 Windows 看监听端口和进程树：

```powershell
Get-Process zhvpn,zhvpn-desktop -ErrorAction SilentlyContinue
netstat -ano | findstr 7890
```

正常形态是一个 `zhvpn-desktop.exe` 加一个长期运行的 `zhvpn.exe __engine`，本地只监听 `127.0.0.1:7890`。短暂的 `zhvpn.exe status --json` 子进程可以出现，但新版 GUI 会串行化状态轮询，避免前端和托盘同时刷状态造成堆积。

同一个 token 是否在不同地方登录，优先看 Hub 日志：

```bash
journalctl -u zhhub.service --since "10 min ago" --no-pager | grep -E 'bootstrap|token_in_use'
wg show wg0 endpoints
```

- `bootstrap 拒绝 ... reason=token_in_use` 表示同 token 在不同公网来源的 30 秒租约内被拒绝。
- `wg show wg0 endpoints` 里客户 peer 的 endpoint 是当前 WireGuard 最后来源。若这个 IP 等于本机直连公网 IP，不能单独判断为异地登录。
- Hub 只信任本机或内网反代传入的 `X-Forwarded-For`；公网客户端伪造 XFF 不会影响 token 冲突判断。

---

## 5. 历史基线（2026-06-03 实测,Mac 出口已弃用）

留作对照，知道「正常」长什么样：

- Hub `wg show`：
  - 客户 `10.66.0.20`（端点为国内 IP）：握手 1 分钟内，收 124 MiB / 发 149 MiB。
  - Mac `10.66.0.100`（端点 `118.158.252.9`）：握手 1 分钟内，收 149 MiB / 发 124 MiB（与客户镜像对称）。
- Hub `net.ipv4.ip_forward = 1`。
- Hub `curl -x http://10.66.0.100:1080 https://api.ipify.org` → `118.158.252.9`。

> 提示：服务器访问文档里的 Peer 表可能滞后，排查时以 `wg show wg0` 的实时结果为准。
