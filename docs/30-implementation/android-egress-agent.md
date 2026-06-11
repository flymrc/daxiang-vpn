# Android 出口节点实现

## 当前生产实现

Android 出口生产数据面只使用 [egress/reverse](../../egress/reverse/README.md)。

```text
中国客户端
  -> WireGuard
  -> Hub/WG 10.66.0.1:18081 HTTP CONNECT proxy
  -> zhreverse server
  -> TCP/yamux reverse tunnel :39093
  -> Android zhreverse client
  -> 手机卡公网出口
```

WireGuard App 仍保留为控制面,用于:

- Android 内网管理地址 `10.66.0.101`。
- `zhandroid-control` SSH 控制面 `10.66.0.101:2022`。
- watchdog 远程自愈和换 IP。

旧 `zhandroid-egress` / `10.66.0.101:1080` Android 数据面已从生产入口拆除,不要再新部署。

## 关键文件

- Android/Hub 反向数据面: [egress/reverse](../../egress/reverse/README.md)
- Android 控制面与 watchdog: [egress/android-control](../../egress/android-control/README.md)
- Android 状态 App: [egress/android-status](../../egress/android-status/README.md)
- Hub 配置示例: [hub-reverse-server.yaml.example](../20-operations/configs/egress/hub-reverse-server.yaml.example)
- Android 配置示例: [android-reverse-client.yaml.example](../20-operations/configs/egress/android-reverse-client.yaml.example)

## Android 侧底层策略

生产脚本 [99-zhreverse-egress.sh](../../egress/reverse/service.d/99-zhreverse-egress.sh) 在启动 `zhreverse client` 前会:

- 禁用 Wi-Fi,避免手机卡出口误走现场 Wi-Fi。
- 保持插电唤醒。
- 关闭 Doze idle 限制。
- 尝试调高 UDP/socket buffer。
- 禁用并停止旧 `zhandroid-egress` service 和进程。

watchdog [watchdog.sh](../../egress/android-control/watchdog.sh) 会周期性重放这些底层基线,并确保 WireGuard 控制面、`zhandroid-control` 和 `zhreverse` supervisor 都在运行。

Hub 侧 `zhreverse server` 对每次 `CONNECT` / `FETCH` 反向命令设置超时。若某条 reverse session 半死、能被选中但不再回应,Hub 会把它从 session 池剔除并重试其它 session,避免 `10.66.0.1:18081` 的 HTTP proxy 请求长期卡住。生产当前使用 TCP/yamux 双连接,用于降低单条 TCP/yamux 在客户端并发下的队头阻塞;Hub 侧同时启用 CONNECT 并发护栏,当前 `max_proxy_connections=96`、`max_proxy_connections_per_client=48`,超限请求快速返回 HTTP 429,避免继续堆积到手机出口。QUIC/UDP 在 Rakuten 手机卡上容易出现空闲超时和半死 session,仅保留为回滚/实验路径。

## 部署验证

Hub 侧:

```bash
systemctl status zhreverse-hub.service
journalctl -u zhreverse-hub.service -n 50 --no-pager
scripts/check-android-reverse-egress.sh
curl --proxy http://10.66.0.1:18081 https://api.ipify.org
```

Android 侧:

```sh
ps -A -o PID,PPID,ARGS | grep zhreverse
tail -80 /data/local/tmp/zhreverse-egress.log
tail -80 /data/local/tmp/zhandroid-control.log
ip route get 1.1.1.1
```

正常生产状态:

- `zhreverse client` 有 1 条 TCP reverse session 连到 Hub。
- Android 默认公网路由应走 `rmnet_data*`,不是 `wlan0`。
- 不应看到 `zhandroid-egress` 进程。
- 客户端 token 的 `egress.proxy_addr` 应为 `10.66.0.1:18081`。
