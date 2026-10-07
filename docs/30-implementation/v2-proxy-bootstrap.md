# 设备 v2 凭据到代理启动

2026-10-07 JST，第五波本地实施；本合同的 WireGuard 数据面使用隔离用户态 fixture。当前生产仍运行原 binary，未开启此能力。

`device activate` 建立 Ed25519 credential；`device bind` 在同一受保护 home 中生成、保存独立 X25519 私钥，再签名提交显式指派的客户 IPv4 `/32`。地址没有自动分配器；不读取或导入 legacy `wireguard/client.key`。接受 `apply` 表示 desired 已提交，`status` 的 effective 表示调度核验完成，不等于流量已通。

未知的 bind 结果保留原 key、request/idempotency identity 和地址 intent，后续 `recover/cancel-pending` 读取或明确取消原结果，不自动换 key 或重发新 apply。初始化后缺 key、公私钥不一致、pending 未解决、过期 credential 或旧 generation 都不能进入启动。

新入口 `POST /api/v2/proxy/bootstrap` 使用独立 `proxy.bootstrap` purpose。挑战/PoP 签名绑定精确 body bytes、method/path、device/credential、request ID 与 nonce。Store 在同一 authority fence/事务中核验当前 credential、device/address/public key、desired/applied generation、binding owner、address reservation、完成的 apply/outbox 和负面 tombstone。返回配置前重新检查时钟与期限。它不执行新的 WG mutation，也不重新探测内核；流量及持续对账另有证明要求。

响应是正常 TLS 1.3 校验下的 **30 秒启动投影**，受 credential/device 期限收紧；不是独立离线签名许可，也不是持续更新的会话租约。projection 绑定 epoch、固定 profile、profile revision/digest、当前 binding/generation。CLI 首次可信投影建立 epoch/profile floor，后续拒 epoch 变化、低 revision 或同 revision 内容变化；需要显式恢复流程，不能静默重登记。同 OS 用户回放整套旧状态不在此本地防回退保证内。

## 显式托管 profile

现有 `NewServer` 不注册新 route；测试使用 `NewServerWithProfile`，Linux hosting 需要 `ZHHUB_DEVICE_AUTH_ENABLED=1` 与额外 `ZHHUB_DEVICE_PROXY_PROFILE`。第七波同时要求匹配的 `ZHHUB_DEVICE_PROXY_GATE_POLICY` / `ZHHUB_DEVICE_PROXY_GATE_SOCKET`；只配 profile 拒绝。profile 无默认生产地址，必须匹配 authority policy 的 epoch/managed-by 和独立 customer WG interface。proxy IP 必须被 policy.Protected 预留，避免分给客户；仅允许该 private IPv4 的精确 `/32`，排除 `10.66.0.0/24`、默认路由和请求 override。

JSON profile 全字段必填：version、authority_epoch、managed_by、wg_interface、revision、wg_endpoint、wg_public_key、proxy_address、egress_id、egress_name、allowed_ips。endpoint 是 canonical IPv4 literal/十进制端口，不在验证过程中解析 DNS 或探测网络。digest 是 canonical string-array 的 SHA256 内容标识，完整定义在维护 OpenAPI 中；digest 自身不能建立发布者信任。

Linux reader 固定 root→leaf 文件句柄，检查 owner、权限、regular file、单 link、路径 identity/mtime/size 和前后两次读取；拒 symlink、FIFO、超限、duplicate/null/casing alias/未知 JSON 字段，不修源文件权限。profile 在 service lifetime 中冻结。公钥和 UDP endpoint 是否对应实际部署仍需操作者及实际流量核验，不由一份 JSON 自证。

第五波仅在 profile-enabled `Service.Run` TLS 前执行初始对账，独立真实 WG 反例证明：部分失败时未清除的外部旧 peer 仍可访问目标。第七波已将[真实 proxy Admission](device-proxy-startup-barrier.md)接到产品普通/striped/fetch，启动改为 closed ACK→initial Tick→最终 fenced WG/DB收敛证明→短期 grant→TLS；失败不放行。profile-OFF 顺序保持。这个补强只关闭匹配固定 scope 的该 proxy route；其他 WG 路径、同 IP 公钥身份、完整持续设备授权和生产生命周期门仍是 NO-GO。

## CLI 和引擎

```text
zhvpn device bind --address <明确指派的IPv4/32> --expected-generation <当前代> --idempotency-key <稳定ID> --ca-file <CA>
zhvpn device status --operation-id <接受的operation> --ca-file <CA>
zhvpn device start --expected-generation <已绑定代> --port <loopback端口> --ca-file <CA>
```

启动参数严格解析，输出独立 contract 2 JSON。根在同 home operation lock 下取得新投影、校验本地私钥派生公钥、写入无私钥 status cache、生成 exact-route sing-box 配置并启动真实引擎。已有引擎一律拒绝认领或静默替换；需要显式 authenticated `stop`。不启用 OS 系统代理、不支持 v2 `--fast`，不调用 token refresh 或手机 SSH/换 IP。通用 `start` 和 `import` 不能把 v2 cache 变成可复用授权。

引擎 generation 绑定 sing-box bytes **及完整 authority projection**，即使路由不变，epoch/generation 改变也产生不同 identity。子进程发布控制端点和 launcher 确认 ready 前均检查投影新鲜度。legacy config generation 的原 SHA256 保持兼容。启动取消只撤回本次 launch 并向已认证的该实例请求停止，不杀同名进程；文件 I/O 和控制请求仍有各自的可用性边界，不能仅由 context 宣称全部 OS 调用硬截止。

公开 `StartContext` 在 v2 路径还必须比对当前配置 bytes 与 canonical generator 的完整输出；不能把旧的 direct/TUN/其他路由配置挂上有效 authority stamp。activate 返回 ready 后及根最终收据前再次核验取消，避免请求在控制回包途中到期仍报告成功。同owner同时改写全套私有launch/config/control材料不属于外部授权防伪保证。

`engine_ready` 仅表示精确子进程完成本地引擎启动和控制握手，实际 egress 用目标响应、WG handshake/transfer 另验。缓存无 WG 私钥，但含公共 profile/identity；设备认证私钥与 WG 私钥只在私有 state。logout 不等于 authority revoke。

## 验证范围

- Windows 真实 CLI → TLS/SQLite → sing-box → 真实用户态 WG → 隔离 HTTP proxy → owned target marker；正常 CGO0 CLI exe 与 race 测试 runner 分开记录。
- 实际 scheduler 操作 WG peer；handshake/rx/tx 对应客户 key。撤销后新 TCP 请求失败，另一个受保护 peer 保持访问；迟到 apply/Store 与 WG 重建不恢复已撤销的 key。
- 当前 key 丢失/不匹配、错 CA、body 篡改/nonce 重放、pending/degraded、旧 generation、profile/epoch/revision/expiry 反例，拒绝路径不能误认领引擎或输出私钥。
- Linux 是实际 TLS/WG fixture及受保护文件行为；Linux CLI 后台 launch 仍未实现。Darwin 编译不代替 Mac 实机。

这片不证明生产 Linux WG 权限/防火墙、现有 token 导入/campaign、GUI/SDK 设备登录、手机 reverse mTLS 迁移、已建立会话的完整撤销 SLA、生产性能/连续观测或签名发行。对应总阶段继续未勾选。
