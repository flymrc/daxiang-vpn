# 2026-06-14 Android 旧 dxreverse 残留清理

## 背景

QUIC A/B 测试清理验证时发现 Android 仍有旧 `dxreverse` 残留:

- `/data/adb/service.d/99-dxreverse-egress.sh.disabled` 仍是可执行文件。
- 进程树中存在 `busybox sh /data/adb/service.d/99-dxreverse-egress.sh.disabled` 和 `dxreverse client --config /data/adb/dxreverse/client.yaml`。
- `/data/local/tmp/dxreverse-egress.log` 显示每约 3 秒连接 Hub `36.50.84.68:39093` 后立刻 `EOF`,形成无效重连循环。

该残留不承载生产流量,但会反复唤醒蜂窝网络,与 Android 出口发热/耗电症状相关。生产基线应只有 `99-zhreverse-egress.sh` supervisor 和 `zhreverse client`。

## 操作

已执行:

- 将旧脚本移出 Magisk `service.d`:
  - from `/data/adb/service.d/99-dxreverse-egress.sh.disabled`
  - to `/data/adb/service.d-retired/99-dxreverse-egress.sh.disabled.retired-20260614-002017`
- 对 retired 文件设置 `600` 权限,避免被误执行。
- 停止旧进程:
  - supervisor PID `1253`
  - client PID `1403`

未删除 `/data/adb/dxreverse/` 目录和 token/config,仅停止并移出开机执行路径,保留回溯材料。

## 验证

- 等待一个重连周期后,`ps` 中只剩:
  - `busybox sh /data/adb/service.d/99-zhreverse-egress.sh`
  - `zhreverse client --config /data/adb/zhreverse/client.yaml`
- `service.d` 中只剩 `99-zhreverse-egress.sh`,无 `dxreverse`。
- `ss` 中只看到 `zhreverse` 两条到 Hub `36.50.84.68:39093` 的 ESTAB 连接。
- `scripts/check-android-egress-health.ps1` 全 PASS:
  - v6 出口 `240b:c010:420:43fc:0:22:6ab2:8701`
  - v4 出口 `133.106.32.25`
  - Hub route MTU `1120`
  - TCPMSS `1080`
  - WireGuard handshake fresh
