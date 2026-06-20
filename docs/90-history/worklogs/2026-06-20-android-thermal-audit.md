# 2026-06-20 Android 出口发热只读排查

## 背景

用户反馈 Android 手机明显发热,手机已接 USB。本次只做只读排查,没有重启、换 IP、停止服务或修改线上配置。

USB ADB 初始能看到设备但状态为 `unauthorized`;后续手机侧授权后,已通过 USB ADB 直连复核关键指标。第一轮现场数据主要通过 Hub `root@36.50.84.68` 经 Android 控制面 `10.66.0.101:2022` 读取。

## 现场状态

- 出口健康:Hub `check-android-egress-health.ps1` 全 PASS。
- reverse session:`session_count=2`,当前 `active_proxy_connections=1`。
- 当前出口:v6 为 Rakuten `240b:*`,v4 为手机公网 `133.106.32.23`,没有 Hub VPS 兜底回归。
- WireGuard:Hub 到 Android peer handshake 新鲜,Hub 路由 MTU `1120`,TCPMSS `1080` 正常。
- 电池:USB 供电中,电量 `57-59%`,电池温度约 `37.2-37.6C`,dumpsys battery `temperature=372-376`。
- Thermal:`Thermal Status: 1`;`VIRTUAL-SKIN` 约 `38.2-38.7C`;`VIRTUAL-SKIN-CHARGE-WLC` 约 `40.6-40.9C`,状态 `2`。
- CPU:即时 `top` 中 `zhreverse` 约 `0-3.7%`,`com.wireguard.android` 曾约 `7%`,`zhandroid-control` 曾约 `3.5%`,整体 CPU 大部分空闲。
- 进程基线:仅看到 `99-zhreverse-egress.sh`,`zhreverse client`,`watchdog.sh`,`zhandroid-control`;未见旧 `dxreverse` / `zhandroid-egress` 残留。
- 网络连接:当前只有两条到 Hub `36.50.84.68:39093` 的 `zhreverse` 长连接,一条走 `wlan0`,一条走 `rmnet1`;另有一条当前健康探针目标连接。
- Wi-Fi:已连接 `4CFBFED8AA05-5G`,RSSI 约 `-50`,链路质量好。
- Doze:`mLightEnabled=false`,`mDeepEnabled=false`,`mState=ACTIVE`,与 2026-06-13 排查一致,设备空闲省电仍未启用。
- SIM/蜂窝:Rakuten LTE/IWLAN,serving 小区约 `rsrp=-82`,`rsrq=-14`,`rssnr=2`,信噪比偏差/拥塞迹象仍在。

## 额外观察

Hub `/debug/session-health` 当前只有 1 条活跃代理连接,但进程生命周期内的 `active_proxy_connections_peak=96`;多个客户端历史上到过单客户端上限 `48`。最近失败里有 `proxy busy for client`,说明今天早些时候出现过高并发代理压力。

`zhreverse-egress.log` 未见持续每 3 秒重连的旧残留循环。最近主要是 Wi-Fi/蜂窝 fallback 切换、目标站 `api64.ipify.org` 偶发 timeout/redial,以及 2026-06-20 03:06 UTC 左右短暂 `no usable reverse client session` 后恢复。

## 初步判断

当前发热不像 CPU 打满,也不像旧 `dxreverse` 残留重连。更可能是这些因素叠加:

1. 手机正在 USB 充电,且 thermal 已在充电相关 skin sensor 上进入轻度/中度状态。
2. `zhreverse` 维持两条 Hub 长连接,同时 Doze/DeviceIdle 关闭,屏幕灭了也不会明显降频/休眠。
3. 上午曾出现 Hub 侧代理并发打满,会让手机目标拨号腿和无线电短时间持续工作。
4. Rakuten LTE 当前信噪比一般,蜂窝目标腿重试/保活会额外耗电和发热。

## 建议

- 短期先观察降温:拔掉 USB 或换独立墙充/低热供电,避免 PC USB 供电 + 出口流量叠加。
- 若允许影响出口可用性,下一步可做 A/B:短暂停 `zhreverse` 10-15 分钟或临时降 `connections: 1`,观察温度回落速度。
- 若继续 24/7 运行,应评估恢复 DeviceIdle/Doze 或给空闲时段降低健康探针/连接压力;改动前需要确认对出口稳定性的影响。
