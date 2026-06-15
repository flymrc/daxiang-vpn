# MVP 01：单 Hub + 单 Mac 日本出口 + 单中国 CLI

> Deprecated:本页记录早期 Mac 出口 MVP 设计。2026-06-15 起,Mac `10.66.0.100:1080` 出口路线已弃用,不再作为新客户端、自动调度或专项验证出口。当前数据面默认使用 Android `zhreverse` / Hub 入口 `10.66.0.1:18081`。不要按本页继续推进 Mac 出口实现。

## 目标

第一个 MVP 只验证一件事：

> 中国客户端通过 `zhvpn` 启动本地代理后，可以使用日本 Mac 的住宅 IP 或手机 IP 访问日本网站。

最终访问链路：

```text
中国客户端浏览器 / curl
    |
    | HTTP/SOCKS5 本地代理
    v
zhvpn CLI 127.0.0.1:7890 / 127.0.0.1:7891
    |
    | WireGuard
    v
Hub 服务器 36.50.84.68
    |
    | WireGuard Peer 间转发到 10.66.0.100:1080
    v
日本 Mac 出口节点 sing-box
    |
    | 直接访问公网
    v
日本住宅 IP / 手机 IP
    |
    v
日本网站
```

注意：不是把 Mac 的流量主动转发到 CLI，而是让中国 CLI 的访问流量通过 Hub 转发到 Mac，再从 Mac 的日本网络出公网。

第一版优先使用“远端代理”方式，而不是三层 NAT 方式：

- Mac 上运行 `sing-box`，监听 `10.66.0.100:1080`。
- CLI 在中国客户端本地监听 `127.0.0.1:7890` 和 `127.0.0.1:7891`。
- CLI 把本地代理请求通过 WireGuard 转发给 Mac 的 `10.66.0.100:1080`。
- Mac 上的 `sing-box` 再从日本本地网络访问目标网站。

这样第一版不需要修改客户端系统路由，也不需要先处理 Mac 上复杂的 NAT 持久化问题。

## 当前 Hub 状态

Hub 服务器已经满足 MVP 的基础条件。

- 公网 IP：`36.50.84.68`
- 系统：Ubuntu 24.04.3 LTS
- WireGuard 接口：`wg0`
- Hub WireGuard IP：`10.66.0.1/24`
- 监听端口：`51820/udp`
- IPv4 转发：已开启
- WireGuard 服务：`wg-quick@wg0` 已运行并开机自启
- Hub 内部转发：已允许 `wg0 -> wg0`

当前 Peer：

| 名称 | WireGuard IP | 当前状态 |
| --- | --- | --- |
| `windows-client-1` | `10.66.0.10` | 之前有握手 |
| `mac-mini` | `10.66.0.100` | 已握手 |

MVP 的第一步已经完成：`mac-mini` 已经和 Hub 握手，Hub 可以 ping 通 `10.66.0.100`。

## MVP 范围

### 本次要做

- 让日本 Mac 连上 Hub。
- 确认 Hub 上能看到 Mac 的 WireGuard 握手。
- 在 Mac 上开启转发和 NAT。
- 让一个中国客户端 CLI 连接 Hub。
- CLI 提供本地 HTTP/SOCKS5 代理。
- 通过本地代理访问日本网站。
- 验证公网出口 IP 是 Mac 所在日本网络的 IP，而不是 Hub IP。

### 本次不做

- 多出口节点调度。
- 自动负载均衡。
- 复杂权限系统。
- Web 管理后台。
- 全局 TUN 模式。
- 域名级智能分流。
- 长期计费、流量统计、用户系统。

## 节点规划

| 节点 | 名称 | WireGuard IP | 角色 |
| --- | --- | --- | --- |
| Hub | `jp-hub-01` | `10.66.0.1` | 中转服务器 |
| 日本 Mac | `mac-mini` | `10.66.0.100` | 日本出口节点 |
| 中国 CLI | `cn-client-01` | `10.66.0.10` 或新 IP | 使用者客户端 |

如果现有 `windows-client-1` 就是当前中国测试客户端，可以继续使用 `10.66.0.10`。如果要新建 CLI 测试客户端，建议分配：

```text
10.66.0.20
```

## 第一步：让 Mac 和 Hub 握手

### Mac 端需要的 WireGuard 配置

Mac 的配置应该类似：

```ini
[Interface]
PrivateKey = <Mac 私钥>
Address = 10.66.0.100/24

[Peer]
PublicKey = <Hub 公钥>
Endpoint = 36.50.84.68:51820
AllowedIPs = 10.66.0.0/24
PersistentKeepalive = 25
```

说明：

- `Address` 使用 `10.66.0.100/24`。
- `Endpoint` 指向 Hub：`36.50.84.68:51820`。
- `AllowedIPs = 10.66.0.0/24` 先只保证 WireGuard 内网互通。
- `PersistentKeepalive = 25` 有利于 Mac 在 NAT 网络后保持连接。

### Hub 上验证

在 Hub 上执行：

```bash
wg show
```

成功时应该看到 `mac-mini` 对应 Peer 有：

```text
latest handshake: ...
endpoint: <Mac 当前公网地址>:<端口>
transfer: ...
```

也可以运行：

```bash
/opt/jp-gateway/scripts/status.sh
/opt/jp-gateway/scripts/diagnostics.sh
```

验收标准：

- Hub 上 `mac-mini` 不再显示“从未握手”。
- `latest handshake` 在几分钟以内。
- Hub 能看到 `mac-mini` 的收发流量。

## 第二步：Mac 开启转发和 NAT

这一阶段先不作为 MVP 必选项。

因为第一版采用远端代理方式，Mac 不需要马上开启系统级 IP 转发和 NAT。Mac 上的 `sing-box` 会直接从本机访问公网，天然使用 Mac 的日本住宅 IP 或手机 IP。

三层 NAT 仍然是后续 TUN / 全局 VPN 模式需要实现的能力。

### 后续 TUN 模式核心要求

- 开启 IPv4 转发。
- 找到 Mac 的 WireGuard 接口，通常是 `utunX`。
- 找到 Mac 的公网出口接口，通常是 `en0`，也可能是手机网络或 USB 网卡接口。
- 使用 `pf` 做 NAT。

### 概念配置

```text
WireGuard 网段：10.66.0.0/24
Mac WireGuard IP：10.66.0.100
Mac 出口接口：en0
NAT：10.66.0.0/24 -> en0
```

### 后续 TUN 模式验收标准

- Mac 本机可以访问外网。
- Mac 本机公网 IP 是目标日本住宅 IP 或手机 IP。
- 从 WireGuard 转来的流量可以通过 Mac 出口访问外网。

## 第三步：Mac 远端代理

Mac 上运行 `sing-box`：

```text
监听地址：10.66.0.100:1080
代理类型：mixed，支持 HTTP 和 SOCKS5
出口方式：direct，直接使用 Mac 本机公网网络
```

当前已验证：

```bash
curl -x http://10.66.0.100:1080 https://api.ipify.org
```

返回：

```text
118.158.252.9
```

Hub 直连公网 IP 是：

```text
36.50.84.68
```

因此可以确认，通过 Mac 代理访问公网时，出口已经变成 Mac 的日本公网 IP。

当前已固化：

| 项目 | 路径 |
| --- | --- |
| WireGuard 配置 | `/usr/local/etc/zhvpn/wireguard/mac-mini.conf` |
| sing-box 配置 | `/usr/local/etc/zhvpn/sing-box/mac-egress.json` |
| WireGuard 启动脚本 | `/usr/local/sbin/zhvpn-wireguard-up.sh` |
| sing-box 启动脚本 | `/usr/local/sbin/zhvpn-sing-box-run.sh` |
| WireGuard LaunchDaemon | `/Library/LaunchDaemons/com.zongheng.zhvpn.wireguard.plist` |
| sing-box LaunchDaemon | `/Library/LaunchDaemons/com.zongheng.zhvpn.sing-box.plist` |
| 日志目录 | `/usr/local/var/log/zhvpn` |

说明：

- `com.zongheng.zhvpn.wireguard` 用于开机后拉起 WireGuard。
- `com.zongheng.zhvpn.sing-box` 用于开机后拉起 Mac 远端代理。
- 当前 `sing-box` 服务已经由 LaunchDaemon 运行。
- `WireGuard` 当前已经在线，LaunchDaemon 脚本检测到 `10.66.0.100` 已存在后会正常退出。

## 第四步：中国 CLI 本地代理

第一版 CLI 不做复杂全局路由，只做本地代理。

启动命令：

```bash
zhvpn proxy start --egress mac-mini
```

默认监听：

```text
HTTP 代理：  127.0.0.1:7890
SOCKS5代理：127.0.0.1:7891
```

用户测试：

```bash
curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp
```

查看出口 IP：

```bash
zhvpn ip
```

或者：

```bash
curl -x http://127.0.0.1:7890 https://api.ipify.org
```

验收标准：

- 本地 `127.0.0.1:7890` 可用。
- 访问日本网站成功。
- 查到的公网 IP 是 Mac 所在日本网络的 IP。
- 查到的公网 IP 不是 Hub 的 `36.50.84.68`。

## MVP 技术实现建议

### Hub

Hub 当前可以继续使用现有脚本：

- `/opt/jp-gateway/scripts/add-peer.sh`
- `/opt/jp-gateway/scripts/status.sh`
- `/opt/jp-gateway/scripts/diagnostics.sh`

MVP 阶段先不重构 Hub，只补必要配置和验证。

### Mac 出口节点

当前已经完成：

- WireGuard 配置已安装。
- `mac-mini` 已和 Hub 握手。
- `sing-box` 已安装。
- `sing-box` 已监听 `10.66.0.100:1080`。
- 从 Hub 通过 `10.66.0.100:1080` 访问公网时，出口 IP 是 `118.158.252.9`。

需要确认：

- `sing-box` 是否需要做成开机自启。
- WireGuard 是否需要做成开机自启。
- 是否要给远端代理增加认证，或者只允许 WireGuard 内网访问。

### 中国 CLI

第一版 CLI 可以先做最小功能：

```bash
zhvpn proxy start --egress mac-mini
zhvpn proxy stop
zhvpn proxy status
zhvpn ip
```

内部可以先集成成熟代理内核，避免自己实现完整 HTTP/SOCKS5 协议。CLI 本地代理可以把请求转发到：

```text
10.66.0.100:1080
```

候选方案：

- `gost`
- `sing-box`
- `xray`

MVP 推荐：

- CLI 负责配置和启动代理内核。
- 代理内核负责本地 HTTP/SOCKS5 监听。
- WireGuard 负责 Hub 和节点之间的隧道。

## MVP 验收清单

### Hub

- [ ] `wg-quick@wg0` 正常运行。
- [ ] `51820/udp` 正常监听。
- [ ] `ip_forward = 1`。
- [ ] `wg0 -> wg0` 转发规则存在。
- [ ] Hub 上能看到 Mac 的最新握手。

### Mac

- [x] Mac 可以连接 Hub。
- [x] Mac 的 WireGuard IP 是 `10.66.0.100`。
- [x] Mac 本地公网 IP 是日本住宅 IP 或手机 IP：`118.158.252.9`。
- [x] Mac 已运行远端代理：`10.66.0.100:1080`。
- [x] Mac 远端代理开机自启。
- [x] Mac WireGuard 开机自启。

### 中国 CLI

- [ ] CLI 可以连接 Hub。
- [ ] CLI 可以启动 `127.0.0.1:7890`。
- [ ] `curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp` 成功。
- [ ] `curl -x http://127.0.0.1:7890 https://api.ipify.org` 返回 Mac 的日本公网 IP。
- [ ] `zhvpn proxy stop` 可以停止代理。

## MVP 完成后的下一步

MVP 成功后再进入第二阶段：

- 把 Mac NAT 配置固化成脚本。
- 增加 Linux 出口节点脚本。
- 增加多个出口节点。
- 增加 `zhvpn egress list` 和 `zhvpn proxy switch`。
- 把 Hub 现有 Shell 脚本迁移到仓库内维护。
- 建立配置清单 `inventory.yaml`。
