# 2026-10-07 第八波兼容部署与 Windows 客户端连通验收

用户明确要求“先部署吧，然后测试一下新版客户端能通就行”。此次使用第八波干净源码 `3b5a2c576aa344f907031b2c35c31c69499424b2` 的已验证产物，部署 Hub API、Hub reverse 和本机 Windows CLI；没有重新打包 GUI、替换手机/Mac、启用 v2 authority/proxy gate、导入 campaign、关闭兼容入口或执行 Jetstar。产物仍为未签名 `dev` 受控 canary，不能视为正式发行或 Steelman 终验。

## 部署与保护

只读复核及备份从 16:09 JST 开始。Hub 服务切换为 **16:14:16–16:14:21 JST**；Windows 新引擎从 16:18:30 JST 开始连接，最终客户端回执为 **16:20:48 JST**，数据库复核为 16:21:30 JST。

| 对象 | 部署前 SHA256 | 部署后 SHA256 |
| --- | --- | --- |
| `/opt/zongheng/zhhub/zhhub` | `c506ea7263dcd48117ea69682bf94097ac7134471b039a0262176edaf12bc1cb` | `66c2547881883ca53fcd9497a450173d4c999f226be2993a2a429dbd01947196` |
| `/opt/zongheng/zhreverse/zhreverse` | `16a2ab9e0dc3e82263b77c67da96a5437787fc3d7e84e748f8dd27f9a90160d3` | `edc832259698127d87f31d3eb50020bb0be1dbc91c9e734e3d2e0cb9fcd608d7` |
| `%LOCALAPPDATA%/ZonghengVPN/bin/zhvpn.exe` | `6eb0d0583428e866f777173ef9999c7a10a95b1065c2eb491666f9a42f5560c3` | `6892481f810aeeae276b024ccdea6ebb3f25102fa00654bf778d5a7acf30a7ba` |

Hub 私有备份：`/root/zongheng-backups/20261007-wave8-3b5a2c5`，目录 0700。包含旧二进制、tokens、reverse 配置、units/drop-ins 和 SQLite online backup；配置与数据库备份为 0600。新 Hub 先在三个独立 loopback 端口及数据库副本完成启动/健康/迁移预检；reverse 在独立 loopback listener 验证实际配置。上传及安装 hash 与构建 manifest 一致。生产二进制同目录 staging 后原子替换，先重启 `zhhub.service`，再重启 `zhreverse-hub.service`。

两服务重启会使控制面请求或代理连接短暂中断。没有改 units、Caddy、UFW、WG 配置、tokens 或 reverse YAML；28 个运行 peer 的公钥/allowed-IP 映射在服务切换后和客户 bootstrap 后均 hash 一致。`wg0`、RDP、管理员、手机控制资产及未知 peer 均保留。

本机备份：`%LOCALAPPDATA%/ZonghengVPN/backups/20261007-wave8-3b5a2c5`，访问仅为当前用户、SYSTEM 和 Administrators。按真实监听进程、可执行路径、home 与旧 PID 记录共同核验旧引擎后，用旧 CLI 停止并确认退出。只备份指定配置/密钥/运行文件和旧 binary，保留已有日志。

新版先拒绝了旧配置的继承权限。这是预期兼容边界；本次作为明确的升级迁移，将原文件归档，再以新 CLI `import` 写入私有配置。WG 密钥重新创建前设置保护 ACL，复制同一 key bytes，迁移前后 hash 一致。未对既有目录递归改 ACL、未拓宽父目录权限。新 CLI 从原路径启动，仍使用本地代理 7890；没有调用 system-proxy acquire/release 或改系统代理注册表。

## 验收

- [x] `zhhub.service` / `zhreverse-hub.service` active；Hub 18080、18079、Admin 18100 与公网 HTTPS `/healthz` 均 HTTP200/status=ok。
- [x] reverse 启动日志：`transport=tcp resolve=client tunnel=0.0.0.0:39093 proxy=10.66.0.1:18081`，96/48 限额、2m idle、10s idle-preempt 保留，`proxy_gate_enabled=false`。
- [x] 手机旧数据面自动重新连接，`session_count=2`。本次未登录手机取型号/二进制证据，不能重写历史 Pixel/Motorola 差异为已查明。
- [x] 新客户端 `version --json`：source_commit=3b5a2c5 完整 SHA、source_state=clean、Go1.26.7、version=dev、sidecar protocol2。
- [x] 新客户端认证 status：running=true、engine_state=ready、proxy_reachable=true、logging_state=healthy；引擎身份与配置 generation 均有公开回执。
- [x] 显式通过 `http://127.0.0.1:7890` 请求 `https://api64.ipify.org?format=json` 返回 HTTP200 和实际 IPv6；经同一代理请求 owned Hub HTTPS `/healthz` 返回 HTTP200/status=ok。最终两次分别约1033ms、306ms，仅为 smoke 时间，不作性能基准。
- [x] 客户 `10.66.0.40/32` 真实 WG handshake 对应新引擎启动时刻，transfer 非零；reverse 有实际 active proxy connection。结合目标响应证明此客户端已通，不以监听或 ready 代替出口证据。
- [x] SQLite quick_check=ok；旧 admin_users=1、egress_nodes=1、migration observation=9，行数保持；新增观察历史表后总表数10→15。inventory 仍0，没有登记正式 campaign。

最新 bootstrap 记录为 trusted_proxy、client_generated、private_key_returned=0，但由于此次 dev metadata，migration_class 仍 **unknown**。当前连通成功不能记为 compliant 或缩短30天窗口，安全收口保持 NO-GO。数据库增加的是观测模型；可变授权事实源仍为 tokens.yaml，不是启用 v2 authority。

## 恢复与证据边界

服务启动/配置失败的恢复脚本只替换该服务旧 binary 并重启，没有全接口回滚或自动覆盖当前数据库。现有备份可供受控恢复；新观察表为追加结构，实际数据库恢复/安全撤销合并和完整回滚演练仍未执行。执行恢复前先核对新观察事实；不能直接用旧数据库快照覆盖上线后事实。

Windows 回退需先用新版认证 stop 确认当前引擎退出，再按私有 backup manifest 恢复指定 binary/config/key 与原权限，使用旧 CLI 启动；不能按进程名全杀或在线覆盖引擎。此流程记录为恢复步骤，未实际回退。

私有证据位于 workspace 外部 `.local/zongheng-vpn/steelman/2026-10-07/deploy-wave8-1609/`：hub-before/preflight/deployment/verification/migration-observation 和 client-install/verification。完整出口 IP、用户路径及原始配置不进入本 worklog。构建依据是上一轮 Windows v13/Linux v3 门禁及十目标 manifest，本轮不把未签名 canary 重标正式 release。

中途原生 `fsutil hardlink list` 不支持该文件系统，首次备份在停机前拒绝；改用持有句柄的 GetFileInformationByHandle，确认单链接/no-reparse 后再迁移。一次组合客户端启动/网络检查命令被自动审批拒绝，未执行；改为分别执行明确范围的 CLI 启动及显式代理只读请求后获执行。一次尝试读取运行中的日志文件因共享模式失败，未改锁/关闭 writer；最终日志健康使用认证 log-status，不宣称已读取日志全文。新 namespace 当前约1469bytes，这只是该时刻容量。

当前 Windows 客户端保持新版引擎运行，未推送 GitHub。整体 G01–G05、实机多平台/GUI 安装包/签名、持续设备授权、手机 mTLS 和生产安全迁移仍未完成。
