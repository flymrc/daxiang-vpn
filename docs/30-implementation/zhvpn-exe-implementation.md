# zhvpn.exe 实施文档

> Deprecated note:本文早期段落大量使用 Mac `10.66.0.100:1080` 作为示例出口。2026-06-15 起,Mac 出口路线已弃用;当前新配置应默认使用 Android `zhreverse` / Hub 入口 `10.66.0.1:18081`。保留 Mac 示例仅作历史实现参考。

## 目标

先做一个 Windows 单文件命令行程序：

```text
zhvpn.exe
```

用户不需要理解：

- WireGuard
- sing-box
- Hub
- Peer
- AllowedIPs
- 远端代理地址

用户只需要执行：

```powershell
zhvpn.exe login <授权码>
zhvpn.exe start
zhvpn.exe status
zhvpn.exe test
zhvpn.exe stop
```

用户看到的是：

```text
纵横 VPN 已连接
出口地区：日本
出口节点：mac-mini
出口 IP：118.158.252.9
本地代理：127.0.0.1:7890
```

## MVP 范围

### 本次做

- Windows `zhvpn.exe`。
- 导入客户端配置。
- 启动本地代理。
- 停止本地代理。
- 查看状态。
- 测试出口 IP。
- 尽量隐藏 WireGuard 和 sing-box 细节。

### 本次不做

- 图形界面。
- 托盘图标。
- Windows Service。
- 自动系统代理设置。
- 全局 TUN。
- 多出口切换。
- 用户登录系统。
- 自动生成 Peer。
- 服务端 API。

## 产品体验

### 导入配置

```powershell
zhvpn.exe import .\cn-client-01.yaml
```

输出：

```text
配置已导入
客户端：cn-client-01
默认出口：日本 mac-mini
```

### 启动

```powershell
zhvpn.exe start
```

输出：

```text
纵横 VPN 已启动
本地代理：http://127.0.0.1:7890
出口节点：日本 mac-mini
```

### 状态

```powershell
zhvpn.exe status
```

输出：

```text
状态：运行中
本地代理：127.0.0.1:7890
远端出口：日本 mac-mini
远端连通：正常
```

### 测试

```powershell
zhvpn.exe test
```

输出：

```text
出口 IP：118.158.252.9
结果：通过
```

### 停止

```powershell
zhvpn.exe stop
```

输出：

```text
纵横 VPN 已停止
```

## 技术选型

### 推荐语言：Go

推荐用 Go 实现 `zhvpn.exe`。

原因：

- 可以编译成单文件 `.exe`。
- 跨平台能力好，后续可做 macOS/Linux。
- 进程管理、文件管理、HTTP 请求、端口检测都方便。
- 无需安装 Python/Node 运行时。
- 后续可以直接做 Windows Service 或托盘 GUI 的控制程序。

### 不推荐首选 Python

Python 开发快，但打包成 `.exe` 会带来：

- 文件体积大。
- 杀毒软件误报概率更高。
- 运行时依赖复杂。
- 长期维护不如 Go 清爽。

### 不推荐首选 Node.js

Node.js 也能打包，但：

- 依赖体积大。
- 子进程和路径管理容易杂。
- 用户机器上的行为不够干净。

### Rust 可作为后续选择

Rust 很适合长期产品，但 MVP 开发速度不如 Go。

结论：

```text
MVP 使用 Go。
```

## 底层组件选择

### 本地代理内核：sing-box

`zhvpn.exe` 第一版不自己实现代理协议，而是启动一个本地 `sing-box.exe`。

原因：

- 成熟稳定。
- 支持 mixed inbound，也就是 HTTP + SOCKS5。
- 支持 HTTP/SOCKS5 上游。
- 后续可扩展规则路由、TUN、DNS。

### WireGuard

MVP 第一版不在 `zhvpn.exe` 中强行管理 WireGuard。

原因：

- Windows 上 WireGuard 驱动和隧道服务需要更复杂的权限处理。
- 当前第一版只要能访问 `10.66.0.100:1080`，就能使用 Mac 出口代理。
- 可以先让管理员准备好 WireGuard 隧道，`zhvpn.exe` 负责检查连通性和启动本地代理。

后续增强：

- 自动安装 WireGuard。
- 自动导入 WireGuard Tunnel。
- 自动启动/停止 WireGuard Tunnel。
- 用 `wireguard.exe /installtunnelservice` 或官方能力管理隧道。

## MVP 架构

```text
zhvpn.exe
    |
    | 读取配置
    | 生成 sing-box 本地配置
    | 启动 sing-box.exe
    v
本地代理 127.0.0.1:7890
    |
    | 上游 HTTP/SOCKS5
    v
Mac 远端代理 10.66.0.100:1080
    |
    v
日本公网出口 118.158.252.9
```

## 打包方式

### 当前打包方式：单文件 zhvpn.exe

当前版本已经把代理内核内嵌到 `zhvpn.exe`。

发布目录：

```text
dist/windows-amd64/
  zhvpn.exe
  README.txt
```

客户不会看到：

- `sing-box.exe`
- Hub 地址
- 出口节点地址
- 远端代理地址
- WireGuard 细节

`zhvpn.exe` 启动时会在本地私有目录释放底层代理内核：

```text
%LOCALAPPDATA%\ZonghengVPN\bin\sing-box.exe
```

这个文件是程序运行时内部文件，不出现在发布包里。

## Windows 本地目录

推荐使用：

```text
%LOCALAPPDATA%\ZonghengVPN\
```

目录结构：

```text
%LOCALAPPDATA%\ZonghengVPN\
  config.yaml
  sing-box\
    local-proxy.json
  run\
    sing-box.pid
  logs\
    sing-box.log
```

## 配置文件

新的产品方向是服务端管理。客户配置不再包含 Hub、出口节点、远端代理等底层信息。

客户可见配置只保留授权：

```yaml
license:
  token: ZH-DEV-TOKEN
```

说明：

- Hub 地址、出口节点、远端代理地址都由服务端下发。
- 客户看不到，也不需要填写。
- `local_proxy` 默认使用 `127.0.0.1:7890`。
- `sing-box.exe` 默认和 `zhvpn.exe` 放在同一个目录，不需要写进用户配置。

## sing-box 本地配置

`zhvpn.exe start` 生成：

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

## 命令设计

### import

```powershell
zhvpn.exe import .\cn-client-01.yaml
```

行为：

- 读取 YAML。
- 校验字段。
- 写入 `%LOCALAPPDATA%\ZonghengVPN\config.yaml`。

### start

```powershell
zhvpn.exe start
```

行为：

1. 读取配置。
2. 检查 `10.66.0.100:1080` 是否可连接。
3. 生成 `local-proxy.json`。
4. 启动 `sing-box.exe run -c local-proxy.json`。
5. 记录 PID。
6. 检查 `127.0.0.1:7890` 是否监听。

### stop

```powershell
zhvpn.exe stop
```

行为：

- 读取 PID。
- 停止 `sing-box.exe`。
- 删除 PID 文件。

### status

```powershell
zhvpn.exe status
```

检查：

- 配置是否存在。
- PID 是否存在。
- 进程是否运行。
- `127.0.0.1:7890` 是否监听。
- `10.66.0.100:1080` 是否可达。

### test

```powershell
zhvpn.exe test
```

行为：

- 使用 `http://127.0.0.1:7890` 请求：

```text
https://api.ipify.org
```

- 返回公网 IP。
- 如果配置了 `expected_public_ip`，则对比是否一致。

## 权限设计

MVP 不需要管理员权限。

原因：

- 不改系统代理。
- 不安装驱动。
- 不创建 Windows Service。
- 不改路由表。
- 只监听 `127.0.0.1:7890`。

需要管理员权限的功能放到后续：

- 自动安装 WireGuard。
- 自动管理 WireGuard Tunnel。
- 全局 TUN 模式。
- 设置系统代理。
- 安装 Windows Service。

## 安全设计

### MVP 风险

本地代理只监听：

```text
127.0.0.1:7890
```

所以其他机器无法直接访问。

远端 Mac 代理只监听：

```text
10.66.0.100:1080
```

只有 WireGuard 内网能访问。

### 后续增强

- 远端代理增加用户名密码。
- 客户端配置增加签名。
- Hub 增加 Peer 管理 API。
- 每个客户端绑定唯一身份。
- 出口代理增加访问日志和限速。

## 开发结构

```text
cli/
  zhvpn/
    go.mod
    cmd/
      zhvpn/
        main.go
    internal/
      app/
        app.go
      config/
        config.go
      proxy/
        singbox.go
      process/
        process_windows.go
      netcheck/
        tcp.go
        ip.go
      paths/
        windows.go
```

## 第一步实现清单

1. 创建 Go 项目。
2. 实现 Windows 路径解析：`%LOCALAPPDATA%\ZonghengVPN`。
3. 实现 YAML 配置读取和导入。
4. 实现 sing-box 配置生成。
5. 实现启动 `sing-box.exe`。
6. 实现 PID 文件。
7. 实现停止进程。
8. 实现状态检查。
9. 实现出口 IP 测试。
10. 编译 `zhvpn.exe`。

## 编译命令

sing-box 以代码库形式编译进 `zhvpn.exe`（进程内运行），不再内嵌外部 exe。
必须带 `with_gvisor` 标签（WireGuard 的 gVisor 用户态网络栈），并用
`-trimpath -ldflags "-s -w"` 剥离调试信息瘦身：

```powershell
go build -tags with_gvisor -trimpath -ldflags "-s -w" -o dist\windows-amd64\zhvpn.exe .\cmd\zhvpn
```

发布构建（同时产出 amd64 / arm64）直接运行：

```powershell
.\build.ps1
```

如果在非 Windows 系统交叉编译：

```bash
GOOS=windows GOARCH=amd64 go build -tags with_gvisor -trimpath -ldflags "-s -w" -o dist/windows-amd64/zhvpn.exe ./clients/cli
```

> 体积：内嵌外部 sing-box.exe 时约 50MB；改为库化 + 只注册用到的协议
> （mixed / http / wireguard）+ 剥离符号后，约 17MB（amd64）/ 15MB（arm64）。

## 发布目录

MVP 发布包：

```text
dist/
  zhvpn.exe
  sing-box.exe
  cn-client-01.yaml
  README.txt
```

用户使用：

```powershell
.\zhvpn.exe import .\cn-client-01.yaml
.\zhvpn.exe start
.\zhvpn.exe test
```

## 验收标准

- [ ] `zhvpn.exe import cn-client-01.yaml` 成功。
- [ ] `%LOCALAPPDATA%\ZonghengVPN\config.yaml` 存在。
- [ ] `zhvpn.exe start` 成功启动本地代理。
- [ ] `127.0.0.1:7890` 正常监听。
- [ ] `zhvpn.exe status` 显示运行中。
- [ ] `zhvpn.exe test` 返回 `118.158.252.9`。
- [ ] 浏览器设置 HTTP 代理 `127.0.0.1:7890` 后可以访问日本网站。
- [ ] `zhvpn.exe stop` 能停止代理。

## 当前实现状态

已完成第一版 `zhvpn.exe` MVP 代码和发布目录。

源码目录：

```text
cli/zhvpn/
```

发布目录：

```text
dist/
  README.txt
  windows-amd64/
    zhvpn.exe
    README.txt
  windows-arm64/
    zhvpn.exe
    README.txt
```

已实现命令：

```powershell
zhvpn.exe import <配置文件>
zhvpn.exe login <授权码>
zhvpn.exe start
zhvpn.exe stop
zhvpn.exe status
zhvpn.exe test
zhvpn.exe help
```

当前验证结果：

- `go test ./...` 通过。
- `zhvpn.exe help` 正常输出。
- `zhvpn.exe import cn-client-01.yaml` 可导入配置。
- `zhvpn.exe login <授权码>` 可登录并只保存 token。
- `zhvpn.exe status` 可显示本地代理和远端出口状态。
- 已生成 Windows x64 发布包：`dist/windows-amd64`。
- 已生成 Windows ARM64 发布包：`dist/windows-arm64`。
- sing-box 已作为代码库编译进 `zhvpn.exe`，进程内运行；发布包里不再出现 `sing-box.exe`。`zhvpn.exe start` 会以隐藏的 `__engine` 子命令重新拉起自身作为后台进程承载 sing-box。
- 发布包里不再包含客户 YAML；客户通过授权码登录。
- 在当前开发机器上，`zhvpn.exe start` 会提示远端出口 `10.66.0.100:1080` 不可达，这是预期结果，因为这台机器尚未作为新的 WireGuard 客户端接入 Hub。

下一步需要先给当前测试客户端创建新的 WireGuard Peer，例如：

```text
名称：cn-client-01
WireGuard IP：10.66.0.20
```

等客户端能访问 `10.66.0.100:1080` 后，再执行：

```powershell
.\zhvpn.exe start
.\zhvpn.exe test
```

## 后续路线

### V0.2

- ~~内嵌 `sing-box.exe`~~ 已完成，并进一步改为库化进程内运行。
- 自动下载或更新内核。
- 支持多个出口。
- 支持 `zhvpn.exe switch mac-mini`。

### V0.3

- 自动设置和恢复系统代理。
- 增加托盘 GUI。
- 增加开机自启。

### V0.4

- 自动安装和管理 WireGuard。
- 支持全局 TUN。
- 支持服务端 API。
