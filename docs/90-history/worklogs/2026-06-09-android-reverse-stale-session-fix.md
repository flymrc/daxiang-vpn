# 2026-06-09 Android reverse stale session 修复

## 背景

ADB 全量调查发现 Android 本机直连公网能跑通,但 Hub 侧经 `10.66.0.1:18081` 代理大量卡在 CONNECT 阶段。Hub proxy TCP 已接受连接,但等待 Android 反向 QUIC stream 回应时超时。

同时确认手机上的 `/data/adb/service.d/99-zhreverse-egress.sh` 和 `/data/adb/zhandroid/watchdog.sh` 仍是旧版本,此前提交的 Wi-Fi lock、buffer tuning、默认路由检查等底层基线尚未部署到设备。

## 修复

- `egress/reverse/main.go`
  - Hub server 在发送 `CONNECT` / `FETCH` 命令并等待 Android 响应时设置 deadline。
  - 若 reverse session 半死或不回应,从 session 池剔除并重试其它 session。
  - 解决 stale QUIC session 被轮询选中后让 HTTP proxy 请求长期挂住的问题。
- `egress/reverse/main_test.go`
  - 新增 stale session 回归测试:第一条 session 接命令但不响应,Hub 应清掉它并使用下一条健康 session。
- Android 设备
  - 通过 ADB 部署新版 `99-zhreverse-egress.sh` 和 `watchdog.sh`。
  - 清理重复启动的 `99-zhreverse-egress.sh` / `zhreverse client`,恢复为单 supervisor + 单 client。
  - 日志确认 `sysctl` tuning 和默认蜂窝路由检查已实际执行。
- Hub
  - 重新构建并替换 `/opt/zongheng/zhreverse/zhreverse`。
  - 重启 `zhreverse-hub.service`。

## 验证

```powershell
go test ./...
powershell -ExecutionPolicy Bypass -File scripts/check-android-shell-baseline.ps1
git diff --check
powershell -ExecutionPolicy Bypass -File scripts/check-android-egress-health.ps1
powershell -ExecutionPolicy Bypass -File scripts/measure-android-egress.ps1 -Runs 5 -TimeoutSeconds 50 -Url https://speed.cloudflare.com/__down?bytes=2000000 -FallbackUrl ""
```

结果:

- `check-android-egress-health.ps1`:全部 PASS。
- Hub 经 Android 出口 IP:`133.106.164.74`。
- 2MB 代理测速 5/5 成功:
  - avg `2.51 Mbps`
  - min `1.14 Mbps`
  - max `4.22 Mbps`
- Hub journal 看到旧半死 session 被 deadline 清理:
  - `read reverse command response ... timeout`
  - 随后 `api.ipify.org`、`ifconfig.me/ip`、`icanhazip.com`、Cloudflare 1MB 均成功。

## 仍需关注

当前 Rakuten LTE 小区质量仍差:Band 3、无 CA、`RSRQ=-17`、`SNR=1`。修复后代理不再被 stale session 卡死,但吞吐上限仍受蜂窝小区质量限制。
