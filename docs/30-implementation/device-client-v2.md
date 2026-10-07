# CLI v2 设备授权消费者

更新时间：2026-10-07。实现位于 `clients/cli/internal/deviceclient/`，API 唯一来源为 [canonical OpenAPI](../../hub/internal/deviceapi/spec/openapi.yaml)。本组件通过显式 `device` 命令使用隔离的 v2 设备授权入口；当前生产仍由 legacy TokenStore 授权，未执行迁移或部署。

第五波新增 `device bind/start`，[独立启动合同](v2-proxy-bootstrap.md)明确本地 WG 私钥、当前 TLS 投影和真实引擎接线。`apply` 仍只接收手动公钥；`bind` 才持久化本地独立 X25519 key。`PrepareStartLocked` 只在持锁条件下取得配置、不启动引擎或写 config；根 app 接实际启动。CLI 不启 OS 代理，不把受控实际 WG fixture 当作生产导入或连续撤销 SLA。

## 命令及边界

```text
zhvpn device activate --server https://localhost:18443 --activation-stdin [--ca-file <CA.pem>]
zhvpn device apply --wg-public-key <本地公钥> --address <分配的单主机前缀> --expected-generation <N> --idempotency-key <唯一操作标识>
zhvpn device disable --expected-generation <N> --idempotency-key <唯一操作标识> [--reason <公开审计原因>]
zhvpn device revoke --expected-generation <N> --idempotency-key <唯一操作标识> [--reason <公开审计原因>]
zhvpn device status --operation-id <接受回执中的 operation_id>
zhvpn device rotate-credential
zhvpn device recover
zhvpn device cancel-pending [--activation-stdin]
```

所有命令输出单个 v2 JSON 回执，接受 `--json`，返回失败时非零退出。`--timeout` 限制整条命令，默认 15 秒、最多 30 秒。私有 CA 在每次命令中显式传入；证书和 hostname 正常验证，TLS 最低 1.3，不接受 HTTP、URL 用户信息、查询串或路径前缀，不跟随重定向，不提供 insecure 选项。传输直接连接，不使用环境代理。

激活凭证只从有界 stdin 读取，命令行不接受该秘密。Ed25519 私钥由客户端本地生成，API 只接收公钥与持钥签名。`apply` 接收 WireGuard 公钥，不导入 legacy YAML、授权码、WireGuard 私钥或生产 peer，不启动隧道，也不修改系统代理。设备认证私钥与 WireGuard 私钥是两种不同的密钥。

首次激活绑定显式服务器 URL；之后使用受保护状态中的 URL，传入其他服务器会拒绝。每次读状态、写 intent、请求 challenge、提交或恢复回执、提交本地状态，都在同一 canonical home 的真实 operations 文件锁内完成。

## 私有状态及签名

固定状态文件为 `<ZHVPN_HOME>/device-v2-state.json`，使用 `shared/proxy.NewPrivateState` 的现有 Owner/DACL、非重解析点、单硬链接及原子写策略。Windows 文件只允许原 home 用户、SYSTEM、Administrators；客户端不扩大 ACL，不自动修改真实用户 profile 权限。已存在目录不满足策略时拒绝执行。状态严格解析，未知版本、字段别名、重复字段、null、未知字段、无效私钥或公私钥不一致均拒绝进入网络请求。

challenge 请求携带三个头：`X-ZH-Challenge-Proof`、32 位小写十六进制 `X-ZH-Client-Nonce`、45 秒 `X-ZH-Client-Expires`。持钥证明使用 canonical `shared/devicecontract.ChallengeProofBytes`，绑定完整原始 JSON 请求体 SHA256、固定 method/path、nonce 和期限。后续 API 签名依 canonical 字符串数组绑定 purpose、method、path、精确提交 body SHA256、device/credential、challenge、nonce、request ID、challenge 期限。客户端不重序列化签名后的提交体。

轮换同时由当前私钥和新私钥对同一 canonical payload 签名；新私钥在 HTTP mutation 前写入私有 intent。旧私钥在已验证的新 public credential 和本地状态提交成功前保留。任何秘密均不进入 JSON 回执、stderr 或错误信息。

## accepted、effective 与未知结果

`202 accepted=true` 只证明 Hub 持久化接受了 desired-state operation。客户端初始回执必须 `effective=false`，不得把接受当成 WireGuard 生效。`status` 或 operation receipt 中的 `effective=true` 表示 Hub 对该最新 generation 的 allowed-ips 校验结果；这仍不证明客户端私钥持有、实际 WireGuard 握手或真实出口流量。

每次 mutation 先持久化原 request ID，命令同时保留幂等键和 expected generation；轮换保留新私钥。请求发出后的网络错误、超时、5xx 或无效 201/202 回执标记 `outcome=result_unknown`、`pending=true`，不重试，不建立新 operation，不丢弃密钥。intent 写失败禁止发送 mutation；服务器已提交而本地 receipt 写失败时，原 intent 仍能恢复。发起 HTTP 之前已取消的 context 可确定未发送，报告 `command_timeout`。

存在 pending intent 时只允许 `recover` 或显式 `cancel-pending`。恢复命令是只读查询：

| 原 mutation | 只读恢复 | 持钥证明 |
| --- | --- | --- |
| activate | `/api/v2/credentials/receipt`，原 request ID + activate + 新公钥 | 本地原激活私钥；未知 device/credential 字段为空 |
| rotate-credential | `/api/v2/credentials/receipt`，原 request ID + credential.rotate + 新公钥 | intent 中的新私钥；不再次轮换 |
| apply / disable / revoke | `/api/v2/operations/receipt`，原 request ID + 原幂等键 | 原 device/credential 和原签署私钥；self-revoke 后只允许历史 receipt |

恢复回执必须匹配本地原身份、公钥、action、generation；credential 不得延长原截止时间，轮换不得返回旧 credential。恢复成功后原子提交本地状态并清除 pending，不产生新的 Hub mutation。再次 `recover` 返回 `no_pending_intent`，不调用服务器。错误 key、request ID 或幂等键不能获取其他回执。

恢复口解决“服务器已经提交而响应丢失”。服务器找不到既有回执时，客户端保留 pending；只读查询本身不能安全推断一条未知网络请求永远不会提交。已过期、再次被替换或撤销的 credential receipt 也不能恢复新的授权。

`cancel-pending` 对 `/api/v2/requests/resolve` 发起显式 resolve-or-cancel，与原 mutation 共用 Hub 的真实 fence。已提交时返回原公开 receipt，客户端报告 `credential_recovered` 或 `operation_recovered`，不会谎报取消；尚未提交时，Hub 持久化永久取消 tombstone，阻止迟到的原请求，然后才返回 `cancelled`。只在验证该回执并提交本地文件之后，客户端清除 pending；未生效的 activation 私钥被舍弃，未生效的 rotation 新私钥被舍弃，原 credential/私钥保留。请求不会自动重试，也不建立新 operation。

激活取消需要通过 `--activation-stdin` 再次提供原激活秘密，该秘密不保存在 intent。轮换取消由新私钥作 primary proof，原 credential 私钥对相同 challenge proof 和最终请求分别提供 `X-ZH-Resolve-Owner-Proof`。命令取消使用原 device/credential/签署 key，严格绑定原 request ID 和幂等键。返回的 kind、request ID、对应公钥或幂等键必须存在且匹配；取消回执不能夹带 credential/operation，提交回执只能包含对应的一种。

取消响应丢失或本地提交失败仍保留 pending；再次显式取消会获得同一决定，不增长授权 mutation 或取消 tombstone。配额、无效证明或损坏响应也不能清除 intent。resolve 返回的 credential 可为历史记录，保留原 expiry；`credential_recovered` 在 `cancel-pending` 中只表示取回历史身份，不表示当前仍可用，后续授权请求仍受 Hub 当前状态检查。

## 独立回执契约

回执不扩展 legacy CLI `contracts.Result`。本包的 [receipt.schema.json](../../clients/cli/internal/deviceclient/receipt.schema.json) 使用 `contract_version=2`，`DecodeReceipt` 严格检查字段、必需 bool、版本、command/outcome、公共 credential、operation accepted/effective 关系。秘密状态结构不导出。

成功 outcome 为 `credential_created`、`credential_rotated`、`credential_recovered`、`accepted`、`status`、`operation_recovered`、`cancelled`；失败为 `rejected`、`result_unknown`、`local_error`。`request_id` 和 `idempotency_key` 是公开定位信息，不能当成授权凭证。消费者必须检查 v2 版本、`ok`、`pending` 和 operation 的独立 `effective` 字段。

## 验收证据

- [x] Windows 本地真实 TLS socket、正常证书校验、实际 protected 临时 home：激活、apply accepted、signed status、双签轮换、self-revoke。
- [x] 独立协议 fixture 核对 PoP 原始字节和最终请求签名数组。
- [x] activate、rotate、apply、self-revoke 已提交后断开 TLS 响应：原 intent 恢复、无额外 mutation。
- [x] 错误新私钥、原 request ID、幂等键拒绝；普通 status 不接受 self-revoked credential。
- [x] mutation timeout、无效 201、intent/receipt 存储故障、同 home 竞争、损坏本地状态、JSON 别名/重复/null/缺字段等回归。
- [x] 四类未知未提交请求显式取消；已提交请求取消返回原 receipt；取消响应丢失重复 resolve；错误 activation secret 和损坏取消回执保留 pending。
- [x] Windows synthetic unsafe ACL 原样拒绝，不修改已有权限、不写私钥、不发 HTTP。
- [x] `go test -race ./clients/cli/internal/deviceclient`、`go vet ./clients/cli/internal/deviceclient`。
- [x] WSL Ubuntu 24.04 实际 Linux TLS/临时 home 包测试（普通测试；该 WSL 没有 gcc，不能声称 Linux race）。
- [ ] 真实 Mac 硬件、本地文件保护和运行权限验收。
- [ ] current legacy authorization → v2 的受控 campaign、备份/恢复、真实 WireGuard 会话撤销、生产流量验收。

上述消费者 fixture 的 WG 状态为合成结果；实际 CLI 与 Hub SQLite/HTTP 的集成证据由 Hub 的 `integration` 测试和本次工作日志记录。此文档不把单元或编译通过声明为生产迁移完成。

第五波另有 `integration,with_gvisor` 的实际用户态 WG 测试：Windows normal CGO0 CLI/TLS/SQLite/sing-box/WG/proxy/target 已贯通，race仅指测试runner；Linux跑真实WG/TLS fixture，产品CLI后台启动明确skip。新 `start-receipt.schema.json`/`DecodeStartReceipt` 用contract2报告本地engine_ready或固定拒绝/state_unknown，不把本地ready当成实际出口验收。
