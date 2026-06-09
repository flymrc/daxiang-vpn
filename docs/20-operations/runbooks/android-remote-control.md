# Android 出口远程控制操作手册

不依赖 ADB,经 WireGuard 隧道像连服务器一样控制安卓出口机(`10.66.0.101`)。
控制面是自研 Go SSH 服务 `dxandroid-control`(代码:[egress/android-control/](../../../egress/android-control/))。

> 前置:连接方必须已在 WireGuard 隧道内(Hub `10.66.0.1` 或已授权的客户端,如 `10.66.0.30`)。
> 公网够不到——daemon 只绑隧道 IP。

## 1. 快速连接

```bash
ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101
```

- **端口 2022**:当前生产控制面端口,由 [watchdog.sh](../../../egress/android-control/watchdog.sh) 的 `CONTROL_LISTEN` 固定。
- **仅公钥登录**,无密码。登录即 **root**,支持交互式 PTY 与 `ssh ... "一次性命令"`。
- 私钥当前在管理机 `~/.ssh/dxandroid_control`(**无 passphrase**)。要从 Hub 连,需先 `scp` 私钥到 Hub。

## 2. 端口与路径速查

| 项 | 值 |
| --- | --- |
| 控制 SSH(Go) | `10.66.0.101:2022` |
| Android 出口数据面 | `dxreverse client` 主动反连 Hub |
| Hub 侧出口代理 | `10.66.0.1:18081` |
| 二进制 | `/data/adb/dxandroid/bin/dxandroid-control` |
| 反向出口二进制 | `/data/adb/dxreverse/bin/dxreverse` |
| 看门狗 | `/data/adb/dxandroid/watchdog.sh` |
| 开机自启 | `/data/adb/service.d/98-dxandroid-control.sh` |
| 出口自启 | `/data/adb/service.d/99-dxreverse-egress.sh` |
| 授权公钥 | `/data/adb/dxandroid/.ssh/authorized_keys` |
| 主机私钥(daemon 自生成) | `/data/adb/dxandroid/keys/ssh_host_ed25519_key` |
| 运行日志 | `/data/local/tmp/dxandroid-control.log` |
| 出口日志 | `/data/local/tmp/dxreverse-egress.log` |

## 3. 从 Hub 验证(不依赖 ADB)

```bash
# 出口数据面健康(在 Hub 上跑)
./scripts/check-android-reverse-egress.sh

# 控制面 / 出口端口是否经隧道可达(在 Hub 上)
echo | timeout 3 nc 10.66.0.101 2022 | head -1     # 应回 SSH-2.0-Go
curl --proxy http://10.66.0.1:18081 https://api.ipify.org
```

被动存活探针(读 wg RX 增量,Hub 上长期跑):
[scripts/watch-android-egress-liveness.sh](../../../scripts/watch-android-egress-liveness.sh)。

## 4. 自愈机制

- `watchdog.sh` 每 30s 自检:若 `10.66.0.101` 地址缺失,会通过 WireGuard App 的 `SET_TUNNEL_UP` broadcast intent 请求拉起 `jp-android-01` 隧道;若地址存在但 Hub 内网 `10.66.0.1` 不可达,会先 `SET_TUNNEL_DOWN` 再 `SET_TUNNEL_UP` 强制重拨;`dxandroid-control` 或 `dxreverse` 挂了也会本地重拉。
- WireGuard App intent 有 120s 冷却,避免无网/无权限时刷屏。
- `service.d/98` 开机由 Magisk 拉起看门狗。
- daemon 用 `IP_FREEBIND` 绑定,隧道 IP 未就绪时也能先监听,隧道一通即可用。
- 可选每日定时重启:把 watchdog 的 `REBOOT_HOUR` 设为如 `"04"`(默认空=关闭)。

边界:**隧道断了以后带内 SSH 仍连不上**。watchdog 会在手机本机尝试重拉隧道,但手机无网、关机、没电、WireGuard App 被系统策略拦截时,仍需要物理接触或 ADB 兜底。

### dxreverse 救援通道

当 Hub/Mac 直连 `10.66.0.101:2022` 超时,但 `dxreverse` 反向出口仍可用时,可以让 Android 通过反向数据面连接自己的控制面:

```bash
ssh -i /root/.ssh/dxandroid_control_hub \
  -o 'ProxyCommand=nc -X connect -x 10.66.0.1:18081 %h %p' \
  -p 2022 root@10.66.0.101
```

这条路径用于应急修复 watchdog / WireGuard,不作为客户侧入口。

生产手机卡出口应关闭 Android Wi-Fi,避免 dxreverse 误走现场 Wi-Fi/住宅出口。检查:

```bash
cmd wifi status
curl --max-time 8 https://api.ipify.org
```

## 5. 部署 / 更新(需 ADB,改动线上前确认)

```powershell
$adb="$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
# 重新编译 arm64
# (Bash) GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/dxandroid-control ./egress/android-control
& $adb push dist/dxandroid-control /data/local/tmp/dxandroid-control
& $adb shell su -c "cp /data/local/tmp/dxandroid-control /data/adb/dxandroid/bin/dxandroid-control"
& $adb shell su -c "chmod 700 /data/adb/dxandroid/bin/dxandroid-control"
# 重启看门狗(单实例)
& $adb shell su -c "pkill -9 -f watchdog.sh"
& $adb shell su -c "pkill -9 -f bin/dxandroid-control"
& $adb shell su -c "setsid sh /data/adb/dxandroid/watchdog.sh >/dev/null 2>&1 </dev/null &"
```

新增允许登录的公钥:把公钥追加到 `/data/adb/dxandroid/.ssh/authorized_keys`(daemon 每次连接重读,免重启)。

## 6. 踩坑备忘(真机部署中踩过)

- **私钥带 passphrase 会"假死"**:OpenSSH 在非交互环境下到签名阶段要解锁私钥,弹不出提示就**卡住**,表现为 "Server accepts key" 后无限挂起、服务端最终报 `no auth passed yet`。务必用**无密码**专用密钥(`ssh-keygen -N ""`)。
- **`su -c "cmd1 && cmd2"` 后半段掉权限**:多命令/重定向经 `adb shell su -c "..."` 时后半段可能以普通用户执行(cp 成功但 chmod/重定向被拒)。**稳妥做法:把操作写进脚本,`su -c /path/script.sh` 单 token 执行**;或每条单独 `su -c`。
- **引号里的 `|`/`-flag` 被层层 shell 拆掉**:`pgrep/pkill -f '带空格 或 -开头 的模式'`、`grep 'a|b'` 经 adb→su 会被误解析。用单 token 模式(如 `pkill -f 2223`),或写进脚本。
- **`adb forward` 只能连设备 `127.0.0.1`**:测绑在 `10.66.0.101` 的生产实例时用 forward 会 EOF/连不上;要么起一个绑 `127.0.0.1` 的临时实例测,要么从隧道内(Hub)直接连。
- **后台进程要 `setsid` + 重定向**:`su -c` 里直接 `&` 起的进程会随该 shell 退出被挂断;`setsid ... >/dev/null 2>&1 </dev/null &` 才常驻、且不挂住 adb。
- **看到旧 `dxandroid-egress` 进程**:说明旧 Android 数据面残留被误启动。当前生产脚本会禁用 `/data/adb/service.d/99-dxandroid-egress.sh` 并停止 `dxandroid-egress`;正常状态只应看到 `99-dxreverse-egress.sh` 和 `dxreverse client`。

## 7. 切换出口公网 IP(不重启)

让蜂窝无线电向运营商重注册,通常会拿到新公网 IP;隧道 IP `10.66.0.101` 不变,切换后 WireGuard 自动重握手恢复。脚本:[egress/android-control/rotate-ip.sh](../../../egress/android-control/rotate-ip.sh)(部署在 `/data/adb/dxandroid/rotate-ip.sh`)。

Windows 一键(推荐,自动换 + 等待 + 打印前后出口 IP):

```powershell
.\scripts\rotate-android-egress-ip.ps1
.\scripts\rotate-android-egress-ip.ps1 -DownSeconds 12 -WaitSeconds 40
```

CLI 客户端也内置了同等入口，会先后打印当前本地代理看到的出口公网 IP：

```powershell
dxvpn.exe rotate-ip
dxvpn.exe rotate-ip --down-seconds 12 --wait-seconds 90
dxvpn.exe rotate-ip --direct --phone 10.66.0.101 --port 2022 --key "$HOME\.ssh\dxandroid_control"
dxvpn.exe rotate-ip --direct --jump root@36.50.84.68 --key "$HOME\.ssh\dxandroid_control"
```

默认 `dxvpn.exe rotate-ip` 由 Hub API 代为触发 Android 控制面,客户机不需要 Android SSH 私钥。`--wait-seconds` 是最大等待时间,CLI 会轮询到出口恢复或超时。`--direct` 是管理员排障入口;普通 `dxvpn.exe start` 是用户态代理模式,不会给 Windows 系统添加 `10.66.0.0/24` 路由,管理员直连时若本机不能直连 `10.66.0.101:2022`,使用 `--jump` 经 Hub 跳板。

手动(任意平台,SSH 远程触发手机上的脚本):

```bash
# rotate-ip.sh 内部用 setsid 脱离会话,避免切换瞬间断链把自己锁死
ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101 'sh /data/adb/dxandroid/rotate-ip.sh'
ssh ... 'sh /data/adb/dxandroid/rotate-ip.sh 12'   # 自定义断网秒数(默认 8)

# 等 ~20-40s 重连后核对新出口 IP
ssh root@36.50.84.68 'curl -s -x http://10.66.0.1:18081 https://api.ipify.org'
# 或 ./scripts/check-android-egress-health.ps1 看 egress_ip
```

> 注意:`rotate-ip.sh` 是手机上的脚本(`#!/system/bin/sh`),**不能在 Windows 本地直接运行**,只能经 SSH 远程触发。

⚠️ **绝不能在前台 SSH 里直接敲飞行模式开关**——"开飞行"会当场切断你的会话,"关飞行"还没跑手机就一直离线、把你锁在外面。务必走 `rotate-ip.sh`(它 `setsid` 脱离执行,断线也会自动把网络恢复)。

注意:运营商可能仍返回相同/粘性 IP,不保证每次都变(多切几次或加大断网秒数);切换瞬间出口中断十几秒。实测一次:`133.106.140.188` → `133.106.35.50`。

## 8. 排查慢速 / 换卡自检

速度慢时,先用 `sim-info.sh` 一览当前卡与小区状态(运营商/APN/网络模式/省流/SNR/频段/漫游/出口 IP),换不同运营商的卡时也跑一次确认设置无误:

```bash
ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101 'sh /data/adb/dxandroid/sim-info.sh'
# 或 USB: adb shell su -c 'sh /data/adb/dxandroid/sim-info.sh'
```

判断要点:
- **APN / 网络模式 / 省流**:APN 应自动匹配该卡;`preferred_network_mode=27` 为全制式;省流应关。这些对了就不是"设置"问题。
- **SNR(rssnr)是关键**:`>10` 好,`1~3` 差。**信号格数满但 SNR 低 = 小区拥塞/干扰**,这是慢速最常见主因,与卡的设置无关。
- **慢就重选小区**:跑 `rotate-ip.sh`(飞行模式重注册)——它不只是换 IP,本质是强制重新选小区,能把手机从拥塞小区"踢"走。实测一次:重选后 serving SNR `1→6`,出口测速 `2.2 → 13 Mbps`(约 6×)。
- 不同运营商的速度差异,绝大部分来自该运营商在此位置的**覆盖/拥塞**,而非手机里要手调的设置。

## 9. 电池(待办)

`/sys/class/power_supply/battery/charge_control_limit` 是 **0–11 充电电流档位**(限流/降温),**不是 SoC 百分比上限**;本机无充电开关/SoC 封顶节点。
作为 24/7 出口需插**独立墙充**(用 PC 的 USB 口供电可能不足以覆盖运行消耗,会净放电)。
仅当确认长期顶在高电量时,限流/限上限才有意义——届时再评估。
