# Hub 安全审查报告 2026-06-04

只读安全审查，全程未改动任何配置、服务或 token。

- 目标主机：`jp-proxy.ruichao.dev` / `36.50.84.68`，Ubuntu 24.04，内核 6.8。
- 方法：经授权 SSH 登录，逐项只读核查（监听端口、服务、SSH 策略、文件权限、防火墙、登录历史）。
- 相关改进项见 [TODO_SECURITY.md](../implementation/TODO_SECURITY.md)。

## 风险概览

| # | 问题 | 严重度 | 已验证事实 |
| --- | --- | --- | --- |
| 1 | dxhub 明文 HTTP 暴露公网 | 🔴 高 | 从中国直连 `http://36.50.84.68:18080/healthz` 成功返回 |
| 2 | bootstrap 经明文下发 WireGuard 私钥 | 🔴 高 | tokens.yaml 含 `private_key` 字段，配合 #1 = 私钥明文跨境传输 |
| 3 | 服务器完全无防火墙 | 🔴 高 | ufw 未安装，iptables INPUT 策略 ACCEPT 且无规则——所有端口全开 |
| 4 | SSH 允许 root + 密码登录 | 🔴 高 | `permitrootlogin yes`、`passwordauthentication yes`，正遭爆破（见 #5） |
| 5 | 正在被 SSH 暴力破解 | 🟠 中 | auth.log 失败 10419 次，单 IP `87.251.64.149` 即 5683 次；无 fail2ban |
| 6 | 226 个待装安全更新 | 🟠 中 | `apt-get -s upgrade` 统计 |
| 7 | dxhub 以 root 运行 | 🟠 中 | 进程归属 root（pid 22841），无降权用户 |
| 8 | dxhub 无审计日志 / 无限流 | 🟡 低 | 代码层面，bootstrap 无频率限制、无访问审计 |

## 做得对的地方（基线正常）

- `tokens.yaml` 权限 `-rw------- root:root`（0600），未被越权读取。
- root 只有 1 个 authorized_key，无多余登录账户、无普通用户。
- WireGuard `wg0` 正常监听 51820、`ip_forward=1`。
- 近期成功登录全部来自同一国内 IP 段 `223.160.x`（运维本人），无可疑成功登录。
- `unattended-upgrades` 在运行。

## 逐条说明与建议

### #1 + #2 明文 HTTP + 私钥下发（同一条致命链路）

与 [TODO_SECURITY.md](../implementation/TODO_SECURITY.md) 的 P0 完全吻合，本次为实测确认而非理论：
18080 明文 HTTP 对全网开放，bootstrap 响应携带 WireGuard 私钥，中日链路上的中间人
抓一次包即可拿到私钥冒充客户端。当前最高优先级。

- 修复：Hub 前置 TLS（反代或 `ListenAndServeTLS`）；私钥改为客户端本地生成、只上报公钥。
- 对应 TODO：P0-1（TLS）、P0-2（私钥本地生成）。

### #3 无防火墙

ufw 未安装，iptables INPUT 为默认 ACCEPT 无规则，任何端口（含 18080 及未来误启动的服务）
直接裸奔公网。

- 修复：安装 ufw，仅放行 `22/tcp`、`51820/udp`；18080 加 TLS 反代后仅经 443 暴露。

### #4 + #5 SSH root + 密码登录，正被爆破

root + 密码登录正遭工业级爆破（万次量级）。当前未见成功爆破迹象，但属定时炸弹。

- 修复：关闭 `PasswordAuthentication` 与 root 密码登录（已在用密钥，影响可控）；安装 fail2ban。

### #6 安全更新积压

226 个安全更新待装。

- 修复：尽快 `apt upgrade`。

### #7 dxhub 以 root 运行

一旦服务被攻破即获 root。

- 修复：建专用低权限用户运行 dxhub。

### #8 无审计日志 / 无限流

bootstrap 无频率限制、无访问审计，授权码即唯一凭证，无法防爆破、事后无可追溯记录。

- 修复：bootstrap 加访问日志（来源、token 命中、时间）与限流。

## 修复优先级建议

1. 防火墙（#3）—— 一步收敛整个暴露面，风险最低收益最高。
2. SSH 加固 + fail2ban（#4/#5）—— 止住正在进行的爆破。
3. TLS + 私钥本地生成（#1/#2）—— 堵住私钥明文跨境。
4. apt 升级（#6）、dxhub 降权（#7）、审计与限流（#8）。

> 本报告仅为只读审查结论。每项修复都会改动生产状态，需在执行前单独确认。
