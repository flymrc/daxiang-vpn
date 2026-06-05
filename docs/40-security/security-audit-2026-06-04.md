# Hub 安全审查报告 2026-06-04

只读安全审查，全程未改动任何配置、服务或 token。

- 目标主机：`jp-proxy.ruichao.dev` / `36.50.84.68`，Ubuntu 24.04，内核 6.8。
- 方法：经授权 SSH 登录，逐项只读核查（监听端口、服务、SSH 策略、文件权限、防火墙、登录历史）。
- 相关改进项见 [安全 TODO](security-todo.md)。

## 风险概览

| # | 问题 | 严重度 | 已验证事实 |
| --- | --- | --- | --- |
| 1 | dxhub 明文 HTTP 暴露公网 | 🔴 高 | 从中国直连 `http://36.50.84.68:18080/healthz` 成功返回 |
| 2 | bootstrap 经明文下发 WireGuard 私钥 | 🔴 高 | tokens.yaml 含 `private_key` 字段，配合 #1 = 私钥明文跨境传输 |
| 3 | 服务器完全无防火墙 | ✅ 已修复 | 原：ufw 未装、iptables INPUT 默认 ACCEPT 全开。2026-06-04 装并启用 ufw，见下「修复记录」 |
| 4 | SSH 允许 root + 密码登录 | 🟠 部分缓解 | `permitrootlogin yes`、`passwordauthentication yes` 仍未改；爆破已由 fail2ban 压制。关密码待定（取决于其他机器密钥下发） |
| 5 | 正在被 SSH 暴力破解 | ✅ 已修复 | 原：auth.log 失败 10419 次、无 fail2ban。2026-06-04 装 fail2ban，启动即封爆破 IP，见下「修复记录」 |
| 6 | 226 个待装安全更新 | ✅ 已修复 | 原 226（刷新后 232）个。2026-06-04 全部安装，剩余安全包 0，见下「修复记录」。建议后续低峰重启收尾 |
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

与 [安全 TODO](security-todo.md) 的 P0 完全吻合，本次为实测确认而非理论：
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

> 本报告初始为只读审查结论。每项修复都会改动生产状态，需在执行前单独确认。

## 修复记录

### 2026-06-04 #3 防火墙（已完成）

在 Hub 安装并启用 ufw，按保命流程操作，未影响 SSH、未锁死自己。

- 默认策略：`deny incoming` / `allow outgoing`（原 iptables 全开暴露面已收敛）。
- 放行规则：
  - `22/tcp`（SSH）
  - `51820/udp`（WireGuard 隧道）
  - `18080/tcp`（dxhub bootstrap，临时保留，待 #1 TLS 上线后收）
- 保命手法：先加规则不启用 → `systemd-run --on-active=15min` 挂自动 `ufw disable` 兜底 →
  再 `ufw enable` → 从中国验证新建 SSH + 18080 均通 → 确认后取消回滚定时器。
- 开机自启：`systemctl is-enabled ufw` = enabled。
- 验证：启用后从中国新建 SSH 成功、`http://36.50.84.68:18080/healthz` 仍返回 ok。

> 注：18080 仍以明文放行，#1/#2 未解决。完成 TLS 后应把 ufw 的 18080 规则删除，仅经 443 暴露。

### 2026-06-04 #5 fail2ban 防爆破（已完成）

装 fail2ban 拦截 SSH 爆破，零锁死风险（只封陌生爆破 IP，不碰密钥登录与白名单）。

- 配置 `/etc/fail2ban/jail.local`：仅启用 sshd jail；`findtime=10m maxretry=5 bantime=1h`；
  `banaction=ufw`（与现有防火墙一致）；`ignoreip` 含本机回环与运维来源段 `223.160.0.0/16`。
- 服务：`systemctl is-active fail2ban`=active，开机自启 enabled。
- 验证：启动 3 秒内即封禁爆破 IP（`92.118.39.236`）；从中国新建 SSH 仍成功，未误伤。

> 注：#4 关闭密码登录尚未做。需在关闭前确保所有要登录的机器公钥已加入 `authorized_keys`，
> 否则只有密码的机器将无法登录。关闭操作有锁死风险，须用回滚兜底手法（同 #3）。

### 2026-06-04 #6 安全更新（已完成）

安装全部安全更新，剩余安全包 0。无内核更新，故未强制重启。

- 前置保命修复：原 `ssh` 服务开机自启为 **disabled**（重启后可能无法 SSH 登录），
  已 `systemctl enable ssh`，现为 enabled（`ssh.socket` 亦 enabled）。这是任何重启的前提。
- 升级方式：`apt-get -y -o Dpkg::Options::=--force-confold upgrade`（保留现有配置、非交互），
  挂 15 分钟 `ssh-rescue` 兜底（防 openssh 升级后 sshd 起不来），完成验证后取消。
- 含底层包：libc6、systemd、openssh-server、docker-ce 等；openssh 升级后 sshd 平稳重启未掉线。
- 验证：从中国新建 SSH OK、18080 healthz OK、wg-quick/dxhub/fail2ban 全 active、wg 3 个握手对端。
- 遗留：系统标记 `libc6`、`apparmor` 建议重启以让底层库完全生效。**无内核更新，非必须**。

### 2026-06-04 重启收尾（已完成）

重启前先逐项实证确认两个保命点，再重启并验证恢复。

- 重启前确认（保命点 1 SSH）：ssh.service/ssh.socket 均 enabled、`sshd -t` 通过、
  authorized_keys 1 公钥权限 600、ufw 放行 22、密码登录仍开（双保险）、fail2ban 未误封我方段。
- 重启前确认（保命点 2 客户机）：wg-quick/dxhub/ufw/fail2ban/docker 均 enabled、
  ip_forward 已 sysctl 持久化、wg0.conf 含 3 peer、端到端出口 IP 基线 118.158.252.9。
- 重启后结果：SSH 5 秒内恢复（boot id 已变）；6 服务全部 active；ufw 三规则恢复、Status active；
  ip_forward=1；两活跃 peer 重新握手；端到端出口 IP = 118.158.252.9，与基线一致；中国侧 18080 可达。
- 客户端无需任何操作，断流数十秒后自动重连。
- 备注：第三个 peer（约 17h 未握手）为闲置旧 peer，与重启无关，印证 DIAGNOSTICS 所述 peer 表过时。

