# 2026-10-05 zhreverse 满额时抢占空闲隧道

背景：

- 经 `zhvpn.exe`（127.0.0.1:7890，Rakuten 手机出口）跑 Jetstar 预订流程，首页能打开，点 Search 跳 `booking.jetstar.com` 时约 0.5 秒 `ERR_CONNECTION_RESET`；换 IP（rotate-ip）无效，同机直连正常。
- Chrome NetLog（产品内核与系统 Chrome 各一份）显示：本地 sing-box 收到 CONNECT 后 1ms 内回 `200 Connection established`，Chrome 发出 ClientHello 后 27–31ms 收到 RST（`os_error=10054`）。本机到 Hub RTT 约 15ms，两个 RTT 说明是 Hub 本机拒绝，未到手机和目标站。
- 三次运行共约 290 次重置，全部发生在 Chrome 同时开着 48–56 条代理隧道时，没有一次低于 48；空载时同样的主机（含 `booking.jetstar.com`、`akstat.io`）经 7890 都能正常握手。
- 结论：Jetstar 首页加载几十个广告/统计域名，每个域名一条 CONNECT，浏览器常驻约 50 条空闲 keep-alive 隧道，顶满 `max_proxy_connections_per_client=48`；Hub 对新 CONNECT 回 429，sing-box 已乐观回过 200，只能重置浏览器连接。系统 Chrome 还经代理跑 Google 后台服务（如 `optimizationguide-pa.googleapis.com`），更容易顶满。与 2026-06-11 “单客户端卡在 48”同源；当时的 2 分钟 idle 回收不足以覆盖页面加载后几十秒内的情况。

改动（`egress/reverse`，Hub server）：

- 新增 `proxy_preempt_idle` 配置 / `--proxy-preempt-idle` 参数，默认 `10s`，`0` 关闭。
- 达到 `max_proxy_connections_per_client` 时，先关闭该客户端最久无流量且空闲至少 `proxy_preempt_idle` 的转发中隧道，把名额让给新 CONNECT；达到 `max_proxy_connections` 时在所有客户端中选。仍找不到才回 429。
- 仍在拨号目标、尚未开始转发的会话不参与抢占；被抢占会话稍后的 release 不会重复释放名额。
- `/debug/session-health` 新增 `proxy_preempt_idle_ms` 与 `proxy_idle_preemptions`。
- 示例配置、`egress/reverse/README.md`、诊断手册同步更新。

验证：

- `go test -race ./egress/reverse`：通过（新增同客户端抢占、保留活跃/拨号中会话、不跨客户端、总上限跨客户端、`0` 关闭、真实 `pipeBothMeasured` 被抢占后两端关闭等用例）。
- `go test ./hub/... ./egress/reverse ./clients/cli/... ./shared/config/...`：通过。

生产：

- 尚未部署。部署后需在启动日志确认 `proxy_preempt_idle=10s`，再经 zhvpn 复跑 Jetstar 流程，并观察 `proxy_idle_preemptions` 与 `proxy_metrics` 中 `rejected` 的变化；部署完成后再更新 `server-access.md` 的当前生产参数。
