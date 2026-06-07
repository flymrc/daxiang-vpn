# egress/android-control

出口手机的**远程控制 + 自愈**能力。让你即使没有 ADB,也能像连服务器一样
从 Hub 经 WireGuard 隧道(`10.66.0.101`)登录并控制安卓出口机。

## 组成

| 文件 | 部署到手机 | 作用 |
| --- | --- | --- |
| `service.d/98-dxandroid-control.sh` | `/data/adb/service.d/98-dxandroid-control.sh` | Magisk 开机自启,拉起看门狗 |
| `watchdog.sh` | `/data/adb/dxandroid/watchdog.sh` | 本地周期自检:保证 dropbear、egress 在跑 |
| `authorized_keys.example` | `/data/adb/dxandroid/authorized_keys`(填真实公钥) | 允许登录的 Hub 公钥 |

配套的 Hub 侧被动存活探针在 [scripts/watch-android-egress-liveness.sh](../../scripts/watch-android-egress-liveness.sh)。

## 工作原理

- 手机 root(Magisk),WireGuard 隧道内有固定内网 IP `10.66.0.101`。
- dropbear SSH 守护进程**只绑定 `10.66.0.101:22`**:公网够不到,只有隧道内 peer(Hub)能连;**仅密钥登录**(`-s -g` 关闭密码)。
- 从 Hub:`ssh -i ~/.ssh/dxandroid_control -p 22 root@10.66.0.101` → 完整 root shell。
- watchdog 每 30s 自检,dropbear/egress 挂了就本地重拉,减少需要人工远程介入的场景。

## 边界(务必知道)

- **依赖隧道在线**:隧道断了(手机没网 / WireGuard App 被杀 / Doze 冻死 / 刚开机未连)就连不上——这正是最需要控制的时刻。所以 watchdog 的本地自愈是关键兜底。
- **关机 / 没电 / 彻底离线**:任何带内方案都无解,只能物理接触。
- **shell ≠ 触屏**:UI 点按需 `input`/uiautomator;刷机/bootloader 级操作脱离 shell。

## 部署前准备

1. **dropbear 二进制(arm64)**:Android 无内置 sshd。需放置 `dropbear` 与 `dropbearkey` 到 `/data/adb/dxandroid/bin/`(来源:Magisk 模块,或静态交叉编译)。
2. **密钥**:在 Hub 生成专用密钥对,公钥写入手机的 `authorized_keys`(见 `authorized_keys.example`)。
3. **authorized_keys 路径**:dropbear 默认读登录用户 home 下 `~/.ssh/authorized_keys`。Android 无标准 passwd,**需在目标设备实测**确认 dropbear 实际读取路径;必要时用 `HOME` 或 dropbear 的授权文件选项对齐(脚本里已用 `HOME=$BASE`,部署时验证)。

## 部署步骤(ADB,经确认后执行)

```powershell
$adb="$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
# 1. 目录
& $adb shell su -c "mkdir -p /data/adb/dxandroid/bin /data/adb/dxandroid/keys"
# 2. 推脚本
& $adb push egress/android-control/watchdog.sh /data/local/tmp/watchdog.sh
& $adb shell su -c "cp /data/local/tmp/watchdog.sh /data/adb/dxandroid/watchdog.sh && chmod 700 /data/adb/dxandroid/watchdog.sh"
& $adb push egress/android-control/service.d/98-dxandroid-control.sh /data/local/tmp/98.sh
& $adb shell su -c "cp /data/local/tmp/98.sh /data/adb/service.d/98-dxandroid-control.sh && chmod 700 /data/adb/service.d/98-dxandroid-control.sh"
# 3. dropbear 二进制 + 公钥(见上,放好后)
# 4. 起看门狗(免重启验证)
& $adb shell su -c "sh /data/adb/dxandroid/watchdog.sh &"
```

## 自愈待办(TODO)

- **WG 隧道自愈**:external 模式下隧道由 WireGuard App 拥有,shell 难以可靠重建。
  可评估通过 intent 触发 App 重连(`am start`/`am broadcast` 到 WireGuard App,
  具体 action 按 App 版本实测),或改用内核 WG + `wg-quick` 由 root 直接管。
- **告警接入**:Hub 探针 `alert()` 钩子目前只打印,接 webhook/邮件。
