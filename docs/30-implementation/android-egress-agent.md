# Android 出口节点客户端

> 状态:本文前半部分记录旧 `dxandroid-egress` / sing-box 数据面。当前 Android 生产替代路径已迁到 [egress/reverse](../../egress/reverse/README.md):Android 主动反连 Hub,Hub 侧暴露 `10.66.0.1:18081` HTTP CONNECT proxy。旧 `dxandroid-egress` 只作为回滚路径保留。

## 当前生产目标

新版 `dxreverse` 优先满足:

- Android 无需入站端口,主动连接 Hub。
- Hub 侧提供 Android 出口代理入口。
- WireGuard App 继续作为内网控制面,不再承载主要公网出口数据面。
- Magisk `99-dxreverse-egress.sh` 和 `watchdog.sh` 负责常驻保活。

配置示例:

- [android-reverse-client.yaml.example](../20-operations/configs/egress/android-reverse-client.yaml.example)
- [hub-reverse-server.yaml.example](../20-operations/configs/egress/hub-reverse-server.yaml.example)

## 旧版目标记录

为 root 安卓手机准备第一版出口节点客户端。

路线对比文档：

- [Android 客户端三条路线](./android-client-routes.md)

第一版不追求完整 Android App 体验，而是优先满足：

- 设备能主动连接 Hub。
- 设备能在 WireGuard 内网地址上暴露远端代理端口。
- 设备能作为 `egress` 节点被中国客户端使用。
- 今晚拿到 root 安卓机后，可以直接通过 `adb push` 和 shell 运行。

## 语言选择

第一版推荐语言：`Go`

原因：

- 现有 `dxvpn` 客户端和 Hub 相关模型已经是 Go。
- 当前仓库已经内嵌 `sing-box` 运行时，Go 复用成本最低。
- 可以直接交叉编译到 `android/arm64`。
- 先做命令行守护进程，再决定是否包一层 Kotlin 前台服务。

不建议第一版直接用 Kotlin/Java 重写网络核心，因为会丢掉现有 WireGuard、sing-box、配置模型的复用优势。

## 角色定位

这个程序不是中国侧桌面客户端，而是：

```text
Android rooted phone = egress agent
```

它的职责是：

- 通过 WireGuard 连接 Hub。
- 在 WireGuard 内网地址上监听 `1080` 代理端口。
- 将收到的流量直接从手机卡网络出口发出。

当前推荐运行方式是 `wireguard.mode: external`：

- WireGuard 官方 App 负责 Android 到 Hub 的隧道。
- `dxandroid-egress` 只负责在 `10.66.0.101:1080` 上提供 mixed 代理。
- 这样可以避开 sing-box 内置 WireGuard endpoint 在 Android 移动网络上的 `sendmsg: message too long` 问题。

## 当前实现范围

已新增一个新的命令入口：

```text
egress/proxy
```

第一版支持：

- `validate`
- `render`
- `run`

说明：

- `validate` 校验节点配置。
- `render` 生成 sing-box 运行配置。
- `run` 以前台方式启动出口守护进程。

目前尚未实现：

- Android 前台服务。
- 开机自启。
- iptables 策略路由增强。
- Hub 侧 egress token/bootstrap。

出口端到 Hub 的保活由 WireGuard keepalive 承担：`egress/proxy/internal/egressproxy/singbox.go` 渲染 WireGuard peer 时设置 `persistent_keepalive_interval: 25`，也就是 embedded/sing-box WireGuard 模式下 Android 出口每 25 秒向 Hub peer 发一次 WireGuard keepalive，供 Hub 侧维持 NAT 映射和判断出口是否仍在线。
当前推荐的 `wireguard.mode: external` 由 WireGuard App/系统隧道负责这层保活；`dxandroid-egress` 在 external 模式只运行代理，不渲染 WireGuard endpoint。
安卓状态 App 仍只提供前台通知和本机状态观察，不直接托管 `dxandroid-egress` 进程生命周期。

## 配置模型

示例配置见：

- [android-egress-01.yaml.example](../20-operations/configs/egress/android-egress-01.yaml.example)

核心字段：

- `node.name`
- `hub.endpoint`
- `hub.public_key`
- `wireguard.mode`
- `wireguard.address`
- `wireguard.private_key`
- `proxy.listen_port`

默认行为：

- `wireguard.mode` 默认为 `embedded`，也就是由 sing-box 自己创建 WireGuard endpoint。
- `wireguard.mode: external` 时，外部 WireGuard App/内核隧道负责 `10.66.0.0/24`，程序只渲染 mixed 代理，不再渲染 sing-box WireGuard endpoint。
- 如果未指定 `proxy.listen_addr`，程序会自动取 `wireguard.address` 里的 IP 作为监听地址。
- 因此第一版默认只在 WireGuard 内网地址上暴露代理，不直接绑到蜂窝公网接口。

## 运行方式

本地 Windows 开发机构建：

```powershell
cd c:\Users\xuotq\daxiang-vpn
$env:GOOS="android"
$env:GOARCH="arm64"
go build -tags with_gvisor -o dist\dxandroid-egress ./egress/proxy
```

拿到 root 安卓机后，预期运行方式：

```sh
adb push dist/dxandroid-egress /data/local/tmp/dxandroid-egress
adb push docs/20-operations/configs/egress/android-egress-01.yaml.example /data/local/tmp/android-egress.yaml

adb shell su -c 'chmod 755 /data/local/tmp/dxandroid-egress'
adb shell su -c "/data/local/tmp/dxandroid-egress validate --config /data/local/tmp/android-egress.yaml"
adb shell su -c "/data/local/tmp/dxandroid-egress run --config /data/local/tmp/android-egress.yaml --workdir /data/local/tmp/dxandroid-egress-work"
```

## 预期拓扑

```text
中国客户端
    |
    | WireGuard -> Hub
    v
Android WireGuard App
    |
    | tun0 / 10.66.0.101
    v
Android egress agent
    |
    | mixed/http proxy on 10.66.0.101:1080
    v
手机卡公网出口
```

## 下一步

当前已经验证：

1. `android/arm64` 二进制可运行。
2. `embedded` 模式能启动，但在 Android 移动网络上会反复出现 `sendmsg: message too long`，不适合作为高速出口。
3. `external` 模式可以由 WireGuard App 创建 `tun0 / 10.66.0.101`，`dxandroid-egress` 成功绑定 `10.66.0.101:1080`。
4. Hub 侧可以通过 Android 代理访问公网。

后续继续补：

1. Android App/前台服务 UI。
2. WireGuard App 开机自动拉起验证。
3. 日志轮转、崩溃重启计数、电量和温度采集。
