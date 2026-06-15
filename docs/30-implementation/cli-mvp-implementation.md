# CLI MVP 实施文档

> Deprecated:本页记录早期 Mac `10.66.0.100:1080` 出口代理实现方案。2026-06-15 起,Mac 出口路线已弃用;新客户端和专项验证应默认走 Android `zhreverse` / Hub 入口 `10.66.0.1:18081`。本页只保留历史参考。

## 目标

实现第一个中国客户端 CLI MVP：

> 用户启动 `zhvpn proxy start` 后，本机出现 `127.0.0.1:7890` 代理地址。通过这个本地代理访问日本网站时，公网出口是日本 Mac 的公网 IP。

当前已具备的远端链路：

```text
Hub 36.50.84.68
    |
    | WireGuard
    v
Mac 出口节点 10.66.0.100
    |
    | sing-box mixed 代理
    v
10.66.0.100:1080
    |
    v
日本公网出口 118.158.252.9
```

CLI MVP 要补齐的是中国客户端本地这一段：

```text
浏览器 / curl / 应用
    |
    | HTTP 代理
    v
127.0.0.1:7890
    |
    | zhvpn CLI 本地代理
    v
WireGuard 隧道
    |
    v
Mac 远端代理 10.66.0.100:1080
```

## 非目标

本阶段不做：

- 不处理昨天已经关机的 Windows 客户端。
- 不做多客户端管理。
- 不做多出口调度。
- 不做全局 TUN 模式。
- 不做系统代理自动设置。
- 不做图形界面。
- 不重构 Hub 上现有脚本。
- 不做用户系统、计费、权限体系。

## MVP 用户体验

### 1. 导入配置

```bash
zhvpn import cn-client-01.yaml
```

### 2. 启动本地代理

```bash
zhvpn proxy start
```

启动后输出：

```text
zhvpn 本地代理已启动
HTTP 代理：127.0.0.1:7890
远端出口：mac-mini
远端代理：10.66.0.100:1080
```

### 3. 测试出口 IP

```bash
zhvpn proxy test
```

期望输出：

```text
当前出口 IP：118.158.252.9
出口节点：mac-mini
结果：通过
```

### 4. 停止代理

```bash
zhvpn proxy stop
```

## 推荐命令

第一版只实现这些命令：

```bash
zhvpn import <配置文件>
zhvpn status
zhvpn proxy start
zhvpn proxy stop
zhvpn proxy status
zhvpn proxy test
```

可以先不实现：

```bash
zhvpn egress list
zhvpn proxy switch
zhvpn tun start
zhvpn tun stop
```

## 配置文件格式

客户端配置文件建议使用 YAML。

示例：`cn-client-01.yaml`

```yaml
client:
  name: cn-client-01
  wg_ip: 10.66.0.20

wireguard:
  config_path: ./wireguard/cn-client-01.conf

hub:
  endpoint: 36.50.84.68:51820
  wg_ip: 10.66.0.1

egress:
  name: mac-mini
  wg_ip: 10.66.0.100
  proxy_addr: 10.66.0.100:1080
  expected_public_ip: 118.158.252.9

local_proxy:
  http_addr: 127.0.0.1:7890
```

说明：

- `wireguard.config_path` 指向客户端自己的 WireGuard 配置。
- `egress.proxy_addr` 是 Mac 上已经运行的远端代理。
- `local_proxy.http_addr` 是用户本机应用要使用的代理地址。

## 客户端 WireGuard 配置

需要新建一个 Peer，不使用昨天的 Windows 客户端。

建议：

```text
名称：cn-client-01
WireGuard IP：10.66.0.20
```

客户端 WireGuard 配置示例：

```ini
[Interface]
PrivateKey = <客户端私钥>
Address = 10.66.0.20/24

[Peer]
PublicKey = <Hub 公钥>
Endpoint = 36.50.84.68:51820
AllowedIPs = 10.66.0.0/24
PersistentKeepalive = 25
```

说明：

- `AllowedIPs = 10.66.0.0/24` 即可。
- 第一版只需要访问 Mac 的 `10.66.0.100:1080`。
- 不需要 `0.0.0.0/0`。
- 不需要改系统默认路由。

## 本地代理设计

第一版 `zhvpn proxy start` 启动一个本地 HTTP 代理：

```text
监听：127.0.0.1:7890
上游：10.66.0.100:1080
```

本地代理只需要把请求转发给 Mac 的远端代理。

```text
127.0.0.1:7890  ->  10.66.0.100:1080
```

因为 Mac 的 `10.66.0.100:1080` 是 mixed 代理，所以它同时支持：

- HTTP CONNECT
- SOCKS5

本地代理 MVP 可以只支持 HTTP 代理。浏览器和 `curl -x http://127.0.0.1:7890` 都能用。

## 技术实现方案

### 方案 A：CLI 直接实现 HTTP CONNECT 转发

CLI 自己监听 `127.0.0.1:7890`。

对每个连接：

- 读取 HTTP 请求。
- 如果是 `CONNECT host:port`，把原始请求转发到 `10.66.0.100:1080`。
- 如果是普通 HTTP 请求，也转发到 `10.66.0.100:1080`。
- 后续数据做双向 TCP 复制。

优点：

- 依赖少。
- MVP 很轻。

缺点：

- 需要自己处理 HTTP 代理边界。
- 后续 SOCKS5、认证、连接池要继续补。

### 方案 B：CLI 启动本地 sing-box

CLI 生成一个本地 `sing-box` 配置：

```text
inbound:  127.0.0.1:7890 mixed
outbound: 10.66.0.100:1080 http 或 socks
```

CLI 只负责：

- 生成配置。
- 启动进程。
- 停止进程。
- 做状态检查。

优点：

- 稳定。
- 同时支持 HTTP 和 SOCKS5。
- 后续扩展更容易。

缺点：

- 需要随 CLI 分发 `sing-box`，或要求用户安装。

### MVP 推荐

建议第一版采用方案 B。

理由：

- Mac 端已经使用 `sing-box`。
- 客户端也使用 `sing-box`，两端行为一致。
- CLI 可以聚焦配置、进程管理、测试，不必手写代理协议。

## 本地 sing-box 配置示例

```json
{
  "log": {
    "level": "info"
  },
  "inbounds": [
    {
      "type": "mixed",
      "tag": "local-mixed-in",
      "listen": "127.0.0.1",
      "listen_port": 7890
    }
  ],
  "outbounds": [
    {
      "type": "http",
      "tag": "mac-egress",
      "server": "10.66.0.100",
      "server_port": 1080
    }
  ],
  "route": {
    "final": "mac-egress"
  }
}
```

如果使用 SOCKS5 上游，也可以改成：

```json
{
  "type": "socks",
  "tag": "mac-egress",
  "server": "10.66.0.100",
  "server_port": 1080
}
```

## CLI 本地目录

推荐目录：

### macOS / Linux

```text
~/.zhvpn/
  config.yaml
  wireguard/
    cn-client-01.conf
  sing-box/
    local-proxy.json
  run/
    sing-box.pid
  logs/
    sing-box.log
```

### Windows

后续单独处理。

```text
%USERPROFILE%\.zhvpn\
```

## 进程管理

### proxy start

流程：

1. 读取 `~/.zhvpn/config.yaml`。
2. 检查 WireGuard 是否在线。
3. 如果 WireGuard 未在线，尝试启动 WireGuard。
4. 生成本地 `sing-box` 配置。
5. 启动 `sing-box`。
6. 写入 PID 文件。
7. 测试 `127.0.0.1:7890` 是否可连接。

### proxy stop

流程：

1. 读取 PID 文件。
2. 停止 `sing-box`。
3. 删除 PID 文件。

### proxy status

检查：

- PID 是否存在。
- 进程是否运行。
- `127.0.0.1:7890` 是否监听。
- `10.66.0.100:1080` 是否可达。

### proxy test

执行：

```bash
curl -x http://127.0.0.1:7890 https://api.ipify.org
```

期望：

```text
118.158.252.9
```

## WireGuard 启动策略

MVP 可以先不把 WireGuard 进程管理做得太复杂。

第一版可接受：

- 用户手工安装 WireGuard。
- CLI 检查 `10.66.0.20` 是否存在。
- CLI 检查是否可以访问 `10.66.0.100:1080`。
- 如果未连接，提示用户启动 WireGuard。

后续再增强：

- macOS 使用 `wg-quick up`。
- Linux 使用 `wg-quick up`。
- Windows 使用 WireGuard CLI 或 WireGuard GUI tunnel service。

## 开发语言建议

推荐优先使用 Go。

原因：

- 单文件发布方便。
- 跨平台好。
- 进程管理、TCP 检查、配置文件处理都简单。
- 后续可直接嵌入轻量代理逻辑。

备选：

- Python：开发快，但发布客户端麻烦。
- Node.js：依赖多，命令行工具可行但不如 Go 稳。
- Rust：很好，但 MVP 开发速度略慢。

## 推荐仓库结构

```text
zongheng-vpn/
  cli/
    zhvpn/
      go.mod
      cmd/
        zhvpn/
          main.go
      internal/
        config/
        proxy/
        wireguard/
        process/
        testutil/
      README.md
```

## MVP 验收标准

### 配置

- [ ] 可以导入 `cn-client-01.yaml`。
- [ ] 配置保存到 `~/.zhvpn/config.yaml`。
- [ ] 本地 `sing-box` 配置可以生成。

### 启动

- [ ] `zhvpn proxy start` 可以启动本地代理。
- [ ] `127.0.0.1:7890` 开始监听。
- [ ] `zhvpn proxy status` 显示运行中。

### 访问

- [ ] `curl -x http://127.0.0.1:7890 https://api.ipify.org` 返回 `118.158.252.9`。
- [ ] `curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp` 可以访问。

### 停止

- [ ] `zhvpn proxy stop` 可以停止本地代理。
- [ ] 停止后 `127.0.0.1:7890` 不再监听。

## 实施顺序

1. 在 Hub 上新增 `cn-client-01` Peer，分配 `10.66.0.20`。
2. 生成客户端 WireGuard 配置。
3. 在仓库创建 Go CLI 项目。
4. 实现 `zhvpn import`。
5. 实现 `zhvpn proxy start`，先启动本地 `sing-box`。
6. 实现 `zhvpn proxy status`。
7. 实现 `zhvpn proxy test`。
8. 实现 `zhvpn proxy stop`。
9. 用本机或指定中国客户端完整测试。

## 当前远端依赖状态

| 项目 | 状态 |
| --- | --- |
| Hub WireGuard | 已运行 |
| Hub 到 Mac WireGuard | 已握手 |
| Mac WireGuard IP | `10.66.0.100` |
| Mac 远端代理 | `10.66.0.100:1080` |
| Mac 远端代理出口 IP | `118.158.252.9` |
| Windows 旧客户端 | 已关机，本阶段不考虑 |
