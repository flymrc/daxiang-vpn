# egress/android-control

出口手机的**远程控制 + 自愈**能力。让你即使没有 ADB,也能像连服务器一样
从 Hub 经 WireGuard 隧道(`10.66.0.101`)登录并控制安卓出口机。

控制面是一个**自研的极简 Go SSH 服务**(`dxandroid-control`),不依赖 dropbear/Termux,
用和 `dxandroid-egress` 同一套 Go 工具链交叉编译到 `linux/arm64`。

## 组成

| 文件 | 部署到手机 | 作用 |
| --- | --- | --- |
| `main.go` + `freebind_*.go` | 编译产物 `/data/adb/dxandroid/bin/dxandroid-control` | Go SSH 服务:绑隧道 IP、仅公钥、PTY shell |
| `watchdog.sh` | `/data/adb/dxandroid/watchdog.sh` | 本地自检:保证 control/egress 在跑,(可选)每日重启 |
| `service.d/98-dxandroid-control.sh` | `/data/adb/service.d/98-dxandroid-control.sh` | Magisk 开机自启,拉起看门狗 |
| `authorized_keys.example` | `/data/adb/dxandroid/authorized_keys`(填真实公钥) | 允许登录的 Hub 公钥 |

配套 Hub 侧被动存活探针:[scripts/watch-android-egress-liveness.sh](../../scripts/watch-android-egress-liveness.sh)。

## 为什么用 Go SSH 而不是 dropbear

- **工具链现成且已验证**:`dxandroid-egress` 就是同样 `GOOS=linux GOARCH=arm64 go build` 交叉编译后跑在这台手机上的,无需 NDK/WSL/dropbear 源码。
- **自己的代码,可审计**,不引入外部二进制(供应链更干净)。
- **天生只绑隧道 IP**:daemon 直接 `Listen("10.66.0.101:22")`,公网网卡上不开端口——dropbear 的核心优点它也有。
- **依赖已在仓库**:`golang.org/x/crypto/ssh` 早已是 go.mod 间接依赖,只新增 `github.com/creack/pty`(PTY 支持)。

## 关键设计

- **只绑隧道 IP**:默认 `-listen 10.66.0.101:22`。`10.66.0.101` 是配置钉死的(WireGuard App `Address` + Hub `AllowedIPs`),永不变,不存在绑错值。
- **绑定时机**:用 `IP_FREEBIND` 允许在 tun0/地址尚未就绪时也能 bind,开机即可监听,隧道一通立即可用;watchdog 再兜底保活。
- **仅公钥认证**:每次连接重新读 `authorized_keys`,改公钥免重启;无密码登录。
- **root shell**:进程由 Magisk 以 root 拉起,登录即 root。支持交互式 PTY(top/vi 可用)与 `ssh host "cmd"` 一次性执行。

## 边界(务必知道)

- **依赖隧道在线**:隧道断了(手机没网 / WireGuard App 被杀 / Doze 冻死)就连不上——这正是最需要控制的时刻。watchdog 的本地自愈是关键兜底。
- **关机 / 没电 / 彻底离线**:任何带内方案都无解,只能物理接触。
- **shell ≠ 触屏**:UI 点按需 `input`/uiautomator;刷机/bootloader 级操作脱离 shell。

## 构建

```bash
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/dxandroid-control ./egress/android-control
```

## 部署(ADB,经确认后执行)

```powershell
$adb="$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
# 1. 目录
& $adb shell su -c "mkdir -p /data/adb/dxandroid/bin /data/adb/dxandroid/keys"
# 2. 推二进制 + 脚本
& $adb push dist/dxandroid-control /data/local/tmp/dxandroid-control
& $adb shell su -c "cp /data/local/tmp/dxandroid-control /data/adb/dxandroid/bin/ && chmod 700 /data/adb/dxandroid/bin/dxandroid-control"
& $adb push egress/android-control/watchdog.sh /data/local/tmp/watchdog.sh
& $adb shell su -c "cp /data/local/tmp/watchdog.sh /data/adb/dxandroid/watchdog.sh && chmod 700 /data/adb/dxandroid/watchdog.sh"
& $adb push egress/android-control/service.d/98-dxandroid-control.sh /data/local/tmp/98.sh
& $adb shell su -c "cp /data/local/tmp/98.sh /data/adb/service.d/98-dxandroid-control.sh && chmod 700 /data/adb/service.d/98-dxandroid-control.sh"
# 3. 授权公钥(在 Hub 生成密钥,公钥写入手机),见 authorized_keys.example
# 4. 起看门狗(免重启验证)
& $adb shell su -c "sh /data/adb/dxandroid/watchdog.sh &"
```

从 Hub 连接:

```bash
ssh -i ~/.ssh/dxandroid_control -p 22 root@10.66.0.101
```

## TODO

- **WG 隧道自愈**:external 模式下隧道由 WireGuard App 拥有,shell 难以可靠重建;
  可评估 intent 触发 App 重连,或改内核 WG + `wg-quick` 由 root 直管。
- **Hub 侧 iptables**(可选加固):即便已只绑隧道 IP,仍可在 Hub 进一步限制只放行特定来源。
- **告警接入**:Hub 探针 `alert()` 钩子目前只打印,接 webhook/邮件。
