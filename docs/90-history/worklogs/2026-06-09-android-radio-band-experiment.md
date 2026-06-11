# 2026-06-09 Android 频段/小区实验

## 背景

在 `zhreverse` 数据面稳定后,继续调查 Android 设备更底层的蜂窝优化空间,重点看是否能通过 root/ADB 尝试频段、小区、网络制式等无线侧变量。

## 只读发现

- 设备平台:`ro.board.platform=taro`(Qualcomm)。
- 标准 Android `cmd phone` 支持:
  - `get-allowed-network-types-for-users`
  - `set-allowed-network-types-for-users`
  - `radio get-modem-service`
- 标准 Android `cmd phone` 未提供单 LTE band 锁定能力。
- 当前没有可用 `/dev/diag*` 暴露给 shell,也未安装 Network Signal Guru 一类锁频工具。
- 当前 APN:`rakuten.jp`。
- 当前公网出口多次变更,但 serving cell 仍为 Rakuten LTE Band 3:
  - EARFCN `1500`
  - PCI `68`
  - bandwidth `20MHz`
  - CA `false`

## LTE-only 实验

执行:

```sh
cmd phone set-allowed-network-types-for-users -s 0 01000001000000000000
```

结果:

- allowed network types 从全制式改为 `LTE|LTE_CA`。
- 重注册 3 次后仍固定在:
  - Band 3
  - EARFCN `1500`
  - PCI `68`
  - CA `false`
- 信号质量未改善:
  - RSRP 约 `-90` 到 `-91`
  - RSRQ `-17` 到 `-20`
  - SNR `-1` 到 `-4`
- 速度未改善:
  - Android 直连 Cloudflare 2MB 约 `2.0-2.7 Mbps`
  - Hub proxy 2MB 约 `0.5-2.1 Mbps`,取决于当轮无线状态

由于没有收益,已恢复全制式:

```sh
cmd phone set-allowed-network-types-for-users -s 0 11001111101111111111
```

恢复后:

- allowed network types 包含 `LTE|LTE_CA|NR` 等全制式。
- `preferred_network_mode=27,27`。

## 结论

- root/ADB 标准接口可做网络类型限制和飞行模式重注册,但不能直接锁单个 LTE band。
- 本轮低风险实验没有找到更好的频段/小区;重注册只换公网 IP/内网 rmnet 地址,serving cell 仍粘在同一 Band 3 小区。
- 下一层频段玩法需要 Qualcomm DIAG/QPST/QXDM 或 NSG 这类工具,风险明显更高;当前设备 shell 未暴露 `/dev/diag*`,不能直接从现有 ADB shell 做安全锁频。
- 当前无线侧瓶颈更像是 Rakuten 在该位置/该机型组合下的承载质量问题,而非 Android 标准网络模式设置问题。

## 更底层 AT/DIAG 试探

继续用 ADB/root 做只读和可恢复试探:

- `/dev/at_mdm0` 可回应标准查询型 AT 命令,能确认 Motorola/Qualcomm modem 固件;输出中包含 IMEI,未记录到文档。
- `AT+CLAC` 中可见 `$QCBANDPREF`、`^PREFMODE` 等厂商私有命令,但 `?` / `=?` 查询没有稳定返回;未发送任何 band/NV 写命令。
- 设备无 `/dev/diag*`,但有 `vendor.qti.diaghal@1.0::Idiag/default`,并且 `diag-router`、`cnss_diag`、`ipacm-diag` 在运行。
- 临时 `setprop sys.usb.config diag,adb` 没让 DIAG 真正进入 USB state。
- 临时 `setprop sys.usb.config diag,usbnet,adb` 可让 `sys.usb.state=diag,usbnet,adb`,DIAG `usb_connected=1`,并出现 `usb0`;随后已恢复 `sys.usb.config=adb`。再通过 `none -> adb` reset 后,DIAG `usb_connected=0`。

安全边界:

- 未改 `persist.sys.usb.config`。
- 未启动 `diag_mdlog` 长日志采集,避免写满存储。
- 未写 NV/EFS/IMEI/baseband calibration。

结论:设备确实有 Qualcomm DIAG/AT 底层入口,但安全锁频仍需要外部 DIAG/NSG/QPST 工具链;当前 ADB shell 下不适合盲写私有 band 命令。

## 数据面传输试验

频段侧没有低风险收益后,继续验证“移动网底层路径”对 `zhreverse` 的影响:

- Android 直连强制 IPv4 到 Cloudflare 2MB 出现超时,强制 IPv6 可通但速度波动。
- 尝试 Hub `resolve: client` + Android `address_family: ipv6`,未提升稳定性,2MB 平均约 `2.21 Mbps` 且仍有失败。
- 尝试 Hub `resolve: client` + Android `address_family: auto`,表现更差,2MB 平均约 `1.63 Mbps`。
- 回到 Hub `resolve: server` + Android `address_family: auto`,QUIC 单连接可达约 `6.11 Mbps`,但仍有 QUIC idle timeout 与 TLS 握手失败。
- 切换为 TCP/yamux 反向隧道,Android `connections: 1`,2MB 5 轮中 4 轮成功,平均约 `10.62 Mbps`,接近当时 Android 本机直连水平。

线上最终保留:

- Hub `/etc/zongheng/zhreverse/server.yaml`: `transport: tcp`, `resolve: server`。
- Android `/data/adb/zhreverse/client.yaml`: `transport: tcp`, `connections: 1`, `address_family: auto`。
- Hub UFW 新增 `39093/tcp`,保留 `39093/udp` 作为 QUIC 回滚入口。
- Hub 新增持久化 QUIC cert/key,Android config 保留 `server_cert_sha256` 供 QUIC 回滚。

当前判断:比继续尝试 Band lock 更有效的底层优化是避开 Rakuten 当前 UDP/QUIC 抖动,用 TCP/yamux 单连接承载 reverse tunnel。
