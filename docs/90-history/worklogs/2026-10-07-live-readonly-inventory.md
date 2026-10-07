# 2026-10-07 当前运行二进制只读复核

Steelman开发期间用本机默认SSH密钥、BatchMode/StrictHostKeyChecking对Hub只读复核。本轮没有输入密码、读取私钥、改service/peer/config、重启、复制或部署二进制。观察时间为2026-10-07 02:39:32 UTC（11:39:32 JST）；会话/计数为该瞬间数据。

| 对象 | 当前只读事实 |
| --- | --- |
| Hub CPU | x86_64 |
| zhhub.service | active/running；MainPID790911；启动2026-08-30 16:14:12 UTC；运行路径 `/opt/zongheng/zhhub/zhhub` |
| 当前Hub二进制SHA-256 | `c506ea7263dcd48117ea69682bf94097ac7134471b039a0262176edaf12bc1cb` |
| zhreverse-hub.service | active/running；MainPID1312383；启动2026-10-04 16:35:27 UTC；运行路径 `/opt/zongheng/zhreverse/zhreverse` |
| 当前reverse二进制SHA-256 | `16a2ab9e0dc3e82263b77c67da96a5437787fc3d7e84e748f8dd27f9a90160d3` |
| 本机客户CLI | PID59396；`AppData/Local/ZonghengVPN/bin/zhvpn.exe`；SHA-256 `6eb0d0583428e866f777173ef9999c7a10a95b1065c2eb491666f9a42f5560c3` |
| 已安装CLI buildinfo | Go1.26.5/windows amd64；path `zongheng-vpn/clients/cli`；vcs.revision `6cd1ba0868bdc816bc27b3029f9293129c1d6435`、vcs.modified=true，因此该revision不足以唯一还原其源码 |

Hub本机 `/healthz` 与 Admin `/admin/api/health` 均返回status=ok。WireGuard地址上的reverse `/debug/session-health`返回session_count=2、proxy_preempt_idle_ms=10000、proxy_idle_preemptions=9211、active_proxy_connections=0、peak=48。仅提取这些公开健康字段，不输出session认证材料、argv秘密或WG配置。

这些结果只证明当前进程/HTTP健康响应及该时刻reverse会话，不证明业务流量、真实客户撤销、TLS身份或Steelman部署。未执行Jetstar，也未更改本机代理。第三波clean开发产物来源为947c5f5/Go1.26.7，不能把它们的门禁当作上述已运行旧二进制的证明。

本机密钥可登录是本次实时事实；此前Permission denied为历史状态。Mac/手机当前二进制、当前授权映射与受保护peer完整清册未由本轮复核，因此P0.2/P0.3完整checkbox继续未勾选。
