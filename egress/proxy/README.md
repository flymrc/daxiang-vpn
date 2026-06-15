# egress/proxy

跨平台出口代理(Go,基于 sing-box)。该路线已于 2026-06-15 标记为弃用,只保留历史参考和可能的实验代码。当前生产出口数据面是 `egress/reverse` 的 Android `zhreverse`。

## 构建目标

| 平台 | 用途 | 状态 |
| --- | --- | --- |
| `darwin/arm64` | **Mac 出口**(旧 `10.66.0.100:1080` 路线) | Deprecated |
| `windows/amd64` | **Windows PC 出口** | Deprecated / 未进入生产 |

```bash
# Deprecated Mac 出口
GOOS=darwin GOARCH=arm64 go build -tags with_gvisor -o dist/zhegress-macos   ./egress/proxy
# Windows 出口(预留)
GOOS=windows GOARCH=amd64 go build -tags with_gvisor -o dist/zhegress-win.exe ./egress/proxy
```

> 配置示例见 [docs/20-operations/configs/egress/](../../docs/20-operations/configs/egress/)。
> Mac 出口现状(LaunchDaemon / sing-box / WireGuard)见 [运维诊断手册](../../docs/20-operations/runbooks/diagnostics.md) 第 2 节;该路径只用于历史诊断,不再作为新数据面事实来源。
