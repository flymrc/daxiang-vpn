# 运维诊断命令手册

面向日常排查：**客户连不上 / 网速慢 / 出口 IP 不对 / 想确认流量是否在走**。
命令分 **Hub**、**Deprecated Mac 出口** 和 **Android 出口** 三块，每条都标注「看什么、怎么判断」。

> 登录凭据（IP / 用户 / 密码）见 [服务器访问文档](server-access.md)，本文不重复抄密码。
>
> 标 ⚠️ 的是会改状态的命令（重载 / 重启），平时排查用不到，确认要改再用。

---

## 0. 流量路径回顾

2026-10-07 第八波兼容 canary 的真实 smoke 已通过，见[当前部署记录](../../90-history/worklogs/2026-10-07-wave8-compat-deployment.md)。新版独立 CLI 在原 `%LOCALAPPDATA%/ZonghengVPN/bin/zhvpn.exe` 路径，代理仍7890。只读验收示例：

```powershell
& "$env:LOCALAPPDATA/ZonghengVPN/bin/zhvpn.exe" version --json
& "$env:LOCALAPPDATA/ZonghengVPN/bin/zhvpn.exe" status --json --no-ip-check
curl.exe --proxy http://127.0.0.1:7890 --max-time 15 --fail https://jp-proxy.ruichao.dev/healthz
curl.exe --proxy http://127.0.0.1:7890 --max-time 15 --fail 'https://api64.ipify.org?format=json'
```

`ready`/`logging_state=healthy` 分别证明认证实例/日志状态；必须结合实际代理目标响应与Hub WG handshake/transfer验证连通。新 CLI 拒绝旧继承权限凭据；升级按部署记录先私有备份/归档、保留WG key，再生成保护ACL文件，不能递归改父目录权限。运行中的日志可能持有共享限制，优先使用认证日志health，不通过删除锁或强停writer读取。此次未验证所有IPv4/IPv6目标、真实系统代理操作或新版GUI安装包。

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

### 1.7 Hub 管理控制台

控制台部署后应由 Caddy 对公网提供 HTTPS,`zhhub` 本身只监听 localhost:

```bash
systemctl status zhhub.service
ss -ltnp | grep -E ':(18079|18080|18100)\b'
curl -s http://127.0.0.1:18079/healthz
curl -s http://127.0.0.1:18080/healthz
curl -s http://127.0.0.1:18100/admin/api/health
systemctl status caddy
caddy validate --config /etc/caddy/Caddyfile
curl -I https://jp-proxy.ruichao.dev/
curl -I https://jp-proxy.ruichao.dev/admin/
curl -I https://jp-proxy.ruichao.dev/not-found-check
```

正常判断:

- `127.0.0.1:18079` 是 Caddy 专用可信代理入口，`18080` 是迁移期兼容入口；两者健康检查都应返回 `{"status":"ok"}`。
- `127.0.0.1:18100` 有监听,但公网不应直连 `18100/tcp`。
- `/admin/api/health` 返回 `{"status":"ok"}`。
- 公网入口由 Caddy 反代;控制台访问后应进入应用内管理员登录页。
- Caddy 管理 `80/443` 自动 HTTPS;控制台上线后替代原 Librespeed 测速页。
- 当前 DNS 仍可走 Cloudflare 代理;访问 `https://jp-proxy.ruichao.dev/admin/` 预期返回前端登录页。
- `https://jp-proxy.ruichao.dev/` 和未知路径都预期 `404 Not Found`,不能返回空白 200。

> 历史状态：`80/tcp` 曾由 Docker `linuxserver/librespeed` 直接占用。2026-07-01 起该容器已停止并取消自动重启,`80/443` 交给 Caddy。

### 1.8 Hub admin SQLite 容量检查

只读检查 admin DB 和 WAL 文件大小:

```bash
du -h /opt/zongheng/zhhub/admin.db*
sqlite3 /opt/zongheng/zhhub/admin.db \
  "SELECT 'audit_events', count(*) FROM audit_events UNION ALL SELECT 'admin_login_attempts', count(*) FROM admin_login_attempts UNION ALL SELECT 'admin_sessions', count(*) FROM admin_sessions;"
sqlite3 /opt/zongheng/zhhub/admin.db \
  "SELECT min(occurred_at), max(occurred_at), count(*) FROM audit_events;"
```

正常判断:

- `audit_events` 默认最多约 `50000` 行,保留约 90 天。
- `admin_login_attempts` 默认最多约 `10000` 行,保留约 7 天。
- `admin.db-wal` 不应长期明显大于 `admin.db`;维护任务会定期 `wal_checkpoint(TRUNCATE)`。
- 若行数持续超过上限,先看 `journalctl -u zhhub.service --since '2 hours ago' --no-pager | grep 'admin db maintenance failed'`。
- 容量上限由 `ZHHUB_ADMIN_AUDIT_*`、`ZHHUB_ADMIN_LOGIN_ATTEMPT_*` 和 `ZHHUB_ADMIN_DB_MAINTENANCE_MINUTES` 控制。

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
- 2026-10-05 已部署的空闲抢占版本还返回 `proxy_preempt_idle_ms=10000` 和 `proxy_idle_preemptions`（计数为零时该字段省略）:后者持续增长说明有客户端常驻连接数顶到上限,Hub 正在关闭空闲隧道给新 CONNECT 让位;`proxy_metrics` 里 `rejected` 仍增长则说明连空闲隧道都没有,需要看是否真有大量同时活跃的连接。浏览器侧表现为 `ERR_CONNECTION_RESET`(本地 sing-box 已先回 200,再因 Hub 429 重置连接)。排查重启参数用限定启动时间窗的 `journalctl` 或 `-n 300`，不要在长期大日志中不限定时间扫描所有记录。
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

旧部署可能记录原始 `ssh: connect to host 10.66.0.101 port 2022: Connection timed out`。第三波本地实现将 SSH 路径、参数和 stderr 收敛为固定 `control_failed`，不能从这个统一错误推断“密钥缺失”或“网络超时”。先核对实际 binary 的版本，再做上面的只读连通性/脚本语法检查；不要为了补错误细节重新触发一次换 IP。Android 日志中连续出现 `control deferred; 10.66.0.101 not present yet` 表示控制隧道地址尚未建立，需结合手机当前 `tun0 / 10.66.0.101` 与 `/data/local/tmp/zhandroid-control.log` 核查。

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
| 一键换 IP 报「Hub 未能触发 Android 控制面换 IP」或固定 `control_failed` | 不能由这个错误唯一定位原因；只读核对控制密钥文件存在/权限（不打印内容）、Hub `ping/nc`、手机地址和脚本语法。第三波本地版本不再输出私钥路径或 SSH 原始 stderr |
| 旧部署日志为 `ssh: connect to host 10.66.0.101 port 2022: Connection timed out` | 优先核查 Android 控制隧道当前是否在线；查 Hub `ping/nc 10.66.0.101:2022`、手机 `ip addr show tun0`、`zhandroid-control.log`。不要把旧日志格式作为新版本必须输出的合同 |
| 客户端提示授权码正在其他网络使用 | 同一 token 正在另一个公网来源 bootstrap，等待约 30 秒或先断开另一台设备 |
| `ip_forward = 0` | Hub 没开转发，流量到 Hub 就断 |

---

## 4.1 客户端重复与 token 冲突排查

2026-10-06 开发分支开始以认证实例身份诊断引擎；先用 `zhvpn status --json --no-ip-check` 读取公开状态，端口/PID 只作辅助。`engine_legacy_state` 需要人工核验旧引擎的可执行路径、home 与真实运行身份，确认退出后归档旧记录，不能按 PID 自动强杀。`engine_identity_unverified`、`engine_control_unavailable` 和停止超时均保留恢复状态，不宣称停止成功。系统代理 journal 异常按[恢复合同](../../30-implementation/client-runtime-safety-contract.md)核对并重试；不要 dump `engine-state.json` 或运行 session 配置，它们可能含控制密钥或 WG 私钥。这些行为尚未发行。

开发分支的本地检查入口为 `pwsh -NoProfile -File scripts/check-client-safety.ps1`，GUI 依赖先在其目录安装。该入口先检查 `shared/contracts` 的五份生成投影漂移，再运行 Go test/vet、客户端/shared/reverse 与离线授权 race、SDK、前端和 Rust library 测试；任何失败即停止。设备授权与新代理租约测试使用隔离 SQLite/fake executor/合成注册表，禁止把通过结果写作生产 peer 或真实 Internet Settings 验收。

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
- 现有生产版本曾对部分私网来源接受 `X-Forwarded-For`，不能据此推断设备归属。10-07 隔离源码已修复：compat 只取 TCP 来源；trusted/Admin 仅信任精确 loopback 代理并校验完整 XFF 链，详见[HTTP 边界](../../30-implementation/steelman-http-admin-boundaries.md)。修复未部署，生产排查仍先核对运行版本和 Caddy。

---

## 4.2 运行时租约与本地门禁（开发分支）

以下是尚未部署的新协议；先确认实际运行版本再用。`zhvpn system-proxy inspect --json` 只读 WAL，`recorded` 不表示代理已生效。Acquire/Release/Recover 会改本用户代理，必须符合本次明确操作意图；release 需要回执中的 lease ID，recover 必须在原 home 引擎已停止后执行。`engine_action_result_unknown` 表示响应丢失，先 inspect 并核验同一实例，不反复 acquire；恢复失败保留 WAL 和引擎，不能手工按 PID 强杀。

本机 Roaming ACL 不符合严格 resolver 时会拒绝真实路径；先诊断 Owner/DACL/目录归属，不把增加允许 SID 或改整棵 profile ACL 当作修复。输出公开 status/receipt 即可，禁止 dump 含 HMAC 密钥的 engine-state 或完整配置。详细拒绝与恢复见 [运行时合同](../../30-implementation/steelman-runtime-integration.md)。

```powershell
pwsh -NoProfile -File scripts/check-steelman.ps1 -EvidenceDirectory <全新私有扫描目录>
pwsh -NoProfile -File scripts/build-steelman-dev.ps1 -OutputDirectory <全新开发产物目录>
```

统一门禁先运行合同/行为，再保存 Windows/Linux/Darwin 的 amd64/arm64 的 govulncheck 原始流、解析 summary、npm audit、Cargo audit 和 Windows/macOS target tree。扫描网络失败、空/损坏报告、未知发现或过期例外均拒绝；JSON scanner exit=0 不能单独证明无可达漏洞。当前例外到 2026-11-06，须届时重新核查。交叉源码扫描和交叉编译不是平台实机运行验收。

新 Hub v2 服务默认关闭；必须显式配置独立客户 interface、绝对 WG/helper 路径和 `ZHHUB_DEVICE_SUPERVISOR_BIN`。Linux supervisor 持同一执行 fence，Hub 崩溃时先关闭子树再释放；helper 自身崩溃且发现不能证明归属的 detached child 时保持 fence/degraded，需要受控人工核验，不能删 lock 文件或整体恢复旧 WG conf 解堵。此流程没有接管当前 `wg0` 或修改 RDP/管理员/手机 peer。

---

## 4.3 第二波本地边界（尚未部署）

legacy/trusted/Admin 源码默认使用 header/body 16 KiB、header 5s、read 10s、write/idle 30s。413 是正文超限，400 包括尾随 JSON 或错误 trusted XFF；请求失败不得有租约副作用。HTTP write timeout 不证明外部命令停止。核对当前部署版本和实际 Caddy 来源后再应用这些诊断，不能从开发文档改写线上事实。

Admin npm 与 desktop 分别扫描，统一 gate 显式纳入 dev/optional/peer；`NPM_CONFIG_OMIT=dev` 的单独 audit 结果不足以验收。Admin 依赖先 `npm ci`，Node 24 为本次运行基线。管理台“未知”应保留未知；rotate 未确认后不要重试，旧入口尚无跨页 durable receipt。

离线恢复先取得受保护完整快照和独立核验的 latest checkpoint，再按[规划合同](../../30-implementation/device-authority-offline-restore-plan.md)执行。程序仅比较和输出计划，源只读但会创建本次私有 scratch 副本；不执行恢复 SQL/WG，`ready_to_restore=false`。禁止把旧 snapshot 的 revoke/outbox 已完成状态当作现场数据面确认。

纯更新 verifier 只验证 staging metadata/实际产物；第四波[离线CLI](../../30-implementation/trusted-update-state.md)已在显式策略批准后持久保存水位，仍不安装。不得把未签名开发包或一份缓存receipt当作正式更新批准。

## 4.4 兼容执行与结果未知（尚未部署）

第三波源码的昂贵 HTTP 入口共享8全局/2每来源在飞名额及有限速率/1024来源表；429 `rate_limited` 和503 `resource_exhausted` 表示本次未进入 handler，按 `Retry-After` 等待。health/snapshot读取不占该池。内部 WG 在飞1个、SSH共享2个；bootstrap 5s/WG 3s、carrier 1.5s、rotate 18s/SSH 15s为本地候选，不可当作当前生产参数。

`process_supervision_unknown` 表示本次 native cleanup尚不能确认，该名额隔离保留。先核验对应服务版本和本次受管树，不按进程名/历史PID强杀，不通过重启假装完成。Linux helper本身毁坏且descendant已脱离session时，本地父进程可能无法认领它；运行库返回unknown，不承诺所有树都能停止。

Admin `rotate_state=unknown` 表示可能已经派发；普通冷却过去、页面刷新/重载都不允许重派。没有安全clear API，当前标记也不跨Hub重启持久保存；不得以重启清标记绕过结果核验。手机端后台恢复脚本必须保留，SSH超时不证明其未启动。完整限制与负例见[执行合同](../../30-implementation/legacy-control-process-budget.md)。

## 4.5 离线更新状态（尚未发行）

`zhvpn update inspect --product <产品> --platform <平台> --architecture <CPU> --channel <渠道> --json`取得受保护当前Policy revision与累计floor，和写入口共用操作锁；它不生成新的staging回执。`registration_required/incomplete/corrupt_state`不得自动删文件或重新登记以绕过历史。初始enroll与policy-approve需要独立审核Policy原始字节及明确批准的SHA-256；对候选附带公钥自行算hash不建立publisher信任。

verify只使用已批准状态，全量重验签名/期限/key和产物后提交staging水位。`commit_unknown`或输出丢失先inspect，再对同一包核验；不能补偿回旧state、执行候选或降低floor。整组旧快照恢复可能回滚本地水位，实际恢复仍须外部最新事实核验。路径/命令示例、平台与断电/内核IO限制见[状态合同](../../30-implementation/trusted-update-state.md)。

## 4.6 Provisional 清册与 observer（开发源码，尚未部署）

新版 `GET /admin/api/migration/readiness` 为 contract2。先核对实际二进制/合同版本；生产旧 contract1 不能冒充新固定分母。baseline 是批准快照，enabled 数量只是 source 状态；missing/disabled/expired/invalid 或 extra 必须显式处置，不能从分母抹除。

离线 `zhhub-campaign-register-linux-amd64` 只接受 `--db <操作者控制的绝对本地DB路径>`、`--inventory-file <已审阅普通本地文件>`、`--approve-inventory-sha256 <人工批准的原始SHA256>` 三个唯一 flag/value 对。登记是实际 SQLite 写入，本批只在合成 fixture 执行；未经单独授权不可指向生产。相同摘要幂等恢复；receipt 输出失败或 commit_unknown 时不能用更换清册/删除库重试，先确认原库，再以同一批准摘要恢复。没有 force/reset/T0 入口。

observer gap 和旧负事实不因 clean restart、后来 secure、普通 audit retention 或页面刷新消失。容量上限4096和计数溢出会拒绝报告；不可删 runs/facts 解除 NO-GO。当前没有归档协议、完整连续窗口或整库旧快照回放防护，详见[清册合同](../../30-implementation/migration-inventory.md)。

## 4.7 v2 proxy 屏障排查（第七波开发源码，未部署）

先确认实际二进制版本。Hub hosting 的 `ZHHUB_DEVICE_PROXY_PROFILE` 必须与 `ZHHUB_DEVICE_PROXY_GATE_POLICY`、`ZHHUB_DEVICE_PROXY_GATE_SOCKET` 成组；reverse 对应 `--proxy-gate-policy-file`、`--proxy-gate-control-socket`。Policy/profile/authority摘要、epoch/interface/managed scope、实际IPv4 listener与controller UID必须一致；文件为当前owner的单link有限普通文件、私有leaf与UDS目录。没有生成可信生产配置/批准安装者的自动流程，不从候选JSON自行推定信任。

`proxy_gate_enabled=true` 不等于开放。closed ACK 后先对账，再检查最终DB/WG收敛，最后取得绝对2s grant。既有 managed WG peer 仍存在但 proxy 返回503可能是正确拒绝；只有受控同路径 target 正负对照可证明通流。健康/HTTP监听不能替代撤销确认，不输出 WG dump、私钥或完整配置。

启用 gate 时 transport 必须为 `tcp` 或 `tcp-tls`；默认/显式 QUIC 返回 `proxy_gate_unsupported_transport`，在读取 policy 或绑定资源前退出。QUIC 的半关闭不能作为本片实际回收证明，不能通过禁用检查让它进入 hosting。

`proxy_gate_cleanup_unknown`、control command 超时/EOF 后须保持关闭，不能以删除socket/lock、重连、重启或扩scope绕过。已有 yamux FIN/迟到OpenStream未确认清理时，receiver拒绝freshowner/grant；Close仅表示失效，AwaitClosed才表示本批实际关闭完成。核验本次拥有的实例/资源后再处理，不关整个WG接口、reverse session或保留peer。

本地Linux验收运行 `sh scripts/check-proxy-barrier-linux.sh`，需显式可信Go PATH、unshare/ip/wg与内核WireGuard；能力缺失应非零失败。原生fixture只在自己创建的user/network namespace改路由/peer，不接触宿主或生产。Windows统一门禁另验共享race/schema和消费者；两种收据分别记录，详见[合同](../../30-implementation/device-proxy-startup-barrier.md)。

## 4.8 客户端日志与派发fence（第八波开发源码，未发行）

普通 `zhvpn status --json --no-ip-check` 返回独立logging_state；旧engine/无法取得同实例可信响应/文件IO在途为unknown，degraded只给固定logging_error_code。运行中日志失败不代表代理或WG失败，也不自动停止引擎。status不会为日志观测隐式bootstrap。healthy不保证完整历史或crash前末条已flush。

新专用child仅写自己的 `logs/engine-events-v1`，4槽和owner合计最多1 MiB，原始消息不保存；旧日志仍保留，需要人工按归属处理，不能把整个logs目录当作新配额范围。`engine_log_open/namespace` 先检查当前版本、owner/权限/单link/目录身份和未知条目，不自动chmod或删除marker重新认领。`queue_overflow/shutdown/write/sync` 为sticky；避免通过删锁/日志假装恢复正常，不粘贴私钥、control_secret或完整配置。

正常Stop须在logger唯一worker回收后才释放旧生命周期锁；文件IO卡住时日志状态和控制接口不等磁盘锁，已授权Stop/启动lease取消后的专用child3秒自退出监督覆盖外层收尾。BeforeStop恢复失败仍保留引擎，不启用强退出。固定错误类别是安全诊断，不能从没有原始栈推定具体磁盘故障原因。

受管proxy future/clear deadline在取消后拒绝，旧许可迟到完成会修复past；closed ACK需要真实Close与先前permit完成。此前获准write的kernel在途bytes不可撤回，yamux内部stream/timer回收和contextual Open仍独立问题。排查保持quarantine与retained流，不用关闭整个session绕过。正式本地门禁含同源event schema检查，Linux门禁另含本轮日志store/专用test child/普通库保持原行为的原生测试，不执行尚未支持的Linux客户端后台launch；平台结果见[第八波worklog](../../90-history/worklogs/2026-10-07-client-engine-observability.md)。

## 5. 历史基线（2026-06-03 实测,Mac 出口已弃用）

留作对照，知道「正常」长什么样：

- Hub `wg show`：
  - 客户 `10.66.0.20`（端点为国内 IP）：握手 1 分钟内，收 124 MiB / 发 149 MiB。
  - Mac `10.66.0.100`（端点 `118.158.252.9`）：握手 1 分钟内，收 149 MiB / 发 124 MiB（与客户镜像对称）。
- Hub `net.ipv4.ip_forward = 1`。
- Hub `curl -x http://10.66.0.100:1080 https://api.ipify.org` → `118.158.252.9`。

> 提示：服务器访问文档里的 Peer 表可能滞后，排查时以 `wg show wg0` 的实时结果为准。
