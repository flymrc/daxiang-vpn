# Android 出口节点客户端

## 目标

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

- 连接 Hub。
- 在 WireGuard 内网地址上监听 `1080` 代理端口。
- 将收到的流量直接从手机卡网络出口发出。

## 当前实现范围

已新增一个新的命令入口：

```text
frontend/dxvpn/cmd/dxandroid-egress
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
- 自动 daemonize。
- iptables 策略路由增强。
- Hub 侧 egress token/bootstrap。

## 配置模型

示例配置见：

- [android-egress-01.yaml.example](../20-operations/configs/egress/android-egress-01.yaml.example)

核心字段：

- `node.name`
- `hub.endpoint`
- `hub.public_key`
- `wireguard.address`
- `wireguard.private_key`
- `proxy.listen_port`

默认行为：

- 如果未指定 `proxy.listen_addr`，程序会自动取 `wireguard.address` 里的 IP 作为监听地址。
- 因此第一版默认只在 WireGuard 内网地址上暴露代理，不直接绑到蜂窝公网接口。

## 运行方式

本地 Windows 开发机构建：

```powershell
cd frontend\dxvpn
$env:GOOS="android"
$env:GOARCH="arm64"
go build -o ..\..\dist\dxandroid-egress ./cmd/dxandroid-egress
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
Android egress agent
    |
    | mixed/http proxy on 10.66.0.100:1080
    v
手机卡公网出口
```

## 下一步

今晚设备连上后，优先做这几件事：

1. 确认 `android/arm64` 二进制可运行。
2. 确认 sing-box 的 wireguard endpoint 在 root 安卓上可启动。
3. 确认代理端口能绑定到 WireGuard 地址。
4. 从 Hub 上确认该节点握手正常。
5. 再补后台保活、前台服务和自恢复。

