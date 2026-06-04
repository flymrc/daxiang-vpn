# 安全与抗封改进 TODO

本文件记录已确认要做、但尚未落地的安全与抗封改进项。按优先级排列。
背景讨论：客户端是否可被裸代理（直接下发代理 IP）替代——结论是不可，
隧道方案在抗 GFW 识别、保护出口 IP、穿透住宅 NAT 上不可替代，详见各项「原因」。

P0 各项已由 [Hub 安全审查报告 2026-06-04](../ops/SECURITY_AUDIT_2026-06-04.md) 实测确认。

## P0：私钥与凭证安全（必须先做）

这两项解决当前实现里真实存在的安全洞，与「要不要客户端」无关，应一起做。

### 1. Hub 启用 TLS

- 现状：Hub 用明文 HTTP 监听（`http.ListenAndServe`，[main.go:26](../../backend/dxhub/cmd/dxhub/main.go#L26)），
  bootstrap 响应里直接下发 WireGuard 私钥（[server.go:55-61](../../backend/dxhub/internal/auth/server.go#L55-L61)）。
- 风险：私钥与 token 经明文 HTTP 在中国↔日本这条最敌对的链路上传输，可被中间人窃听/篡改。
- 方案（二选一）：
  - 在 Hub 前置反向代理（Caddy/Nginx）终止 TLS，对外只暴露 HTTPS。
  - 或在 Go 服务内直接 `ListenAndServeTLS`。
- 验收：客户端只通过 HTTPS 访问 `/api/client/bootstrap`；明文 HTTP 不再对外。

### 2. 私钥客户端本地生成，只上报公钥

- 现状：私钥存在 Hub（[store.go:45-48](../../backend/dxhub/internal/auth/store.go#L45-L48)），
  经 HTTP 下发给客户端。私钥同时存在于 Hub + 链路 + 客户端三处。
- 目标：客户端首次启动时本地生成 WireGuard 密钥对，私钥永不离开设备，
  login/bootstrap 时只把【公钥】上报给 Hub，Hub 用公钥配置 peer，从不接触私钥。
- 好处：
  - 消除明文链路上的私钥泄露；公钥泄露无所谓（本就公开）。
  - 攻击面从三处收缩到客户端一处；Hub 被入侵也拿不到任何客户端私钥。
  - Hub 不再是「私钥金库」，消除单点全灭风险。
  - 符合架构文档既定规则「私钥尽量只保存在所属设备上」（[ARCHITECTURE.md:391-392](../architecture/ARCHITECTURE.md#L391-L392)）。
- 依赖：必须配合 P0-1 的 TLS 一起做——TLS 不是为了保护公钥（公钥不怕看），
  而是防止上报的公钥被中间人篡改替换，并保护 token 本身不被窃听。
- 验收：Hub 的 tokens 配置里不再出现任何客户端私钥；客户端本地持有私钥。

## P1：抗封升级（被针对性封 WireGuard 时启用）

- 现状：客户端走用户态 WireGuard 隧道（[singbox.go:92-111](../../frontend/dxvpn/internal/proxy/singbox.go#L92-L111)）。
  加密优于裸代理，但 WireGuard 握手有可识别指纹、且是 UDP，可能被 GFW 识别/限速/阻断。
- 目标：在 sing-box 引擎中启用伪装成正常 TLS 的协议（Shadowsocks+插件 / Trojan / VLESS+Reality），
  让流量「藏进人群」，而不是自研私有混淆。
- 为什么不自研混淆：私有指纹全球唯一、无人测试、封它零误伤，等于单挑国家级系统持续升级，几乎稳输；
  正确做法是复用社区方案。sing-box 已内置这些协议，只是引擎里特意没注册以缩小体积
  （[engine.go:78-84](../../frontend/dxvpn/internal/proxy/engine.go#L78-L84)），升级 ≈ 注册多几行 + 改 Hub 配置。
- 代价（需提前评估）：
  - Trojan 需域名 + 真实 TLS 证书，最好背一个正常网站作门面（门面放 Hub，住宅出口做不了）。
  - VLESS+Reality 免域名但需挑稳定的「借壳」目标站，实现/版本敏感。
  - 套 TLS 比裸 WireGuard 多一层加解密与握手 RTT，在本就高延迟的中日链路上可感知。
  - 运维复杂度上升：证书续期、借壳维护、参数对齐。
- 触发条件：当观测到 WireGuard 隧道被针对性阻断/限速时优先做此项。

## 运维加固（来自 [安全审查 2026-06-04](../ops/SECURITY_AUDIT_2026-06-04.md)）

服务器层面的加固项，与代码改动独立。详细背景见审查报告。

- [x] **防火墙**（审查 #3）—— 2026-06-04 完成。装并启用 ufw，默认 deny incoming，
  仅放行 `22/tcp`、`51820/udp`、`18080/tcp`（18080 待 TLS 后收）。修复记录见审查报告。
- [ ] **SSH 加固 + fail2ban**（审查 #4/#5）—— 关闭密码登录与 root 密码登录、装 fail2ban；
      当前正遭万次级爆破。操作有锁死风险，需用同样的回滚兜底手法。
- [ ] **apt 安全更新**（审查 #6）—— 226 个待装。
- [ ] **dxhub 降权运行**（审查 #7）—— 现以 root 运行，改用专用低权限用户。
- [ ] **bootstrap 审计日志 + 限流**（审查 #8）—— 记录来源/token 命中/时间，加频率限制。

## 不做（已评估否决）

### 裸代理（直接给客户机代理 IP，去掉客户端）

否决原因：
- 抗 GFW：HTTP/SOCKS5 握手与目标域名明文可见，IP 存活以小时~天计。
- 出口 IP 存活：出口 IP 暴露给全员，一个被封全员同时挂，且无法轮换保护。
- NAT 穿透：日本住宅宽带多在运营商 NAT 后，无可直连公网端口，
  「住宅机开放端口直连」物理上不成立——这正是现有「住宅机主动连 Hub」拓扑的原因
  （[ARCHITECTURE.md:44-45](../architecture/ARCHITECTURE.md#L44-L45)）。
- 凭证管控：仅靠代理账号密码，弱于现有 token 的即时启停/过期（[store.go:65-81](../../backend/dxhub/internal/auth/store.go#L65-L81)）。

裸代理唯一优势是设备覆盖 + 零分发；若该诉求强烈，正解是换更通用的隧道形态
（如订阅链接 + 通用客户端），而非退回裸代理。
