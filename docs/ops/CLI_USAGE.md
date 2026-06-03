# CLI 使用设计

## 设计目标

中国客户端使用时要尽量简单：

- 不要求用户理解 WireGuard 路由细节。
- 不要求用户手工编辑配置文件。
- 尽量提供一个本地代理地址，例如 `127.0.0.1:7890`。
- 浏览器、命令行工具、应用程序只要设置代理，就可以通过日本出口访问日本网站。

## 推荐用户体验

### 1. 登录或导入配置

第一次使用：

```bash
dxvpn login --hub 36.50.84.68 --token <令牌>
```

或者导入管理员生成的配置：

```bash
dxvpn import ./cn-client-01.yaml
```

### 2. 查看可用出口

```bash
dxvpn egress list
```

示例输出：

```text
名称          地区    类型      当前 IP          状态
jp-mac-01    日本    住宅IP    123.xxx.xxx.xxx   在线
jp-phone-01  日本    手机IP    106.xxx.xxx.xxx   在线
```

### 3. 启动本地代理

最简单模式：

```bash
dxvpn proxy start --egress jp-mac-01
```

默认监听：

```text
HTTP 代理：  127.0.0.1:7890
SOCKS5代理：127.0.0.1:7891
```

然后用户只需要把浏览器或系统代理设置为：

```text
127.0.0.1:7890
```

### 4. 测试出口 IP

```bash
dxvpn ip
```

示例输出：

```text
当前出口：jp-mac-01
出口类型：日本住宅 IP
公网 IP：123.xxx.xxx.xxx
```

也可以直接测试日本网站：

```bash
dxvpn test https://www.yahoo.co.jp
```

### 5. 切换出口

```bash
dxvpn proxy switch jp-phone-01
```

### 6. 停止代理

```bash
dxvpn proxy stop
```

## 两种客户端模式

### 模式一：本地代理模式

这是第一版最推荐的模式。

```text
应用程序 / 浏览器
    |
    | HTTP / SOCKS5 代理
    v
127.0.0.1:7890 / 7891
    |
    | CLI 内部转发
    v
WireGuard 隧道
    |
    v
Hub
    |
    v
日本出口节点
```

优点：

- 用户使用简单。
- 不需要改系统默认路由。
- 不容易把整个电脑网络弄断。
- 可以按应用配置代理。
- 适合第一版快速落地。

缺点：

- 只有支持代理的应用才能直接使用。
- UDP 应用支持较弱，除非额外实现 TUN 模式。

### 模式二：全局 TUN 模式

后续再做。

```bash
dxvpn tun start --egress jp-mac-01
```

优点：

- 可以接管整台机器的流量。
- 应用不需要单独设置代理。
- 更像传统 VPN。

缺点：

- 需要管理员权限。
- Windows/macOS/Linux 都有不同的路由和权限处理。
- 更容易影响用户本机网络。

建议：

- 第一阶段先做本地代理模式。
- 第二阶段再做 TUN 全局模式。

## 推荐命令结构

```bash
dxvpn login --hub <hub地址> --token <令牌>
dxvpn import <配置文件>

dxvpn status
dxvpn ip
dxvpn test <URL>

dxvpn egress list
dxvpn egress select <出口名称>

dxvpn proxy start --egress <出口名称>
dxvpn proxy start --auto
dxvpn proxy switch <出口名称>
dxvpn proxy stop
dxvpn proxy status

dxvpn tun start --egress <出口名称>
dxvpn tun stop
dxvpn tun status
```

## 极简使用路径

对于普通用户，最好只需要三步：

```bash
dxvpn import cn-client-01.yaml
dxvpn proxy start --auto
dxvpn ip
```

然后把浏览器代理设置为：

```text
127.0.0.1:7890
```

## 本地代理协议

建议 CLI 同时提供：

- HTTP 代理：`127.0.0.1:7890`
- SOCKS5 代理：`127.0.0.1:7891`

浏览器和大部分工具优先使用 HTTP 代理即可。

命令行工具示例：

```bash
curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp
```

或者：

```bash
curl --socks5 127.0.0.1:7891 https://www.yahoo.co.jp
```

## CLI 内部实现建议

第一版可以这样实现：

- CLI 启动 WireGuard 隧道，连接 Hub。
- CLI 启动本地 HTTP/SOCKS5 代理。
- 本地代理把 TCP 请求转发进 WireGuard。
- Hub 根据客户端的出口选择，把流量转给日本出口节点。
- 日本出口节点 NAT 到本地住宅或手机网络。

本地代理组件可以选择：

- 自研轻量 HTTP CONNECT / SOCKS5 代理。
- 或集成成熟代理内核，例如 `gost`、`sing-box`、`xray` 的本地代理能力。

第一版建议优先集成成熟代理内核，减少协议实现风险。

## 配置文件示例

```yaml
client:
  name: cn-client-01
  wg_ip: 10.66.0.10

hub:
  endpoint: 36.50.84.68:51820
  public_key: "..."

proxy:
  http_listen: 127.0.0.1:7890
  socks5_listen: 127.0.0.1:7891

egress:
  default: jp-mac-01
```

## 第一版验收标准

- 中国客户端执行 `dxvpn proxy start --egress jp-mac-01` 后，本地出现 `127.0.0.1:7890`。
- 使用 `curl -x http://127.0.0.1:7890 https://www.yahoo.co.jp` 可以访问日本网站。
- `dxvpn ip` 显示的是日本出口节点的公网 IP，不是 Hub 的公网 IP。
- `dxvpn proxy switch jp-phone-01` 可以切换到另一个日本出口。
- `dxvpn proxy stop` 可以干净停止本地代理和相关连接。

