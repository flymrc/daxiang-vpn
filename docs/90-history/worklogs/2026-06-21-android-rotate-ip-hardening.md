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

## 生产部署

- 2026-06-21 22:18 JST 已部署到生产。
- Hub:
  - 本机交叉编译 `dist/linux-amd64/zhhub`,SHA256 `239C50A39014D605D7836061DD10EE3630852EA76D6163B217B0AF9E5A1FFA16`。
  - 上传到 Hub `/tmp/zhhub-31e4a1e`。
  - 备份旧二进制为 `/opt/zongheng/zhhub/zhhub.bak-20260621-131811`。
  - 替换 `/opt/zongheng/zhhub/zhhub`,新增 systemd drop-in `/etc/systemd/system/zhhub.service.d/10-android-control-hardening.conf`。
  - `systemctl restart zhhub.service` 后服务 active,PID `535340`,`/healthz` 返回 `{"status":"ok"}`。
- Android:
  - 通过 USB ADB 推送 `watchdog.sh`、`rotate-ip.sh` 到 `/data/adb/zhandroid/` 并 `chmod 700`。
  - 重启 `watchdog.sh` 和 `zhandroid-control`;新进程为 `watchdog.sh` PID `6261`,`zhandroid-control` PID `6281`。
  - 生产脚本 SHA256:
    - `/data/adb/zhandroid/watchdog.sh`: `64fa3b1427a61affc7e9dca63a118b1eb80ee69df59000904d16927a49a92f42`
    - `/data/adb/zhandroid/rotate-ip.sh`: `160ec0f41094f3f1a967267d50d70bcfed171d8bb8ce355838d5bd4c8d488575`
- 部署后检查:
  - Hub `zhhub.service` active,PID `535340`。
  - Hub `/healthz` OK。
  - Hub 使用 `/root/.ssh/zhandroid_control_hub` 和 `/root/.ssh/zhandroid_control_known_hosts` 可执行 Android 控制面命令。
  - `/root/.ssh/zhandroid_control_known_hosts` 已生成。
  - Android 代理 v6 出口 `curl --proxy http://10.66.0.1:18081 https://api6.ipify.org` 返回 `240b:c010:472:5fa4:0:18:7232:cb01`。
  - 高频 bootstrap 仍存在,但 Android 控制面 SSH 日志未再按 bootstrap 频率增长;部署后的两次登录为人工健康检查。
