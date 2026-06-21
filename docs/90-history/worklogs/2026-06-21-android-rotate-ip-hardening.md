# 2026-06-21 Android rotate-ip 风险加固

## 背景

在 `2026-06-21-android-rotate-ip-control-timeout.md` 排查后,继续按隐藏副作用、兼容性、边界情况、性能、安全、命名、测试和维护成本做风险审查并修复。

## 改动

- Hub bootstrap 的 Android 运营商名探测改为带缓存:
  - 新增 `ZHHUB_ANDROID_CARRIER_CACHE_SECONDS`,默认 300 秒。
  - 同一 `management_addr` 的成功/空结果都会缓存,避免控制面离线或 GUI 高频 bootstrap 时持续派生 SSH。
  - `0` 可禁用动态探测,使用 token 配置里的显示名。
- Hub 控制面 SSH host key 策略加固:
  - 默认使用 `ZHHUB_ANDROID_CONTROL_KNOWN_HOSTS=/root/.ssh/zhandroid_control_known_hosts`。
  - 默认 `ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY=accept-new`,首次自动记录,后续 host key 变化会阻止连接。
  - 可临时设为 `no` 作为应急回滚。
- `rotate-ip` 参数边界统一:
  - Hub API 和 CLI direct 均限制 `down_seconds / --down-seconds` 为 1..60。
  - CLI 对 Hub `invalid_down_seconds` 返回更准确的错误信息。
- Android `/data/adb/zhandroid/rotate-ip.sh` 加固:
  - 本地校验断网秒数。
  - 后台执行时记录飞行模式 ON/OFF 请求、成功/失败和最终 `airplane_mode_on` 状态。
  - 避免 SSH 层只看到 `dispatched` 却完全不知道 Android 命令是否失败。
- Android watchdog 加固:
  - 默认 `DISABLE_WIFI=0`,不再隐藏地周期性关闭 Wi-Fi,避免破坏双网络 POC 的 `wlan0` 主隧道。
  - `tun0 / 10.66.0.101` 长时间缺失时,从单纯 `SET_TUNNEL_UP` 升级为 `SET_TUNNEL_DOWN` + `SET_TUNNEL_UP` bounce。
  - stuck 日志按间隔输出,避免新日志噪声。
- 文档同步:
  - 更新 Hub API 部署环境变量。
  - 更新 Android 控制与 diagnostics 排查说明。
  - 更新 server access 中 Android 当前控制面/watchdog 行为。

## 验证

- `go test ./hub/... ./clients/cli/...`:通过。
- 本机 `bash -n` 因 WSL 环境损坏无法运行。
- 通过 USB ADB 将 `watchdog.sh`、`rotate-ip.sh` 推到 `/data/local/tmp/*-check.sh`,执行 Android `sh -n`:通过。未覆盖生产路径,检查后已删除临时文件。

## 注意

这些改动目前是仓库侧修复。要让生产生效,需要重新部署 Hub `zhhub` 二进制,并把更新后的 `watchdog.sh`、`rotate-ip.sh` 推送到 Android 对应路径后重启 watchdog 或重启手机侧服务。
