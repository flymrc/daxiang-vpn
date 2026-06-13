# 2026-06-14 Android 出口 QUIC over IPv4 A/B 测试

## 目标

验证 Android -> Hub 隧道腿从 IPv4 TCP/yamux 切到 IPv4 QUIC/UDP 后,是否能提升稳定性/速度或降低发热压力。本次只做临时 A/B,不替换生产 TCP/yamux。

## 临时测试拓扑

- 生产 TCP/yamux 保持不动:
  - tunnel:`36.50.84.68:39093/tcp`
  - Hub proxy:`10.66.0.1:18081`
- 临时 QUIC/UDP:
  - Hub 临时 server:`zhreverse server -transport quic -listen 0.0.0.0:39094 -proxy 10.66.0.1:18082`
  - Android 临时 client:`zhreverse client -transport quic -server 36.50.84.68:39094 -address-family ipv6 -connections 2`
  - 临时放行 UFW `39094/udp`

测试结束后已清理:

- kill Hub 临时 server PID `240878`
- kill Android 临时 client PID `30106`
- 删除临时 UFW `39094/udp`
- 验证 Hub 无 `39094/18082` 监听,Android 无 `zhreverse client -transport quic` 进程

## 结果

健康检查:

- TCP 路径:`10.66.0.1:18081` PASS,v6 出口 `240b:c010:420:43fc:0:22:6ab2:8701`,v4 出口 `133.106.32.25`
- QUIC 路径:`10.66.0.1:18082` PASS,v6/v4 出口相同

20MB Cloudflare 下载测速:

| 路径 | 样本 | avg Mbps | min Mbps | max Mbps |
| --- | ---: | ---: | ---: | ---: |
| TCP/yamux baseline | 3 | 20.16 | 13.92 | 31.84 |
| QUIC/UDP temp | 3 | 9.63 | 7.99 | 10.70 |
| TCP/yamux recheck | 3 | 38.74 | 37.94 | 39.82 |

结论:本次窗口里 QUIC/UDP 可用,但吞吐明显低于 TCP/yamux;不建议直接切生产。

## 热状态

测速后 Android:

- 电池温度约 `38.6C`
- `Thermal Status: 1`
- `VIRTUAL-SKIN` 约 `40.0C`
- `VIRTUAL-SKIN-CHARGE-WLC` 约 `43.4C`

该温升来自连续测速窗口,不能单独证明 QUIC 比 TCP 更热;但也没有看到 QUIC 在短测中带来降温收益。

## 额外发现:旧 dxreverse 残留重连循环

测试后清理验证进程时发现 Android 仍有旧进程:

- `busybox sh /data/adb/service.d/99-dxreverse-egress.sh.disabled`
- `dxreverse client --config /data/adb/dxreverse/client.yaml`

`/data/local/tmp/dxreverse-egress.log` 显示其每约 3 秒:

- `connected to reverse tcp server 36.50.84.68:39093`
- 随即 `client connection 1/1 disconnected: EOF`

这条旧进程没有稳定承载流量,但会持续唤醒蜂窝网络并造成无效重连,和发热/耗电症状高度相关。当前生产基线应只有 `99-zhreverse-egress.sh` 和 `zhreverse client`;下一步应确认后停止旧 `dxreverse` 监督脚本和 client。
