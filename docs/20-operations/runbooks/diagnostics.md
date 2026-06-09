# 运维诊断命令手册

面向日常排查：**客户连不上 / 网速慢 / 出口 IP 不对 / 想确认流量是否在走**。
命令分 **Hub**、**Mac 出口** 和 **Android 出口** 三块，每条都标注「看什么、怎么判断」。

> 登录凭据（IP / 用户 / 密码）见 [服务器访问文档](server-access.md)，本文不重复抄密码。
>
> 标 ⚠️ 的是会改状态的命令（重载 / 重启），平时排查用不到，确认要改再用。

---

## 0. 流量路径回顾

```text
客户端  --WireGuard-->  Hub(36.50.84.68, wg0/10.66.0.1)
   --WireGuard Peer 间转发 / Hub 本地 reverse proxy
   -->  出口节点(Mac 10.66.0.100 或 Android dxreverse)
   --> NAT / 手机网络 --> 出口公网
```

- 客户端的 WG IP 由 Hub 按授权码分配（例如当前客户是 `10.66.0.20`）。
- Mac 出口固定是 `10.66.0.100`，对外住宅 IP 当前是 `118.158.252.9`。
- Android 出口数据面是 Hub WireGuard 地址 `10.66.0.1:18081` 的 `dxreverse` proxy，公网 IP 随手机卡或 WiFi 网络变化。

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
客户 peer 和 Mac peer 的收发应该**对称镜像** ——
客户「发」≈ Mac「收」，客户「收」≈ Mac「发」。对得上就说明流量在贯通整条链路。

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

### 1.3 端到端验证：Hub 经 Mac 代理出公网

```bash
curl -x http://10.66.0.100:1080 -s https://api.ipify.org; echo
```

- 返回 `118.158.252.9`（日本住宅 IP）＝整条出口链路通、出口 IP 正确。
- 卡住/超时＝Hub 到 Mac 这段，或 Mac 上的 sing-box 有问题。

SOCKS5 方式同理：

```bash
curl --socks5-hostname 10.66.0.100:1080 -s https://api.ipify.org; echo
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

## 2. 日本 Mac 出口节点

> 系统 macOS（Apple Silicon），`maruichao` 登录：`ssh maruichao@100.80.36.89`
> 地址是 **Tailscale 地址**，本机要在同一 Tailnet 才连得上。

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
ls -la /usr/local/var/log/dxvpn/
tail -n 80 /usr/local/var/log/dxvpn/*.log
tail -f  /usr/local/var/log/dxvpn/*.log     # 实时跟踪，边连边看
```

### 2.4 看开机自启服务（LaunchDaemon）

```bash
sudo launchctl print system/com.daxiang.dxvpn.wireguard
sudo launchctl print system/com.daxiang.dxvpn.sing-box
```

输出里重点看 `state = running` 和 `last exit code`（非 0 表示上次异常退出）。

⚠️ 需要重启服务时：

```bash
# 重启（先 kickstart -k 强制重拉）
sudo launchctl kickstart -k system/com.daxiang.dxvpn.wireguard
sudo launchctl kickstart -k system/com.daxiang.dxvpn.sing-box

# 彻底卸载 / 重新装载（改了 plist 才需要）
sudo launchctl bootout   system /Library/LaunchDaemons/com.daxiang.dxvpn.sing-box.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.daxiang.dxvpn.sing-box.plist
```

### 2.5 看出口公网 IP

```bash
curl -s https://api.ipify.org; echo         # Mac 实际出公网的住宅 IP
```

### 2.6 配置文件与启动脚本

```bash
# WireGuard 配置
cat /Users/maruichao/.dxvpn/wireguard/mac-mini.conf       # 工作配置
cat /usr/local/etc/dxvpn/wireguard/mac-mini.conf          # 固化配置

# sing-box 配置
cat /Users/maruichao/.dxvpn/sing-box-mac-egress.json      # 工作配置
cat /usr/local/etc/dxvpn/sing-box/mac-egress.json         # 固化配置

# 启动脚本
cat /usr/local/sbin/dxvpn-wireguard-up.sh
cat /usr/local/sbin/dxvpn-sing-box-run.sh
```

---

## 3. Android 出口节点

> 当前 Android 出口数据面已迁到 `dxreverse`:Android 主动反连 Hub,Hub 侧暴露 `10.66.0.1:18081` HTTP CONNECT proxy。
> WireGuard App 仍作为内网控制面使用,不是主要公网出口数据面。

### 3.1 Hub 侧验证 Android 出口

```bash
scripts/check-android-reverse-egress.sh
curl -x http://10.66.0.1:18081 -s https://api.ipify.org; echo
curl -L --max-time 30 -x http://10.66.0.1:18081 -o /dev/null \
  -w "code=%{http_code} bytes=%{size_download} bps=%{speed_download} seconds=%{time_total}\n" \
  "https://speed.cloudflare.com/__down?bytes=50000000"
```

若 Hub 本机 curl 能通,但 Windows 客户端 `dxvpn.exe status` 获取出口 IP 失败,检查 UFW 是否允许 WireGuard 客户端访问 Hub proxy:

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

### 3.4 不用 ADB 远程控制 Android

目标方案见 [egress/android-control](../../../egress/android-control/README.md)：手机端由 Magisk `service.d` 拉起 watchdog，watchdog 保证 Go SSH 控制面 `dxandroid-control` 只监听 WireGuard 内网 `10.66.0.101:2022`，并且只允许密钥登录。

从已经通过 VPN/WireGuard 进入 `10.66.0.0/24` 的管理机直连手机：

```powershell
ssh -i $env:USERPROFILE\.ssh\dxandroid_control -p 2022 root@10.66.0.101
```

从 Hub 侧也可以连，但 Hub 只是 WireGuard 路由/中转节点，不是日常必需跳板：

```bash
ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101
```

登录后常用控制命令：

```sh
ps -A -o PID,PPID,ARGS | grep dxreverse
tail -80 /data/local/tmp/dxreverse-egress.log
sh /data/adb/service.d/99-dxreverse-egress.sh
tail -80 /data/local/tmp/dxandroid-control.log
```

安全边界：

- `dxandroid-control` 必须只绑定 `10.66.0.101:2022`，不要监听 `0.0.0.0`。
- 只用 SSH key 登录，禁止密码登录。
- 带内控制依赖 WireGuard 隧道在线；手机没电、关机、无网、隧道未起时仍需要物理接触或 ADB 兜底。

### 3.5 Android 本机检查

ADB 可用时：

```powershell
$adb="$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
& $adb shell su -c "ip route get 36.50.84.68"
& $adb shell su -c "ps -A -o PID,PPID,ARGS | grep dxreverse"
& $adb shell su -c "tail -80 /data/local/tmp/dxreverse-egress.log"
& $adb shell su -c "sed -n '1,40p' /data/adb/dxreverse/client.yaml"
& $adb shell su -c "ip addr show tun0 | grep 10.66.0.101"
```

重点看：

- `ip route get 36.50.84.68` 是走 `rmnet_data*` 还是 `wlan0`。
- 日志中是否有 `connected to reverse tcp server`。
- 当前 `connections` 是否为预期值（生产为 2）。
- Hub `dxreverse-hub.service` 启动日志是否显示 `max_proxy_connections=32 max_proxy_connections_per_client=12`。
- WireGuard App 是否创建了 `tun0 / 10.66.0.101`。
- 若 `tun0` 缺失,watchdog 会最多每 120s 发一次 WireGuard App `SET_TUNNEL_UP` intent;若 `tun0` 存在但 Hub 内网 ping 失败,watchdog 会 `SET_TUNNEL_DOWN` 后再 `SET_TUNNEL_UP` 强制重拨。可看 `/data/local/tmp/dxandroid-control.log` 中的 `wireguard unhealthy` 记录。

### 3.6 当前已知性能判断

- 手机 App 测到的高速下载不等于出口可用下载速度。
- 作为出口时，电脑下载需要手机把数据上传回 Hub，因此手机上行是关键瓶颈。
- 若仍看到 `dxandroid-egress` 进程,说明旧服务残留被误启动;当前默认应只有 `99-dxreverse-egress.sh` 和 `dxreverse client`。

---

## 4. 一分钟快速体检流程

按这个顺序走，能快速定位问题在哪一段：

```bash
# ① 在 Hub 上：客户和 Mac 都有近 2 分钟握手吗？流量在涨吗？
wg show

# ② 在 Hub 上：转发开着吗？端到端出口通吗？
sysctl net.ipv4.ip_forward
curl -x http://10.66.0.100:1080 -s https://api.ipify.org; echo
curl -x http://10.66.0.1:18081 -s https://api.ipify.org; echo

# ③ 若 ② 不通，再上 Mac 看 sing-box 和日志
sudo /opt/homebrew/bin/wg show
pgrep -fl sing-box
tail -n 50 /usr/local/var/log/dxvpn/*.log
```

| 现象 | 大概率原因 |
| --- | --- |
| Hub 上客户 peer 无握手 / 握手很久前 | 客户端没启动、网络不通、或客户端配置/密钥不对 |
| 客户有握手但 `transfer` 不涨 | 客户连上了但没真正走流量（浏览器没设代理？） |
| Hub `curl -x ...1080` 超时 | Hub→Mac 这段断，或 Mac 上 sing-box 挂了 |
| 出口 IP 不是 `118.158.252.9` | Mac 的 WAN/住宅网络变了，或走了别的出口 |
| Android 手机卡直连快但代理慢 | 多半是手机上行到 Hub 慢，不是手机下行慢 |
| Android 日志大量 `message too long` | Android WireGuard/sing-box 发包路径仍需优化 |
| `ip_forward = 0` | Hub 没开转发，流量到 Hub 就断 |

---

## 5. 正常基线（2026-06-03 实测）

留作对照，知道「正常」长什么样：

- Hub `wg show`：
  - 客户 `10.66.0.20`（端点为国内 IP）：握手 1 分钟内，收 124 MiB / 发 149 MiB。
  - Mac `10.66.0.100`（端点 `118.158.252.9`）：握手 1 分钟内，收 149 MiB / 发 124 MiB（与客户镜像对称）。
- Hub `net.ipv4.ip_forward = 1`。
- Hub `curl -x http://10.66.0.100:1080 https://api.ipify.org` → `118.158.252.9`。

> 提示：服务器访问文档里的 Peer 表可能滞后，排查时以 `wg show wg0` 的实时结果为准。
