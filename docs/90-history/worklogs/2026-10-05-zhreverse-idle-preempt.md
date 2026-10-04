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

- 本轮基于 GitHub `main=92379a3` 的单提交分支 `e5be358` 验证，未混入主工作区未推送的 `fa4190a`、`e69645a` 或未提交改动。
- `go test ./egress/reverse` 首次在 Windows 的既有 `TestOpenCommandDropsStaleSession` 超短超时路径失败；随后 `go test -count=3 ./egress/reverse` 连续三次通过。本轮未修改该测试，不能把首次失败隐藏为全程通过。
- `go test -count=10 -run '^(TestAcquireProxySlot|TestPreemptedPipe|TestLoadReverseConfigExamples|TestPipeBoth)' ./egress/reverse` 通过，覆盖新增抢占、拨号/活跃会话保护、客户端隔离、总限额、重复释放及两端关闭。
- `go test ./hub/... ./clients/cli/... ./shared/config/...` 通过。
- 本轮未运行完整 race 检查；此前重基阶段报告的调度计时敏感失败不能等同于 race 检查通过。生产二进制没有启用 race。
- 构建：Go `1.26.5`，`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o dist/reverse/zhreverse-linux-amd64 ./egress/reverse`。显式关闭 VCS 自动标记，避免父仓库工作区信息被 Go 写入嵌套 worktree 的二进制；源码提交由 git 独立校验。此次重建与先前 `7783367f…0528e8` 构建的工具链/参数不同，以上传前后实测 SHA256 为准。

生产：

- 2026-10-05 01:31 JST 经本机既有 Mac 管理公钥登录局域网 `192.168.68.123`，以 `HostKeyAlias=100.80.36.89` 验证既有主机键，再由 Mac 已有默认密钥登录 Hub，幂等追加 Windows 公钥。没有复制 Mac 私钥或输入密码。Windows 后续以本机 `id_ed25519` 直接登录成功。
- 部署前：Hub `x86_64`，`zhreverse-hub.service` 的 ExecStart 为 `/opt/zongheng/zhreverse/zhreverse server --config /etc/zongheng/zhreverse/server.yaml`；原启动于 2026-07-08 UTC，Go build info 的源码为 `ee6edc45a57c`。原服务端参数与仓库匹配，差异主要是后续手机端接口绑定功能；本次不部署手机。
- 01:35:27 JST 替换 Hub 二进制并重启，约 3 秒后两条 reverse 会话恢复。启动日志：`transport=tcp resolve=client tunnel=0.0.0.0:39093 proxy=10.66.0.1:18081 max_proxy_connections=96 max_proxy_connections_per_client=48 proxy_idle_timeout=2m0s proxy_preempt_idle=10s`。配置文件、unit、WireGuard peer 与 zhhub 均未更改。
- 新 SHA256：`16a2ab9e0dc3e82263b77c67da96a5437787fc3d7e84e748f8dd27f9a90160d3`。备份：`/opt/zongheng/zhreverse/zhreverse.bak.20261005-idle-preempt`；原 SHA256：`91dbae431dece50d8e034b369cd936a388e1a19a8b716097832773fe23b24ef9`。部署脚本会在服务/参数/会话/出口验证失败时恢复备份并重启，本轮全部通过，未回滚。
- `/debug/session-health`：2 条会话，`proxy_preempt_idle_ms=10000`，并发上限仍为 96/48，idle timeout 120000ms。
- Hub 经手机出口的 IPv6 为 `240b:c010:640:391a:0:42:8b1b:4001`，IPv4 为 `210.157.193.193`；本机 `127.0.0.1:7890` 经现有 zhvpn 的 IPv6 请求也返回同一手机地址。
- 用户随后明确取消 Jetstar 重跑，本轮没有完成该网站搜索/booking 业务验收，也未人为顶满线上并发来增加 `proxy_idle_preemptions`。部署后的空闲抢占计数和真实流量应继续观察。
- 回滚：复制上述备份到临时文件，再原子替换当前二进制并 `systemctl restart zhreverse-hub.service`；回滚也会短暂断开代理连接，不需要改配置或手机端。
