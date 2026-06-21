# 2026-06-21 Android rotate-ip 控制隧道超时排查

## 背景

用户反馈 Android 机换 IP 功能坏了,手机已通过 USB 连接。本次以只读排查为主:未触发真实 `rotate-ip`,未重启 Hub/手机,未修改生产服务状态。

## 结论

这次失败不是 `/data/adb/zhandroid/rotate-ip.sh` 脚本损坏,而是 Hub 在故障窗口连不到 Android 控制面 `10.66.0.101:2022`。

Hub `zhhub.service` 日志显示今天三次换 IP 请求失败:

- `2026-06-21 10:16:35 UTC`
- `2026-06-21 10:29:30 UTC`
- `2026-06-21 10:44:14 UTC`

错误均为:

```text
ssh: connect to host 10.66.0.101 port 2022: Connection timed out
```

Android 侧 `/data/local/tmp/zhandroid-control.log` 显示从本地时间 `2026-06-21 13:00` 到 `20:43` 左右,watchdog 一直在等待 WireGuard 隧道地址:

```text
wireguard unhealthy; requesting tunnel UP via WireGuard App intent (tunnel=jp-android-01)
control deferred; 10.66.0.101 not present yet
```

直到 `2026-06-21 20:43:49 JST` 才出现:

```text
started zhandroid-control on 10.66.0.101:2022
```

因此换 IP 失败的直接原因是控制隧道 `tun0 / 10.66.0.101` 长时间未建立,Hub 无法 SSH 到手机执行 rotate 脚本。

## 当前状态

排查时 USB ADB 可用:

- 设备:`Pixel_7a`,ADB `device`
- `su` 正常:`uid=0(root)`
- 飞行模式当前关闭:`airplane_mode_on=0`
- Android 上 `rotate-ip.sh` 存在且可执行,hash 与仓库一致
- `zhandroid-control`、`watchdog.sh`、`zhreverse client` 均在运行
- `tun0` 当前存在:`10.66.0.101/24`
- Hub 当前可 `ping 10.66.0.101`,且 `nc 10.66.0.101 2022` 成功
- Hub 通过控制面执行 `echo control-exec-ok` 成功
- Hub 通过控制面执行 `sh -n /data/adb/zhandroid/rotate-ip.sh` 成功
- 当前 Android reverse 出口 IPv6 返回 Rakuten `240b:*`

## 额外观察

Hub `zhhub` 在客户端 bootstrap 时会同步 SSH 到 Android 控制面读取运营商名:

```text
getprop gsm.operator.alpha; getprop gsm.sim.operator.alpha
```

今天 `cn-client-010` 从 `109.123.252.167` 以约 3 秒频率反复 bootstrap,导致 Hub 高频派生 carrier-probe SSH,手机控制日志出现大量 `accept key` / `login`。这不是 rotate 失败的根因,但会放大控制面日志噪声和故障期间的连接压力。

## 后续建议

- 短期:若再出现同类失败,先检查 Hub 到 `10.66.0.101:2022` 的连通性和手机 `tun0` 是否存在,不要先怀疑 `rotate-ip.sh`。
- 稳定性:增强 watchdog 对 WireGuard App 长时间不响应 `SET_TUNNEL_UP` 的兜底策略,例如连续失败后记录更明确原因或尝试更强的 WireGuard App 重拉流程。
- Hub 降噪:考虑给 Android carrier probe 加缓存/节流,避免每次 bootstrap 都 SSH 手机,尤其是 GUI/客户端高频轮询时。
