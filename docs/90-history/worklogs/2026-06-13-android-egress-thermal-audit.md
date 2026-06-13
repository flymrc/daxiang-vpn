# 2026-06-13 Android 出口发热/耗电只读排查

## 背景

用户反馈 Android 手机出口「无缘无故发热」。本次仅做只读检查,不重启、不换 IP、不改服务状态。

## 现场状态

- Android 出口在线:`zhreverse client` 运行,Hub 侧 `10.66.0.1:18081` 可用。
- 电池:`level=24`,未充电,电池温度约 `36.0-36.3C`。
- Thermal:`Thermal Status: 0`,但 `VIRTUAL-SKIN-CHARGE-WLC` 约 `37.4C`,机身有轻微偏热迹象。
- CPU:即时 `top` 中 `zhreverse` / `zhandroid-control` CPU 约 0,没有发现高 CPU 进程。
- 60 秒采样:流量不大,`tun0` 仅增加约几十 KB,不是大下载导致。
- 蜂窝链路:`zhreverse` 到 Hub 保持 2 条 TCP 长连接,系统把其移动网络耗电归到 root/UID 0。
- 电量统计:约 8 小时屏幕关闭期间掉电约 75%,`UID 0` 的 `mobile_radio` 是最大耗电项。
- Doze:`cmd deviceidle enabled` 返回 `0`,`mLightEnabled=false`,`mDeepEnabled=false`,`mState=ACTIVE`,说明轻度/深度 idle 当前未启用。
- 信号:运营商 Rakuten LTE,RSRP 约 `-85dBm`,但 RSRQ 到 `-15/-20`,RSSNR 多条为负,更像干扰/拥塞环境。

## 初步判断

当前发热不像 CPU 打满或真实大流量,更像以下因素叠加:

1. 手机作为出口需要长期保持蜂窝数据和两条到 Hub 的反向 TCP 隧道。
2. Android Doze/DeviceIdle 当前关闭,屏幕灭了也没有进入轻度/深度 idle。
3. Rakuten LTE 当前无线质量一般,上行保活/重传和周期性健康探测会让 modem 长时间耗电。

## 后续建议

- 若可以接受短暂观察窗口,先恢复 DeviceIdle/Doze,再观察 30-60 分钟掉电和温度。
- 若仍热,做 A/B:短暂停 `zhreverse` 或降到 `connections: 1` 对比温度和掉电;这会影响出口可用性/韧性,需确认后再操作。
- 长期优化方向仍是让隧道腿走 IPv6 或降低蜂窝长连接保活成本,避免 Rakuten IPv4/CGNAT 长连接长期顶着 modem。
