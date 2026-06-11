# 2026-06-11 zhreverse 空闲连接回收

背景：

- Windows GUI 测 FAST 时,页面一度显示 0 并提示无法到达测速服务器。
- 排查发现 Hub 侧 `10.66.0.1:18081` 对单客户端残留接近 `max_proxy_connections_per_client=48` 的 ESTAB 连接；重启本机 sidecar 后基础访问恢复。
- FAST/浏览器异常中断时,旧 `zhreverse` Hub 侧 CONNECT 隧道没有明确 idle 回收,可能长期占用单客户端并发槽。

改动：

- `egress/reverse` Hub server 增加 `proxy_idle_timeout` 配置和 `--proxy-idle-timeout` 参数。
- 默认值为 `2m`;配置文件未写该字段时自动补默认值。
- Hub 侧 HTTP CONNECT 双向转发改为：
  - 任一方向结束时主动关闭两端连接。
  - 若超过 `proxy_idle_timeout` 无流量,主动关闭两端连接并释放并发槽。
- Android client 侧复用转发函数但传 `0`,保持原有无 idle 回收行为,避免手机侧数据面协议行为变化。
- 示例配置 [hub-reverse-server.yaml.example](../../20-operations/configs/egress/hub-reverse-server.yaml.example) 增加 `proxy_idle_timeout: 2m`。
- 运维入口 [server-access.md](../../20-operations/runbooks/server-access.md) 记录当前生产参数。

生产：

- Hub 当前二进制备份到 `/opt/zongheng/zhreverse/zhreverse.bak.20260611-idle-reaper`。
- 新 Hub 二进制部署到 `/opt/zongheng/zhreverse/zhreverse`。
- 重启 `zhreverse-hub.service` 后 active。
- 启动日志确认：`proxy_idle_timeout=2m0s`。

验证：

- `go test ./egress/reverse`：通过。
- `go test ./hub/... ./egress/reverse ./clients/cli/... ./shared/config/...`：通过。
- 本机经 `127.0.0.1:7890` 访问 `https://api64.ipify.org`：返回日本手机出口 IPv6 `240b:c010:421:d18c:0:42:e654:1701`。
- 经 FAST/Netflix OCA range 下载 2 MiB 成功：约 `2.92s`,下载速率约 `717022 B/s`。
- 生产观察一个 idle 周期后,Hub 侧 `18081` ESTAB 连接数从 22 降到 13,说明空闲连接已被回收,未再卡在 48。
