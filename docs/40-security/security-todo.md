# 安全与抗封改进 TODO

本文件记录已确认要做、但尚未落地的安全与抗封改进项。按优先级排列。
背景讨论：客户端是否可被裸代理（直接下发代理 IP）替代——结论是不可，
隧道方案在抗 GFW 识别、保护出口 IP、穿透住宅 NAT 上不可替代，详见各项「原因」。

P0 各项已由 [Hub 安全审查报告 2026-06-04](../40-security/security-audit-2026-06-04.md) 实测确认。

## P0：私钥与凭证安全（必须先做）

这两项解决当前实现里真实存在的安全洞，与「要不要客户端」无关，应一起做。

### 1. Hub 启用 TLS

- 现状：Hub 用明文 HTTP 监听（`http.ListenAndServe`，[main.go:26](../../hub/main.go#L26)），
  bootstrap 响应里直接下发 WireGuard 私钥（[server.go:55-61](../../hub/internal/auth/server.go#L55-L61)）。
- 2026-07-01 状态澄清：Hub 管理控制台已经通过 Caddy 提供
  `https://jp-proxy.ruichao.dev/admin/`，但这只覆盖 admin 管理面,不能等同于客户端
  bootstrap 已安全。
- 2026-07-01 迁移进展：生产 Caddy 已提供 `https://jp-proxy.ruichao.dev/api/client/*`
  和 `/healthz` 到 `127.0.0.1:18080` 的 HTTPS 反代；客户端代码已改为默认使用
  `https://jp-proxy.ruichao.dev`。客户端发布和公网 `18080/tcp` 收口仍需按 runbook
  分步执行后才算关闭 P0-1。
- 风险：私钥与 token 经明文 HTTP 在中国↔日本这条最敌对的链路上传输，可被中间人窃听/篡改。
- 方案（二选一）：
  - 在 Hub 前置反向代理（Caddy/Nginx）终止 TLS，对外只暴露 HTTPS。
  - 或在 Go 服务内直接 `ListenAndServeTLS`。
- 验收：客户端只通过 HTTPS 访问 `/api/client/bootstrap`；明文 HTTP 不再对外。

### 2. 私钥客户端本地生成，只上报公钥

- 现状：私钥存在 Hub（[store.go:45-48](../../hub/internal/auth/store.go#L45-L48)），
  经 HTTP 下发给客户端。私钥同时存在于 Hub + 链路 + 客户端三处。
- 2026-07-01 迁移进展：CLI 已实现本地生成/复用 `ZHVPN_HOME/wireguard/client.key`,
  bootstrap 请求上报 `wireguard_public_key`;生产 Hub 已部署新 `zhhub`,可校验公钥、
  `wg set` 应用 peer,并在新协议响应中省略 `wireguard.private_key`。仍需分发新客户端,
  再清理 `tokens.yaml` 中 legacy `private_key` 后才算关闭。
- 目标：客户端首次启动时本地生成 WireGuard 密钥对，私钥永不离开设备，
  login/bootstrap 时只把【公钥】上报给 Hub，Hub 用公钥配置 peer，从不接触私钥。
- 好处：
  - 消除明文链路上的私钥泄露；公钥泄露无所谓（本就公开）。
  - 攻击面从三处收缩到客户端一处；Hub 被入侵也拿不到任何客户端私钥。
  - Hub 不再是「私钥金库」，消除单点全灭风险。
  - 符合架构文档既定规则「私钥尽量只保存在所属设备上」（[系统架构](../10-architecture/system-architecture.md#L391-L392)）。
- 依赖：必须配合 P0-1 的 TLS 一起做——TLS 不是为了保护公钥（公钥不怕看），
  而是防止上报的公钥被中间人篡改替换，并保护 token 本身不被窃听。
- 验收：Hub 的 tokens 配置里不再出现任何客户端私钥；客户端本地持有私钥。

## P1：抗封升级（被针对性封 WireGuard 时启用）

- 现状：客户端走用户态 WireGuard 隧道（[singbox.go:92-111](../../shared/proxy/singbox.go#L92-L111)）。
  加密优于裸代理，但 WireGuard 握手有可识别指纹、且是 UDP，可能被 GFW 识别/限速/阻断。
- 目标：在 sing-box 引擎中启用伪装成正常 TLS 的协议（Shadowsocks+插件 / Trojan / VLESS+Reality），
  让流量「藏进人群」，而不是自研私有混淆。
- 为什么不自研混淆：私有指纹全球唯一、无人测试、封它零误伤，等于单挑国家级系统持续升级，几乎稳输；
  正确做法是复用社区方案。sing-box 已内置这些协议，只是引擎里特意没注册以缩小体积
  （[engine.go:78-84](../../shared/proxy/engine.go#L78-L84)），升级 ≈ 注册多几行 + 改 Hub 配置。
- 代价（需提前评估）：
  - Trojan 需域名 + 真实 TLS 证书，最好背一个正常网站作门面（门面放 Hub，住宅出口做不了）。
  - VLESS+Reality 免域名但需挑稳定的「借壳」目标站，实现/版本敏感。
  - 套 TLS 比裸 WireGuard 多一层加解密与握手 RTT，在本就高延迟的中日链路上可感知。
  - 运维复杂度上升：证书续期、借壳维护、参数对齐。
- 触发条件：当观测到 WireGuard 隧道被针对性阻断/限速时优先做此项。

## 运维加固（来自 [安全审查 2026-06-04](../40-security/security-audit-2026-06-04.md)）

服务器层面的加固项，与代码改动独立。详细背景见审查报告。

- [x] **防火墙**（审查 #3）—— 2026-06-04 完成。装并启用 ufw，默认 deny incoming，
  仅放行 `22/tcp`、`51820/udp`、`18080/tcp`（18080 待 TLS 后收）。修复记录见审查报告。
- [x] **fail2ban 防爆破**（审查 #5）—— 2026-06-04 完成。仅 sshd jail，banaction=ufw，
      运维来源段加白；启动即封爆破 IP。修复记录见审查报告。
- [ ] **关闭 SSH 密码登录**（审查 #4）—— 关 `PasswordAuthentication` 与 root 密码登录。
      前置条件：所有要登录的机器公钥须先加入 `authorized_keys`（否则只有密码的机器登不上）。
      操作有锁死风险，需用回滚兜底手法。当前因待定其他机器密钥下发而搁置。
- [x] **apt 安全更新**（审查 #6）—— 2026-06-04 完成，剩余安全包 0。
      顺带修复 `ssh` 开机自启（原 disabled，重启会登不上）。无内核更新故未重启。
      遗留：libc6/apparmor 建议低峰重启收尾（非必须）。
- [x] **重启收尾** —— 2026-06-04 完成。重启前实证确认两个保命点（SSH 可连、客户机自动恢复），
      重启后 SSH 5 秒恢复、全部服务自启、ufw/ip_forward 恢复、端到端出口 IP 与基线一致（118.158.252.9）。
- [ ] **zhhub 降权运行**（审查 #7）—— 现以 root 运行，改用专用低权限用户。
- [ ] **bootstrap 审计日志 + 限流**（审查 #8）—— 记录来源/token 命中/时间，加频率限制。

## 不做（已评估否决）

### 裸代理（直接给客户机代理 IP，去掉客户端）

否决原因：
- 抗 GFW：HTTP/SOCKS5 握手与目标域名明文可见，IP 存活以小时~天计。
- 出口 IP 存活：出口 IP 暴露给全员，一个被封全员同时挂，且无法轮换保护。
- NAT 穿透：日本住宅宽带多在运营商 NAT 后，无可直连公网端口，
  「住宅机开放端口直连」物理上不成立——这正是现有「住宅机主动连 Hub」拓扑的原因
  （[系统架构](../10-architecture/system-architecture.md#L44-L45)）。
- 凭证管控：仅靠代理账号密码，弱于现有 token 的即时启停/过期（[store.go:65-81](../../hub/internal/auth/store.go#L65-L81)）。

裸代理唯一优势是设备覆盖 + 零分发；若该诉求强烈，正解是换更通用的隧道形态
（如订阅链接 + 通用客户端），而非退回裸代理。

## Android 出口远程控制安全约束

- Android 远程控制采用自研 Go SSH 控制面 `zhandroid-control` over WireGuard，只允许监听 `10.66.0.101:2022`。
- 禁止监听 `0.0.0.0` 或蜂窝/WiFi 公网接口。
- 只允许 SSH key 登录，禁用密码登录；真实 `authorized_keys` 和私钥不得入库。
- 远程控制通道只作为运维入口，不替代 Hub 侧健康检查和本机 watchdog 自愈。

