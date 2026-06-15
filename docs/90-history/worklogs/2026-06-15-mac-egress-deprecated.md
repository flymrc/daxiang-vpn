# 2026-06-15 Mac 出口路线弃用标注

## 背景

easyJet / FlightConnections 调查确认当前 Mac mini 出口与本机直连公网 IP 相同,继续把 Mac `10.66.0.100:1080` 作为验证出口不能解决 IP 信任问题。

## 决策

- Mac mini `10.66.0.100:1080` 出口路线标记为 deprecated。
- Mac WireGuard/sing-box 可保留为历史配置、管理内网和只读诊断对象。
- 新客户端、自动调度、专项爬虫验证和后续默认 token 不再指向 Mac 出口。
- 默认数据面使用 Android `zhreverse` Hub 入口 `10.66.0.1:18081`。

## 已同步文档

- `README.md`
- `docs/README.md`
- `docs/10-architecture/system-architecture.md`
- `docs/10-architecture/egress-strategy.md`
- `docs/20-operations/runbooks/diagnostics.md`
- `docs/20-operations/runbooks/server-access.md`
- `hub/config/tokens.example.yaml`
- `egress/proxy/README.md`

## 后续

- 新 token 配置继续默认绑定 Android `jp-android-01`。
- 若需要给浏览器或 Wraith 指定出口,优先使用 `http://10.66.0.1:18081`。
- 历史 Mac 实现文档仅保留为参考,不要按其继续推进生产路径。
