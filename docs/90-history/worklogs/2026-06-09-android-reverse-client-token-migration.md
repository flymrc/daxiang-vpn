# 2026-06-09 工作记录：Android reverse 客户端入口迁移

## 背景

`DX-JP-TEST-100` 登录后本地代理 `127.0.0.1:7890` 能启动,但 `dxvpn.exe status` 获取出口 IP 失败。排查发现 Hub 授权 API 下发的 Android 出口仍是旧手机入站代理:

```text
egress.proxy_addr = 10.66.0.101:1080
```

线上实际生产数据面已切到 `dxreverse` 反向 QUIC,旧 `10.66.0.101:1080` 当前不可连接;Hub 侧可用入口应是 `dxreverse` proxy。

## 线上改动

在 Hub `root@36.50.84.68` 上做了配置备份后修改:

- `/etc/daxiang/dxreverse/server.yaml`
  - `server.proxy` 从 `127.0.0.1:18081` 改为 `10.66.0.1:18081`。
- `/opt/daxiang-vpn/dxhub/tokens.yaml`
  - 所有 `egress.name=jp-android-01` 且仍指向 `10.66.0.101:1080` 的 token 改为 `10.66.0.1:18081`。
  - 共更新 `11` 个 token: `DX-JP-TEST-001` 到 `DX-JP-TEST-010`,以及 `DX-JP-TEST-100`。
- 重启:
  - `dxreverse-hub.service`
  - `dxhub.service`

备份文件:

- `/etc/daxiang/dxreverse/server.yaml.bak.`
- `/opt/daxiang-vpn/dxhub/tokens.yaml.bak.`

> 备注:本次远程命令由 Windows PowerShell 发起,命令里的 `$(date +%H%M%S)` 被本地 PowerShell 误解析,所以备份后缀缺少时间部分。备份内容仍已生成。

## 验证结果

已确认:

- `dxreverse-hub.service` 为 `active`。
- `dxhub.service` 为 `active`。
- Hub 正在监听 `10.66.0.1:18081`。
- `DX-JP-TEST-100` bootstrap 已返回 `egress.proxy_addr=10.66.0.1:18081`。
- 生产 token 中 `11` 个 Android token 均已指向 `10.66.0.1:18081`。
- Android 控制面 `10.66.0.101:2022` 返回 SSH banner。
- Android WireGuard peer 握手新鲜。

当前卡点:

- 重启 Hub 端 `dxreverse` 后,Android 端 `dxreverse client` 没有自动重连。
- Hub 上 `10.66.0.1:18081` 当前返回 `503 reverse client is not connected`。
- Hub 上现有 `/root/.ssh/dxandroid_control` 不能登录手机,手机拒绝该公钥。
- 当前 Windows 机器没有可用的 Android 控制面私钥,无法远程执行 `pkill dxreverse` 或重拉 `/data/adb/service.d/99-dxreverse-egress.sh`。

## 恢复动作

需要用当前被手机授权的控制面私钥,或 ADB/物理接触,在 Android 上重拉数据面:

```sh
pkill -f '/data/adb/dxreverse/bin/dxreverse' || true
setsid sh /data/adb/service.d/99-dxreverse-egress.sh >/dev/null 2>&1 </dev/null &
```

恢复后在 Hub 上验证:

```bash
journalctl -u dxreverse-hub.service -n 50 --no-pager
curl -x http://10.66.0.1:18081 https://api.ipify.org
```

客户端侧需要重新 `dxvpn.exe stop` 后 `dxvpn.exe start`,确保运行时配置使用新的 `egress.proxy_addr`。

## 同日追加：Hub UFW 放行 WireGuard 客户端访问 proxy

Android reverse client 随后自动重连,Hub 本机访问 `10.66.0.1:18081` 已可出公网,但 Windows 客户端仍超时。抓包确认客户端 `10.66.0.30` 的 SYN 已到 Hub `wg0`,目标 `10.66.0.1:18081`,但 Hub 没回 SYN-ACK。

原因:

- Hub `INPUT` 默认策略为 `DROP`。
- `dxreverse` 已监听 `10.66.0.1:18081`,但 UFW 未允许 WireGuard 客户端访问该本机端口。

处理:

```bash
ufw allow in on wg0 to 10.66.0.1 port 18081 proto tcp comment "dxreverse wg clients"
```

曾临时插入一条同等 `iptables -I INPUT` 规则用于快速恢复;确认 UFW 持久规则生效后已删除临时规则。

最终验证:

- `ufw status verbose` 显示 `10.66.0.1 18081/tcp on wg0 ALLOW IN Anywhere`。
- `dxvpn.exe status` 返回出口 IP `133.106.33.238`。
- `curl.exe -x http://127.0.0.1:7890 https://api.ipify.org` 返回 `133.106.33.238`。

## 同日追加：CLI rotate-ip 前置检查

用户执行:

```powershell
dxvpn.exe rotate-ip
```

失败:

```text
Warning: Identity file C:\Users\marui\.ssh\dxandroid_control not accessible
ssh: connect to host 10.66.0.101 port 2022: Connection timed out
```

原因:

- 普通 `dxvpn.exe start` 是用户态 WireGuard,不会给 Windows 系统添加 `10.66.0.0/24` 路由。
- `rotate-ip` 走 Android 控制面 SSH,需要系统能到 `10.66.0.101:2022`,或通过 Hub `--jump`。
- 本机没有当前默认的 Android 控制面私钥 `~/.ssh/dxandroid_control`。

修正:

- `dxvpn.exe rotate-ip` 默认改为调用 Hub API `/api/client/rotate-ip`,客户机不再需要 Android SSH 私钥。
- Hub API 校验客户 token 后,只允许 `jp-android-01` 出口触发 Android 控制面 `/data/adb/dxandroid/rotate-ip.sh`。
- Hub 已部署新版 `dxhub` 并设置 systemd drop-in:
  - `DXHUB_ANDROID_CONTROL_KEY=/root/.ssh/dxandroid_control_hub`
- Hub 上生成了无 passphrase 服务专用 key:
  - private: `/root/.ssh/dxandroid_control_hub`
  - public: `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMFnqYTgTqmQkJ314sFHCuaHd5q4NvrjsWZwNsR8E5H7 dxhub-android-control@36.50.84.68`
- CLI `rotate-ip` 新增 `--jump <SSH跳板>`。
- CLI 新增 `--direct` 管理员排障模式;仅 `--direct` 或显式传 `--key/--jump/--phone/--port` 时走旧 SSH 直连路径。
- 默认 key 查找兼容 `~/.ssh/dxandroid_control` 和 `~/.ssh/dxandroid_control_local`。
- SSH 前先检查私钥文件是否存在。
- 未指定 `--jump` 时先检查 `10.66.0.101:2022` 是否可达,不可达则给出“用户态代理模式没有系统路由”的明确提示。

一次性基础设施动作:

- 已把 Hub public key 追加到 Android `/data/adb/dxandroid/.ssh/authorized_keys`。
- 追加前,Hub API 会返回 `control_failed`,日志中为 `Permission denied (publickey)`。

尝试过的追加路径:

- Windows 本机无 ADB,也没有 `~/.ssh/dxandroid_control_local` 备份。
- Hub 到 Android `10.66.0.101:2022` 可达,但现有 Hub key 均未被 Android 授权。
- Android ADB TCP `10.66.0.101:5555` 不通。
- Tailscale 可达 Mac mini `100.80.36.89:22`,但 Windows 当前 SSH key 无法登录 `maruichao@mac-mini`。
- Tailscale SSH wrapper 也未能进入 Mac,仍卡在 host key/SSH 授权阶段。

结论:

- 记载中的 Mac mini 路径应可用,因为 Mac 上曾生成并授权 `~/.ssh/dxandroid_control_local`。
- 用户提供 Mac mini 登录凭据后,已通过 Tailscale `100.80.36.89` 登录 Mac,并使用 Mac 上的 `~/.ssh/dxandroid_control_local` 登录 Android 追加 Hub public key。
- Hub 验证命令 `ssh -i /root/.ssh/dxandroid_control_hub -p 2022 root@10.66.0.101 id` 成功。
- 客户端端到端验证 `dxvpn.exe rotate-ip --down-seconds 1 --wait-seconds 12` 已成功触发 Android 重注册;等待恢复后 `dxvpn.exe status` 显示出口 IP 从 `133.106.33.238` 变为 `133.106.36.21`。

## 同日追加：rotate-ip 默认等待策略

用户使用默认参数执行:

```powershell
dxvpn.exe rotate-ip
```

观察到:

- Hub 已成功触发 Android rotate-ip。
- CLI 默认等待 30s 后单次探测,当时出口仍未恢复,打印 `换 IP 后出口：(暂不可达)`。
- 随后再次执行 `dxvpn.exe status`,出口已恢复并变为 `210.157.193.217`。

原因:

- Android `rotate-ip.sh` 使用 `setsid ... &` 异步派发飞行模式切换,SSH/API 会很快返回。
- 蜂窝重注册 + WireGuard 重握手 + dxreverse 反连恢复经常超过 30s。
- 原 CLI 只固定 sleep 后查一次,容易造成“已触发成功但显示暂不可达”的假失败体验。

修正:

- `rotate-ip` 默认 `--wait-seconds` 从 30 调整为 75。
- `--wait-seconds` 仍表示最大等待时间。
- CLI 先等待 `downSeconds + 10s` 避免误读旧 IP,之后每 5s 轮询出口公网 IP,恢复即返回。
- 文档示例将较长断网场景更新为 `--wait-seconds 90`。
