# P1 设备授权与撤销合同

> 状态：IN_PROGRESS，2026-10-07 JST。开发分支已有持久设备授权、TLS v2 API、后台调度和受监督 WG adapter，证据与当前接线见 [运行时集成](steelman-runtime-integration.md)。未接管生产 TokenStore，campaign/导入和真实隧道撤销仍待验收。
> 对应 [Steelman 计划](./zhvpn-steelman-refactor-plan.md) P1/P4/P5/P8；事实基线见 [10-06 只读资产记录](../90-history/worklogs/2026-10-06-zhvpn-asset-baseline.md)。legacy 客户端生产迁移仍以[现有安全迁移计划](../40-security/client-security-migration-plan.md)的 campaign、观察窗口和 Go/No-Go 为执行依据。

## 1. 当前状态与目标的分界

当前授权事实源是生产 `tokens.yaml` 与启动时加载的 `auth.TokenStore`。SQLite 当前保存 Admin 会话、审计、migration observations 和界面投影，不保存设备授权、credential、peer ownership、outbox 或正式 campaign。WG 公钥提交目前直接触发 `wg set`，API 到期/禁用判断不会自行撤销旧 peer。

目标是以持久设备/凭证/授权关系表达 desired state，由单一可信执行边界驱动客户 WG peer。沿用已有 SQLite 能力，不增加数据库服务或 hosted CI。开发分支使用独立前缀表实现 customer device、credential、nonce、binding 历史、tombstone、operation、outbox 与 action intent，并有默认关闭的 TLS API/到期调度及显式 Linux supervisor。未接入现有 TokenStore，也未导入当前客户或接管 wg0；独立客户 interface 保护 legacy writer。导入/campaign、备份恢复撤销合并及真实隧道验收仍待实施；fake WG + 真实进程测试不代表实际 WG 权限已撤销。

范围包括客户 CLI/GUI/SDK 及受登记出口的身份边界；客户 Android App 仍限于既有 Slice 0，不能按本合同宣称可分发、正式上线或内部 Alpha。新 API 必须记录对该 Slice 的兼容性和测试缺口；出口 Android 基础设施与客户 Android 是不同角色。

## 2. 身份与密钥

| 对象 | 语义/约束 |
| --- | --- |
| legacy token ID | 保留现有稳定非秘密标识，用作历史观测/campaign 关联；不是新设备身份，也不是认证凭证。导入须检测标识碰撞，不能默默合并 |
| installation ID | 清册中一个实际安装实例；同 token 的多个实例必须分别核验，不能从一次 bootstrap 推定“一 token 一实例” |
| device ID | 服务端生成的稳定 opaque ID；角色、所有权和状态由服务端确定；与 OS 用户、home 和版本证据明确关联 |
| activation credential | 受限用途、有效期及次数的激活材料；只能授予指定角色/权限。新持久化只保存受保护 verifier，不保存可重放原文 |
| device authentication key | 用于设备认证/请求签名的独立密钥。P1 默认选用标准 Ed25519 签名，私钥只留设备；算法/编码/域分离由版本化合同固定 |
| WG key | WireGuard 使用的 X25519 密钥，用于 WireGuard 握手，不是通用签名密钥。不得假定现有 WG 私钥能直接执行 Ed25519 签名 |
| reverse credential | 每个出口独立的 TLS 身份/凭证，绑定已登记 egress 角色；不是客户 WG 身份或所有出口共用的长期 token |

认证私钥、WG 私钥和 TLS credential 的用途分别定义，不靠隐式转换或复用推定安全。客户提交 WG 公钥时，认证请求绑定 `device_id`、公钥、目标 generation 和操作用途；这是设备对关联的授权声明，不自动证明其持有 WG 私钥。

若产品宣称已验证 WG 私钥持有，必须以服务端可关联的真实 WG 握手/数据面挑战取得证据；验公钥长度、请求签名或 `wg set` 成功都不能代替该证据。身份登记、peer 已应用、WG 已验证分别返回状态。

## 3. 状态与最小持久模型

以下是逻辑模型，最终 schema 在实现切片通过合同/生成校验后确定；不要求每行概念对应独立服务。

| 事实 | 必须持久保存 |
| --- | --- |
| 设备与 ownership | device ID、角色、明确 owner、状态、有效期、authority epoch、generation |
| credential | credential ID、device ID、类型、认证公钥/verifier、用途、有效期、换发关系和撤销记录 |
| 客户 WG binding | device ID、公钥、分配地址、managed-by 标识、role=customer、desired generation、applied generation、前后映射 |
| 激活/挑战 | challenge ID、nonce/绑定摘要、用途、过期时间、消费结果、幂等关联 |
| 操作与 outbox | operation ID、幂等键/请求摘要、主体/资源、desired generation、状态、尝试/截止时间、执行结果及可定位错误 |
| 撤销 | 对象、generation/epoch、生效预算、原因、提交/执行/核验时间和持久 tombstone |
| 导入/campaign lineage | legacy token ID→installation ID→device ID 关联、导入版本与校验、campaign 成员及 lifecycle disposition、T0/窗口/证据引用 |

设备状态至少区分 pending、active、disabled、expired、revoked。binding 状态至少区分 unapplied、apply_pending、applied、revoke_pending、revoked、degraded。credential/设备/binding 的状态不能用一个 `enabled` 或 `connected` 布尔值相互代替。

- generation 对同一授权资源单调增加；authority epoch 区分授权事实源切换及已批准恢复世代。
- 新启用是显式 apply/重新授权操作；禁用、到期、删除必须 revoke；换钥撤销旧公钥。重新启用不能擦掉历史 tombstone，已撤销 credential 不再次生效。
- 统一固定 UTC 时间、边界条件和受控时钟。legacy 日期 expiry 在迁移适配中明确保留旧解释，不能因新 schema 默默延长有效期。
- 客户 WG 地址与公钥具有唯一/冲突约束；申请不能占用其他设备或受保护资产的公钥/地址。
- 删除业务对象不得先删除执行撤销所需的 binding、前后映射和 tombstone。审计保留/压缩不得删除仍约束权限的撤销事实。

## 4. 激活、请求重放与 credential 换发

- [ ] 激活必须走受验证的安全入口；nonce 含用途、期限、请求绑定和设备认证公钥关联，校验设备认证私钥持有。
- [ ] 一次性材料的消费、设备/credential 发放和对应审计在同一事务完成；并发消费只能产生一个既定结果。
- [ ] 请求签名使用版本化 canonical payload，绑定 method、path、body 摘要、device/credential、操作用途、request ID、nonce 与期限，拒绝跨用途/资源/版本重放。
- [ ] 幂等键作用域至少包含主体与操作；相同键不同摘要是冲突。合法重试返回同一 operation/result，不执行第二次授权、换钥或换 IP。
- [ ] 返回历史结果前重新验证当前主体状态。幂等缓存不能令后来已撤销的 credential 再授权，也不能向已失权主体重新发放秘密。
- [ ] credential 换发设置有限重叠窗口和有效期；旧 credential 的终止不依赖客户端主动退出；退出窗口后负例必须拒绝。

TLS 信任锚、签发主体、证书角色、有效期与换发流程必须有独立维护源。通过同一 CA 的证书仍需检查具体已登记出口、状态与权限；新 reverse 认证失败不能降级 raw TCP。手机 TLS 方案必须以当前实际二进制/能力验证决定，不能沿用旧 Motorola/dxreverse 叙述作为部署依据。

## 5. apply/revoke 与可信执行边界

授权 API 事务提交 desired state、operation 和 durable outbox；API 可回 `accepted`，只有 executor 核验运行态后才能报告 `applied`/`revoked`。WireGuard 外部动作无法与 SQLite 做同一个原子事务，合同必须覆盖“DB 已提交、WG 尚未改变”和“WG 已改变、结果未提交”两种窗口。

执行边界的硬约束：

1. 客户 peer 的自动变更只通过单一受控 executor。API/兼容服务/回退版本不能拥有绕过该边界直接执行 `wg set` 的能力；上线前固定服务权限与可回退最低安全版本。
2. 单机跨进程执行具有崩溃释放的互斥及 fencing；授权提交与执行按已定义资源顺序协调。仅有 leader lease、PID 或执行前查一次 generation，不足以防止失去资格的旧进程继续改 WG。
3. executor 在可信边界核验 authority epoch、当前 desired generation、ownership、允许动作和保护清单；旧任务是 superseded，不能覆盖新 revoke。具体锁/提交协议须通过两个进程乱序执行测试后再选定。
4. operation/outbox 持久化且可重试；同 operation 重复执行收敛到相同 desired state。重试前重新读最新授权，不能按队列中的旧参数无条件重放。
5. 执行后核验对应公钥/allowed IP 及撤销范围，再记录 applied generation；超时/响应丢失返回 pending/unknown，不能伪报成功。子进程有总截止时间。
6. 换钥保留旧/新 binding 和 operation journal；同地址的 WG 路由切换不是“两个公钥同时拥有地址”。记录预期短暂切换及核验，失败只恢复仍获授权的受影响映射。

受保护边界比自动修复优先：

- 只有 `managed_by=本执行者`、`role=customer`、ownership 已核验的 binding 才可 apply/revoke/reconcile；“已登记资产”本身不是删除资格。
- RDP、管理员、Mac/手机控制面和 unknown peer 不归客户 reconciler 管理。数据库缺记录、无握手、无 allowed IP 都不是删除理由。
- 保护清单校验公钥和地址冲突，不能仅检查地址池。身份重分类/转移是独立已审计操作；不自动吞并历史资产。
- 永远不整体覆盖运行中的 `wg0`。运行态与持久源配置的变更精确限定受管 binding，源配置恢复也保留非客户/unknown peer。

## 6. 租约、撤销预算与 restart

设备授权由 identity/credential/ownership 决定，来源 IP 只作审计信号。若需要单实例或单会话租约，必须明确 session ID、续租、离线宽限、抢占、到期和数据面处置；同 NAT、移动换 IP 和多个 home 不自动视为同设备/不同设备。

XFF 只在明确 trusted listener 上由明确代理赋值；兼容直连和普通 WG 客户不能靠 private IP 获得“可信 forwarder”资格。所有入口受请求体/字段上限、header/read/write/idle 超时、并发、速率和子进程总预算约束。

建议初始撤销目标（PLANNED，须在隔离环境与 canary 中验证）：健康 Hub 上，从持久 revoke 提交至运行态核验，p99≤5 秒、最长≤30 秒。该预算包含新请求拒绝、客户 peer 失权和相关受管 session/stream 终止；不能只测下一次 bootstrap 返回 401。

- 超预算持久标记 `revoke_pending/degraded`、告警并禁止该对象新的授权动作，继续受控重试；不牵连删除所有 peer。
- 到期驱动有持续调度，API 无请求也会触发；时间源/调度故障是可观察异常。
- executor/API/WG 重启后，从最新 epoch/generation、outbox、tombstone 与实际 WG 精确对账；不会仅加载旧 `.conf` 复活已撤销公钥。
- crash injection 分别覆盖提交前/后、WG 动作前/后、结果写入前/后、换钥中途和撤销中途。重新启动只收敛最新授权状态，不恢复失权状态。

## 7. YAML/SQLite 接管与 campaign 连续性

迁移至少有三个明确阶段：YAML_CURRENT → DB_SHADOW_VALIDATED → DB_AUTHORITATIVE。前两阶段仍以现有 TokenStore 为业务权威；数据库导入/比较不提前改变运行授权。切换在单独批准的维护窗口进行，禁止 YAML 与 DB 双向独立写入同一授权事实。

- [ ] 先冻结并建立受限 token/实际安装实例/ownership 清册；导入 idempotent、版本化且可校验。token 中存在旧私钥不意味着该公钥仍拥有 runtime 地址。
- [ ] 保留原 token ID、全部 campaign 成员、处置原因、历史观测、E2E 证据引用、T0 与窗口重置记录；删除/禁用/到期不从分母静默消失。
- [ ] 记录 token→installation→device 的一对多及换发关系；共享 token 的一个成功观测不能自动让多个 device compliant。
- [ ] 保留旧 token 观测作为历史证据，不自动升级成新设备的认证/端到端/合规证据；新 device 必须取得对应的真实双次验证。
- [ ] 权威切换同时修改 readiness reconciliation 合同与生成/消费者测试，从新的权威源对账并保留 legacy lineage；不能让旧 YAML 分母或空影子表制造“全部 compliant”。
- [ ] 不重设 T0、不缩短既有 30 天静默、保守发现及其他维护观察窗口；出现 legacy/direct/unknown 或观测空洞按现有规则重置并保留旧记录。
- [ ] campaign 未完整实现、ownership unknown、观测不连续或迁移校验失败时维持 NO-GO；端口通、测试 token 或数据库表存在都不构成收口门禁。

## 8. rollback 不恢复撤销权限

代码、schema 和安全状态的回退分别设计。允许回退的版本必须理解当前授权 schema/epoch/tombstone，并继续使用可信 executor；不能以回退到旧 root 服务的方式重新开放直接 `wg set`。

- 备份恢复需要保留/合并备份之后的撤销记录与最新生效世代。拿不到这段事实时，受影响授权 fail closed，而不是恢复旧 enabled、私钥或 allowed IP。
- 被禁用、过期、撤销的 credential 以及换钥后已终止的旧公钥不会因恢复 config、DB 或 source `.conf` 复活。
- 旧迁移计划允许的临时 legacy 私钥/入口应急恢复，仅适用于当前仍获授权并重新核验的对象；不能恢复已失权对象或扩大范围。HTTP 应急仍需明确来源、期限和原计划的窗口重置，不能自动长期开回明文。
- 回退只恢复受影响且仍获授权的映射，保留 RDP/管理员/Mac/手机/unknown peer。privileged break-glass 不是自动流程，必须另行确认并审计。
- 必做演练：“禁用设备→部署失败→代码回退→DB/配置恢复→WG 重启”，证明失权仍生效，受保护资产仍正常。

## 9. P1 最小离线验收与下一切片

以下先用合成 credential、fake WG executor 和隔离 SQLite 验证；P1 合同完成不等于生产启用。沿用本地门禁，不增加 hosted CI。

- [ ] 固定版本化请求/响应、identity/key 编码、状态和错误码；HTTP canonical OpenAPI、Admin 现有 oapi-codegen/sqlc 与 CLI/SDK 消费者对齐，禁止手改生成物。
- [ ] 合成导入覆盖 legacy 共享 token、未知 peer、`.30` 类公钥冲突、过期配置、runtime 无对应 peer、稳定 ID 碰撞和 campaign 处置保留。
- [ ] 激活、请求签名、nonce、幂等及换发的允许/拒绝/并发/重放断言通过；WG X25519 与设备签名密钥绝不混用。
- [ ] 两个独立进程重复/乱序 apply/revoke、过期 executor、超时/失去响应、逐步崩溃的结果均收敛最新状态，且旧任务不覆盖已生效 revoke。
- [ ] 禁用、到期、删除、换钥、重启和备份恢复精确保护非 customer/unknown peer；撤销预算、pending/unknown 语义有证据。
- [ ] 双次安装实例/E2E 与 campaign 分母/T0/观察历史保持连续；没有证据的条目保持 unknown/NO-GO。
- [ ] 手机 TLS/mTLS 双端离线负例和真实手机能力切片另行完成；客户 Android Slice 0 兼容性明确记录，不能扩大为客户 Android 发布承诺。

下一步仅建立本合同的版本化 DTO/schema、隔离持久模型与 fake executor 回归切片；真实 authority 接管、peer 变更、证书/手机部署及兼容退役均待各自成熟门禁和具体生产确认。
