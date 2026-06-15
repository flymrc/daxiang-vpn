# Admin Innernet Client

`admin-innernet` 是管理专用 WireGuard peer,用途类似 Tailscale 的内网接入:

- 只访问 `10.66.0.0/24` WireGuard 内网。
- 不接管默认路由。
- 不把普通公网流量导入 Hub。
- 用于访问 Hub、deprecated Mac peer、Android 出口控制面等管理地址。

## 当前分配

| 项 | 值 |
| --- | --- |
| Peer name | `admin-innernet` |
| WireGuard IP | `10.66.0.40/32` |
| Client config | `local/wireguard/admin-innernet.conf` |
| Installed config | `~/.zhvpn/wireguard/admin-innernet.conf` |
| Template | `docs/20-operations/configs/client/admin-innernet.conf.example` |

真实配置含私钥,放在仓库本机私有目录 `local/wireguard/`,该目录已被 `.gitignore` 忽略。

## 常驻与状态栏

当前这台 Mac mini 仍有历史 `mac-mini` WireGuard 常驻隧道 `10.66.0.100/24`。Mac 出口数据面已弃用,但该隧道仍可作为管理内网连通性来源。因此它访问管理内网不需要再启动第二条 `admin-innernet` 隧道;状态栏会把这个现有内网连通状态显示为在线。

已安装的状态栏工具:

| 项 | 路径 |
| --- | --- |
| App binary | `local/apps/ZonghengInnernetStatus` |
| LaunchAgent | `~/Library/LaunchAgents/com.zongheng.zhvpn.innernet-status.plist` |
| Helper scripts | `~/.zhvpn/bin/zhvpn-admin-innernet-*.sh` |

启动/停止状态栏:

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/com.zongheng.zhvpn.innernet-status.plist
launchctl kickstart -k "gui/$(id -u)/com.zongheng.zhvpn.innernet-status"
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/com.zongheng.zhvpn.innernet-status.plist
```

状态栏菜单提供:

- 当前 innernet 状态。
- `admin-innernet` connect/disconnect 入口。
- 复制 Android SSH 命令。
- 打开本机 WireGuard 配置目录。

注意:在这台 Mac mini 上,不要同时启用历史 `mac-mini` 和 `admin-innernet` 两条指向同一 Hub/同一 `10.66.0.0/24` 的 WireGuard 隧道,否则路由会重叠。`admin-innernet` 更适合导入到其它管理机。

## 路由原则

客户端配置的关键点:

```ini
AllowedIPs = 10.66.0.0/24
```

不要写:

```ini
AllowedIPs = 0.0.0.0/0
```

这样本机只有访问 `10.66.0.x` 时走 VPN,访问普通互联网时仍走本机原网络。

## 验证

连接后应能访问:

```bash
ping 10.66.0.1
ping 10.66.0.100
ping 10.66.0.101
ssh -i ~/.ssh/zhandroid_control_local -p 2022 root@10.66.0.101 id
```

不应改变普通公网出口 IP。
