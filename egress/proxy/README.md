# egress/proxy

跨平台出口代理(Go,基于 sing-box)。一套代码,多平台出口节点共用。

## 构建目标

| 平台 | 用途 | 状态 |
| --- | --- | --- |
| `darwin/arm64` | **Mac 出口**(当前 Mac mini `10.66.0.100` 出口,计划从裸 sing-box+launchd 迁到本代码) | 🅿️ 预留 |
| `windows/amd64` | **Windows PC 出口** | 🅿️ 预留 |

```bash
# Mac 出口(预留)
GOOS=darwin GOARCH=arm64 go build -tags with_gvisor -o dist/dxegress-macos   ./egress/proxy
# Windows 出口(预留)
GOOS=windows GOARCH=amd64 go build -tags with_gvisor -o dist/dxegress-win.exe ./egress/proxy
```

> 配置示例见 [docs/20-operations/configs/egress/](../../docs/20-operations/configs/egress/)。
> Mac 出口现状(LaunchDaemon / sing-box / WireGuard)见 [运维诊断手册](../../docs/20-operations/runbooks/diagnostics.md) 第 2 节;迁到本代码前那套仍是事实来源。
