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

## 日本 Mac 出口节点

- 角色：日本本地出口节点
- 连接地址：`100.80.36.89`
- 说明：该地址是 Tailscale 地址，不是普通局域网地址。
- SSH 用户：`maruichao`
- SSH 密码：已省略。优先使用既有可信通道或密码管理器。
- SSH 登录命令：

```bash
ssh maruichao@100.80.36.89
```

当前状态：

- 主机名：`maruichaodeMac-mini.local`
- 系统：macOS 26.5
- 本地网络接口：`en1`
- 本地 IP：`192.168.68.97`
- 当前公网出口 IP：`118.158.252.9`
- WireGuard 工具：已通过 Homebrew 安装
- WireGuard 配置：`/Users/maruichao/.dxvpn/wireguard/mac-mini.conf`
- WireGuard 固化配置：`/usr/local/etc/dxvpn/wireguard/mac-mini.conf`
- WireGuard 接口：`utun7`
- WireGuard IP：`10.66.0.100/24`
- 远端代理内核：`sing-box`
- 远端代理配置：`/Users/maruichao/.dxvpn/sing-box-mac-egress.json`
- 远端代理固化配置：`/usr/local/etc/dxvpn/sing-box/mac-egress.json`
- 远端代理监听：`10.66.0.100:1080`
- 远端代理类型：mixed，也就是同时支持 HTTP 和 SOCKS5
- WireGuard 开机启动：`/Library/LaunchDaemons/com.daxiang.dxvpn.wireguard.plist`
- 远端代理开机启动：`/Library/LaunchDaemons/com.daxiang.dxvpn.sing-box.plist`
- WireGuard 启动脚本：`/usr/local/sbin/dxvpn-wireguard-up.sh`
- 远端代理启动脚本：`/usr/local/sbin/dxvpn-sing-box-run.sh`
- 日志目录：`/usr/local/var/log/dxvpn`

当前验证结果：

- Hub 可以 ping 通 Mac 的 WireGuard IP：`10.66.0.100`
- Hub 可以访问 Mac 远端代理：`10.66.0.100:1080`
- Hub 直连公网 IP：`36.50.84.68`
- Hub 通过 Mac 代理访问公网 IP：`118.158.252.9`
- Hub 通过 Mac 代理访问 `https://www.yahoo.co.jp` 成功

常用检查命令：

```bash
# Mac 上检查 WireGuard
sudo /opt/homebrew/bin/wg show

# Mac 上检查 LaunchDaemon
sudo launchctl print system/com.daxiang.dxvpn.wireguard
sudo launchctl print system/com.daxiang.dxvpn.sing-box

# Hub 上检查 Mac 远端代理出口
curl -x http://10.66.0.100:1080 https://api.ipify.org
curl --socks5-hostname 10.66.0.100:1080 https://api.ipify.org
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
- 防火墙：`ufw` 未启用
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
| `mac-mini` | `10.66.0.100/32` | 已握手，Hub 可 ping 通 |
| `admin-innernet` | `10.66.0.40/32` | 管理专用内网 peer,只路由 `10.66.0.0/24`,不承载公网出口流量 |

## 当前模式

服务器当前是 P0 阶段的 WireGuard Hub：

- 允许 `wg0` 内部 Peer 之间互相转发。
- 客户端当前只把 `10.66.0.0/24` 路由进 WireGuard。
- 还没有实现“国内客户端通过日本本地节点出公网”的完整出口方案。

目标产品的下一步是 P1：

- 日本节点连接到 Hub。
- 中国客户端连接到 Hub。
- 中国客户端把指定流量或全量流量转发给选定的日本出口节点。
- 日本出口节点把来自 WireGuard 的流量 NAT 到本地住宅网络或手机网络。
