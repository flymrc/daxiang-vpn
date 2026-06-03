# 运维诊断命令手册

面向日常排查：**客户连不上 / 网速慢 / 出口 IP 不对 / 想确认流量是否在走**。
命令分 **Hub** 和 **Mac 出口** 两块，每条都标注「看什么、怎么判断」。

> 登录凭据（IP / 用户 / 密码）见 [SERVER_ACCESS.md](SERVER_ACCESS.md)，本文不重复抄密码。
>
> 标 ⚠️ 的是会改状态的命令（重载 / 重启），平时排查用不到，确认要改再用。

---

## 0. 流量路径回顾

```text
客户端(中国)  --WireGuard-->  Hub(36.50.84.68, wg0/10.66.0.1)
   --WireGuard Peer 间转发-->  日本 Mac(10.66.0.100)
   --sing-box 代理(10.66.0.100:1080)--> NAT --> 日本住宅公网
```

- 客户端的 WG IP 由 Hub 按授权码分配（例如当前客户是 `10.66.0.20`）。
- Mac 出口固定是 `10.66.0.100`，对外住宅 IP 当前是 `118.158.252.9`。

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

## 3. 一分钟快速体检流程

按这个顺序走，能快速定位问题在哪一段：

```bash
# ① 在 Hub 上：客户和 Mac 都有近 2 分钟握手吗？流量在涨吗？
wg show

# ② 在 Hub 上：转发开着吗？端到端出口通吗？
sysctl net.ipv4.ip_forward
curl -x http://10.66.0.100:1080 -s https://api.ipify.org; echo

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
| `ip_forward = 0` | Hub 没开转发，流量到 Hub 就断 |

---

## 4. 正常基线（2026-06-03 实测）

留作对照，知道「正常」长什么样：

- Hub `wg show`：
  - 客户 `10.66.0.20`（端点为国内 IP）：握手 1 分钟内，收 124 MiB / 发 149 MiB。
  - Mac `10.66.0.100`（端点 `118.158.252.9`）：握手 1 分钟内，收 149 MiB / 发 124 MiB（与客户镜像对称）。
- Hub `net.ipv4.ip_forward = 1`。
- Hub `curl -x http://10.66.0.100:1080 https://api.ipify.org` → `118.158.252.9`。

> 提示：`SERVER_ACCESS.md` 里的 Peer 表（只列了 `10.66.0.10` 和 `mac-mini`）已过时，当前活跃客户是 `10.66.0.20`。
