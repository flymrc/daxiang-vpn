# 2026-06-14 zhreverse Hub 健康优先 session 调度

## 背景

用户希望按 ROI 继续优化 Android 出口速度/稳定性。QUIC over IPv4 A/B 显示当前窗口吞吐不如 TCP/yamux,因此下一步选择不增加协议复杂度的 Hub 侧调度优化:让 `connections: 2` 的两条反向隧道不再盲目轮询,而是优先使用更健康的 session。

## 改动

`egress/reverse/main.go`:

- `sessionManager` 增加 per-session health state:
  - active stream 数
  - 连续失败数
  - 最近失败时间
  - reverse command RTT EWMA
- `openCommand` / `openStream` 改用 `reserveSession`:
  - 选择 active 少、RTT 低、近期无失败的 session。
  - 成功 command 更新 RTT EWMA。
  - stream 关闭时通过 `trackedConn` 释放 active 计数。
  - command 阶段失败仍沿用旧逻辑:记录失败、剔除该 session、重试其它 session。
- 保持现有 stale session 防护语义不变。

`egress/reverse/main_test.go`:

- 保留 `TestOpenCommandDropsStaleSession`。
- 新增 `TestOpenCommandPrefersLowerRTTSession`。
- 新增 `TestOpenCommandPrefersIdleSessionOverLowerRTTBusySession`。

文档:

- [egress/reverse/README.md](../../../egress/reverse/README.md) 将 server 行为从 round-robin 更新为健康优先。
- [android-egress-agent.md](../../30-implementation/android-egress-agent.md) 同步 Hub session 调度说明。
- [android-egress-polish-plan.md](../../30-implementation/android-egress-polish-plan.md) 新增第 7 项并标记代码完成、待部署。

## 验证

- `gofmt -w egress/reverse/main.go egress/reverse/main_test.go`
- `go test ./egress/reverse`:通过。
- `go test ./...`:通过。

## 生产部署

已部署到 Hub:

- 本地构建:`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/zhreverse-linux-amd64-health-picker ./egress/reverse`
- SHA256:`ebde771d2a29e6f68ebc33605f874aeabdc9255cd821e414c562b94fd35cbe6a`
- 上传:`/tmp/zhreverse-health-picker`
- 备份旧二进制:`/opt/zongheng/zhreverse/zhreverse.bak-20260614-health-picker`
- 安装:`/opt/zongheng/zhreverse/zhreverse`
- 重启:`systemctl restart zhreverse-hub.service`
- 新 PID:`243942`

部署后验证:

- `zhreverse-hub.service`:active。
- Hub 监听:
  - `*:39093`
  - `10.66.0.1:18081`
- Android 两条 reverse TCP session 已重连:
  - `133.106.32.25:33175`
  - `133.106.32.25:12127`
- `scripts/check-android-egress-health.ps1`:全 PASS。
- 20MB x2 下载测速:平均约 `24.98 Mbps`,范围 `20.98-28.97 Mbps`。
- 重启后 5 分钟日志无新增错误。

## 回滚

恢复 Hub 旧二进制并重启:

```bash
cp /opt/zongheng/zhreverse/zhreverse.bak-20260614-health-picker /opt/zongheng/zhreverse/zhreverse
systemctl restart zhreverse-hub.service
```

Android 手机端无需回滚配置。
