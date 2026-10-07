# v2凭据到真实代理启动接线计划

2026-10-07 JST；第五波，待实施。仍在隔离worktree，不部署当前Hub/手机或改wg0/OS代理。目标是显式device bind/start使用本地WG私钥、经PoP签名请求取得可信TLS配置投影，并通过实际用户态WG传递受控流量。已有激活/apply/effective并不等于这条链路完成。

- [ ] canonical OpenAPI增加专用proxy.bootstrap请求purpose与DTO；维护源生成/严格消费者/旧endpoint兼容通过。
- [ ] trusted本地route profile显式启用新入口，绑定authority epoch、managed-by/customer interface、profile revision/digest、WG endpoint/publickey、HTTP proxy地址。没有默认生产地址；仅精确IPv4 proxy /32，拒管理网/默认路由/请求override。profile错误拒绝开启该能力。
- [ ] authority事务重新验证live credential、当前公钥/地址、期望generation、applied==desired、nonce/purpose。配置投影绑定上述事实与短有效期，经现有正常校验证书的TLS返回；不返回token、私钥或SSH地址，不以TLS响应冒充独立离线签名产物。
- [ ] device bind在私有state生成/保存本地X25519密钥，沿现有apply/outbox/receipt提交显式指派的地址；不引入自动地址分配，不导入legacy client.key，不在结果未知时换key重派。
- [ ] device start取得新投影，公钥严格等于本地派生值、epoch/generation/scope一致才写可启动配置并调用现有引擎。config携带明确授权来源与精确allowed-ips；v2不走token refresh或旧10.66路由默认值。GUI/SDK的新设备登录交互另验收。
- [ ] 真实CLI→TLS/SQLite→sing-box→用户态WG→隔离proxy→owned target唯一marker贯通；实际握手/transfer与公钥一致，撤销后新请求失败，保留另一受保护peer正对照。不能以memory fakeWG计作此证据。
- [ ] 错CA/公钥/epoch/profile/address/generation、nonce重放、过期/撤销、pending/degraded、key丢失、响应丢失、端口占用等拒绝路径无可启动配置或误认领引擎；Windows真实CLI启动及Linux实际WG/TLS fixture分别记录。
- [ ] 独立反例、生成/Go/race/消费者统一门禁、九目标clean开发构建、实现/架构/安全/diagnostics与当日worklog更新后再本地提交。

测试customer pool用独立私有网（例如10.250.0.0/24），精确proxy路由10.250.0.1/32，仅在owned fixture启用对应proxy来源CIDR。现有依赖支持gVisor+wireguard-go用户态fixture，无需管理员TUN或新的库。Linux CLI当前启动stub不能算实际产品启动通过；macOS实机、真实Linux WG权限/防火墙、手机reverse迁移、授权导入/campaign、完整会话租约与持续撤销SLA均继续在总计划未勾选。
