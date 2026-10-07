# 客户端 HTTPS 与本地 WireGuard 密钥迁移计划

> 状态：**ACTIVE / 阶段 2 observation-only canary；最终生产收口仍为 NO-GO**
> 文档类型：长期执行清单
> 创建日期：2026-08-28
> 适用范围：Windows GUI、独立 CLI、Python SDK、Hub bootstrap/rotate、Caddy 入口、token 生命周期
> 不在本计划内：Android 出口实现改造、反向隧道改造、其他产品和仓库根产品代码

## 1. 目标

2026-10-06 衔接：[Steelman 重构计划](../30-implementation/zhvpn-steelman-refactor-plan.md)新增设备身份、有效 peer 撤销与 reverse 认证加密的待实施合同；本文件仍负责 legacy HTTPS/私钥收口，不改变当前 observation-only/readiness、campaign、观察窗口或不增加 hosted CI 政策。授权事实源接管须保留历史分母和 lineage，不能按计划提前宣称已部署。

同日本地后续切片已建立 [CLI JSON 同源消费者](../30-implementation/cli-json-contract-v1.md)、[设备授权离线模型](../30-implementation/device-auth-foundation.md)与未接入控制面的代理租约核心。fake executor、合成注册表和模型回归均不计入生产 campaign、实际安装实例或真实撤权证据；现有 NO-GO 与观察窗口不因这些代码通过测试而改变。

将仍在使用的客户端从迁移期兼容状态安全收口到以下目标状态：

- 客户端只通过受信任的 HTTPS 入口调用 bootstrap/rotate。
- WireGuard 私钥只在客户端本地生成和保存。
- Hub 只接收公钥，任何成功响应都不包含客户端私钥。
- 所有继续有效的安装实例均有可审计的版本、协议和端到端成功证据。
- 公网 legacy HTTP listener 关闭；Hub 客户端 API 仅能由本机 Caddy 访问。
- 无主、共享、休眠未知或拒绝升级的 token 已撤销、停用或安全换发。
- 收口过程不影响 WireGuard、代理数据面、Android 出口和管理控制台。

安全合规不能只由版本号决定。Hub 可以根据单次请求判定 `secure_bootstrap`，但一个安装实例只有同时满足下列条件并通过观察窗口，才能记为 `compliant`：

```text
批准的发布构建
+ 受信任的 HTTPS ingress
+ 有效 wireguard_public_key
+ 响应未返回 wireguard.private_key
+ WireGuard 和代理端到端验证成功
```

## 2. 清单使用规则

- `[ ]` 表示尚未完成或证据不足。
- `[x]` 只能在任务已验证后勾选，并在“执行记录”中补充日期和脱敏证据位置。
- 不使用“基本完成”“大概新版”等中间结论；证据缺失一律记为 `unknown`。
- 任一阶段的退出门槛未全部满足，不得开始下一阶段的生产变更。唯一例外是：阶段 1 退出门槛通过后，可在阶段 0 的人员清册尚未完成时部署可逆、非阻断的阶段 2 观测 canary，用于建立事实；它不得设置 campaign `T0`、发布正式客户端、清理私钥或收口 listener。
- 每次只执行一个有明确边界的变更切片；私钥清理、防火墙收口和 listener 变更不得放在同一维护窗口。
- 先补离线回归测试，再改变行为；先跑聚焦测试，再跑项目本地完整门禁。
- 本项目不增加 hosted CI；所有门禁按项目政策在本地运行。
- 真实用户、联系方式、token 明文、完整 IP、公钥、私钥、客户端路径和原始日志不得写入本文件或公开仓库。

## 3. 事实源与证据位置

### 3.1 产品事实源

- token 启用、到期和客户端配置：生产 `tokens.yaml` / `TokenStore`。
- 客户端请求合同：[CLI bootstrap 客户端](../../clients/cli/internal/bootstrap/client.go)。
- Hub 分类及响应行为：[Hub bootstrap handler](../../hub/internal/auth/server.go)。
- 管理审计持久化：[Hub admin DB](../../hub/admin/internal/db/)。
- 发布版本：对应产品元数据和批准的发布清单，不另建第二份运行时版本配置。
- 当前迁移背景：[P0 客户端发放记录](../90-history/worklogs/2026-07-02-client-p0-release-packages.md)。
- 生产收口程序：[Hub API 部署 runbook](../20-operations/runbooks/hub-api-deploy.md)。

### 3.2 私有证据位置

- [x] 在外层 ZeroCoreUnit00v2 工作区的机器本地 `.local/zongheng-vpn/migration/` 建立受限迁移目录；不要在本产品 Git 根目录内创建同名目录。
- [x] 建目录前确认解析后的绝对路径位于 `zongheng-vpn` Git 工作树之外。
- [x] 确认外层仓库忽略该目录，且公开树审计不会发布其中内容。
- [ ] 只在私有迁移矩阵中记录用户/安装实例与稳定 token ID 的对应关系。
- [ ] 私有矩阵不保存 token 明文、公钥、私钥或完整配置。
- [ ] 含 legacy 私钥的生产备份只保存在 Hub root-only、受控加密目录。
- [x] 为私有证据配置负责人、权限、保留期和销毁条件。

## 4. 当前基线

- [x] 已确认当前 Hub 不能从历史日志证明客户端产品类型或版本。
- [x] 已确认成功 bootstrap 审计没有保存“是否携带公钥”的布尔证据。
- [x] 已确认当前 Hub 不能追溯请求来自 HTTPS 反代还是公网 HTTP 直连。
- [x] 已确认配置中保留 legacy 私钥不能证明客户端实际使用旧协议。
- [x] 已确认现有历史活动只能用于建立清册，不能回填为新版证据。
- [x] 当前全局结论为 `ready=false`。
- [ ] 将当前 token 总量、启用/到期状态和历史活动窗口记录到私有迁移矩阵。
- [ ] 为每个仍启用且未过期的 token 找到明确负责人和安装实例。
- [ ] 将共享 token 拆分为“一安装实例一 token”；未拆分前保持 `unknown`。
- [ ] 明确管理、测试、无人值守 SDK 和低频实例的责任人。

## 5. 迁移判定模型

### 5.1 安装实例状态

- `unknown`：没有可信观测，或观测链有空洞。
- `legacy`：使用公网 HTTP、未上报公钥、收到私钥，或版本/协议不受支持。
- `candidate`：已有一次 `secure_bootstrap`，但客户端端到端证据、第二次验证或观察期尚未完成。
- `compliant`：Hub 侧运行证据和安装实例端到端证据均完整，并通过规定的静默窗口。
- `disabled`：已审计停用，不再属于允许继续使用的分母。
- `revoked`：token 及对应旧 peer/session 已撤销。

### 5.2 Campaign 分母

- [ ] 在阶段 0 建立预迁移清册快照；它是定责依据，不是正式 campaign 分母。
- [ ] 可审计客户端发布就绪且 Hub 可信观测稳定后，将清册快照固化为 campaign，并设置 `T0`。
- [ ] 分母包含 `T0` 时所有启用且未过期、业务上仍需使用的 token。
- [ ] 观测期间新发 token 自动加入分母，并从创建起要求新协议。
- [ ] token 被删除、停用或到期时必须写入审计处置，不能从分母静默消失。
- [ ] campaign 分母与 `tokens.yaml` 定期自动 reconciliation。
- [ ] readiness 只允许 `compliant`、`disabled`、`revoked`；存在一个 `unknown`、`legacy` 或临时例外即为 NO-GO。

### 5.3 推荐节奏

| 阶段 | 参考时长 | 说明 |
|---|---:|---|
| 0. 冻结和定责 | 1–3 天 | 完成预迁移清册、责任人和实施决策 |
| 1. 合同与离线测试 | 2–4 天 | 先建立可验证边界，不触碰生产兼容路径 |
| 2. Hub 观测部署 | 至少 24 小时 | 验证分类连续可靠，暂不设置 T0 |
| 3. 客户端发布与 canary | 2–3 天 | 三类产物通过后设置 T0 |
| 4. 用户升级 | 主动追踪最多 21 天 | 无法全量确认时留在本阶段进入最多 90 天保守发现 |
| 5. 静默观察 | 30 天 | 只有 unknown 全部处置后才能开始 |
| 6. 预演与备份 | 1 天 | 必须在目标 RTO 内完成恢复演练 |
| 7. Enforce 与私钥清理 | 7–12 天 | 分批处理，至少 7 天全量浸泡 |
| 8. 公网入口收口 | 约 10 天 | 防火墙、listener 分窗口执行 |
| 9. 最终观察与退役 | 30 天 | 稳定后移除兼容代码并销毁敏感备份 |

## 6. 阶段 0：冻结、定责和实施决策

### 6.1 角色

- [ ] 指定迁移负责人，维护 campaign 分母和总门禁。
- [ ] 指定客户端发布负责人，维护版本和 SHA256 发布清单。
- [ ] 指定 Hub 运维负责人，负责部署、备份、监控和回滚。
- [ ] 指定安全复核人，独立复核 Go/No-Go 和敏感信息边界。
- [ ] 指定用户沟通负责人，维护私有用户清册和升级预约。

同一人可以承担多个角色，但发布、生产切换和最终 Go 审批必须使用独立检查清单。

### 6.2 决策

- [ ] 批准“先观测、后升级、再收口”的执行顺序。
- [ ] 批准下一版带观测能力的 GUI/CLI 和 Python SDK 版本号。
- [ ] 批准受支持构建的 allowlist；不能只按“版本号更大”信任未知产物。
- [ ] 批准默认路线：全部有效实例主动双次验证，随后连续 30 天零 legacy/direct/unknown。
- [ ] 批准保守路线：无法主动确认全部低频实例时，使用最多 90 天发现窗口；结束后仍 unknown 的 token 必须停用或撤销。
- [ ] 冻结旧客户端安装包的继续分发。
- [ ] 规定从 `T0` 起新 token 不再带 legacy 私钥，并只能用于受支持客户端。

### 6.3 阶段 0 退出门槛

- [ ] 预迁移清册快照中的 100% 条目都有负责人或明确撤销决定。
- [ ] 共享 token 清单完整。
- [ ] 受支持版本、观察窗口、异常期限和最终审批人均已确定。
- [ ] 私有迁移矩阵已创建且不含秘密。

## 7. 阶段 1：观测合同与离线测试

### 7.1 客户端最小合同

新客户端在 bootstrap 和 rotate 请求中只增加以下非敏感字段：

```text
client_product: desktop-gui | cli | python-sdk
client_version: 非 dev 的构建版本；批准状态由后续发布 allowlist 判定
protocol_version: 2
wireguard_public_key: 现有字段
```

- [x] 定义并测试 `client_product` 枚举。
- [ ] 定义并测试版本格式、长度上限和批准版本策略。
- [x] release 构建脚本拒绝缺失或 `dev` 版本，并在可执行架构匹配宿主时校验产物身份。
- [x] GUI bundled CLI 在构建时固化 GUI surface/version。
- [x] Python SDK bundled CLI 在构建时固化 SDK surface/version。
- [x] 独立 CLI 默认报告自身 surface，并由发布参数注入 version。
- [x] 旧请求缺少新增字段时，在观察期显式分类为 legacy/unknown，不能默认成新版。

### 7.2 Hub 派生字段

以下字段必须由 Hub 根据可信运行态事实派生，不能接受客户端自报：

```text
ingress: trusted_proxy | compat
key_mode: client_generated | server_legacy
private_key_returned: true | false
migration_class: secure_bootstrap | legacy | unknown
```

- [x] 只有专用 loopback listener 的 handler 能派生 `trusted_proxy`。
- [x] 公网兼容 listener 的 handler 固定派生 `compat`；生产切换完成前不把它猜成 direct HTTP。
- [x] 不信任公网客户端提供的 `X-Forwarded-*` 或 ingress header。
- [x] `key_mode` 从已验证的公钥是否存在派生，不记录公钥本体。
- [x] `private_key_returned` 从实际响应派生。
- [x] 只有已知产品、非 dev 构建版本、protocol 2、可信代理入口、有效公钥、成功且无私钥响应才能分类为 `secure_bootstrap`；批准 allowlist 留给 `compliant` 判定。
- [x] Hub 不得仅凭 `secure_bootstrap` 把安装实例标记为 `compliant`；当前 observation-only readiness 固定为 false。
- [ ] 有效 token 的失败、409、legacy 和 direct 尝试也必须更新阻断时间。
- [ ] rotate 请求沿用同一产品、版本、协议和 ingress 判定。

### 7.3 入口边界

推荐使用两个物理分离的 listener：

```text
Caddy HTTPS -> 专用 loopback-only secure listener
公网迁移兼容 -> 独立 legacy listener
```

- [x] 确认专用 loopback 端口不与现有控制面或数据面 listener 冲突。
- [x] Caddy 只反代到 loopback secure listener。
- [x] legacy listener 保持现有兼容行为，直至全局 Go 门槛通过。
- [x] 两个 listener 复用同一显式 handler/service，不复制 bootstrap 业务逻辑。
- [x] 外部提供的转发头不能改变 listener 派生的 ingress。

### 7.4 迁移观测投影与 campaign 成员表

原始 `audit_events` 有保留时间和行数上限，不能作为 readiness 的唯一依据。现有 Hub admin SQLite 应保存两个职责分离、由 readiness 显式关联的数据集。

运行观测投影每 token 一行，只由请求事件自动更新：

```text
token_id
first_seen_at / last_seen_at
last_product / last_version / last_protocol
last_ingress / last_key_mode / last_result
first_secure_bootstrap_at / last_secure_bootstrap_at
last_legacy_at / last_direct_http_at
last_unknown_at / last_failure_at
secure_bootstrap_count / legacy_count / direct_http_count / unknown_count
last_observation_write_at
```

campaign 成员表每 token 一行，保存非敏感的生命周期和人工端到端验收事实；它不能直接手工设置 `compliant=true`：

```text
campaign_id / token_id
member_since / member_status
disposition_at / disposition_reason_code
first_e2e_verified_at / second_e2e_verified_at
e2e_evidence_ref
last_reconciled_at
```

- [x] 复用现有稳定 token ID 概念，不创建第二套人工 ID。
- [x] 运行观测投影由成功 bootstrap 审计事件自动更新，禁止管理员手工把 token 改成 compliant。
- [ ] campaign 成员状态只能通过已审计的创建、停用、到期、撤销或端到端验收动作更新。
- [ ] 端到端证据引用只指向脱敏记录，不保存用户身份、token 或密钥。
- [ ] readiness 由运行观测投影、campaign 成员表和 `TokenStore` reconciliation 共同派生。
- [ ] token 从 `tokens.yaml` 删除前必须先有 campaign disposition；否则 readiness 立即变为 unknown。
- [x] 重复 bootstrap 通过主键 upsert 保持每 token 一行。
- [ ] SQLite 重启后投影不丢失。
- [x] 乱序事件不能倒退最近观测字段；历史分类计数和时间只增不退。
- [x] 任一审计/观测写入失败将 observation-only readiness 标记为不健康并保持 NO-GO。
- [ ] readiness 只有在 `secure_bootstrap` 双次证据、端到端双次证据、生命周期处置和观察窗口全部满足时才返回 compliant。
- [x] observation-only readiness 报告列出 `ready=false`、当前有效 token 分母、逐 token 非敏感版本、分类计数和 blocker；不返回秘密。

当前本地实现只完成自动观测投影与 `observation_only` 报告。campaign 成员、批准版本 allowlist、端到端证据和静默窗口尚未实现，因此这一步不会也不能产生 `compliant` 或 `ready=true`。

### 7.5 隐私和保留

- [x] 审计及投影不保存 token 明文、WireGuard 公钥、私钥或完整请求体。
- [x] 迁移投影不新增完整 IP、设备 ID、硬件、主机名或用户名。
- [x] 使用合成秘密样本验证日志、SQLite 和报告不存在秘密泄漏。
- [ ] 原始审计沿用现有受控保留策略。
- [ ] token 停用后，逐 token 迁移投影最多保留 90 天。
- [ ] campaign 完成并经过回滚期后，只保留匿名聚合终验报告。

### 7.6 离线测试清单

- [x] product/version/protocol 的正常与非法输入。
- [ ] `dev`、缺版本、超长字段和未批准预发布版本。
- [x] secure_bootstrap/legacy/unknown 完整分类矩阵。
- [x] 伪造 `X-Forwarded-*` 不能被分类为 HTTPS。
- [ ] secure_bootstrap 后出现一次 legacy/direct 会重置静默期。
- [ ] 新增、停用、到期、撤销 token 的 campaign 分母 reconciliation。
- [x] 审计 JSON 和 readiness 报告不含 token、公钥或私钥。
- [ ] 高频 bootstrap、进程重启和乱序事件下投影正确。
- [ ] 新协议响应不含私钥；legacy 响应只在 `observe` 模式存在。
- [ ] `enforce` 模式下旧请求返回明确的 `426 upgrade_required`，不返回配置。
- [ ] 使用全 unknown 的合成 campaign 验证 readiness 必须为 false。

### 7.7 阶段 1 退出门槛

- [x] 所有离线回归测试通过。
- [x] 公开 API/管理合同如有变化，生成文件已按项目既有流程更新并通过 drift 检查。
- [x] 聚焦测试通过。
- [x] 项目本地完整门禁通过。
- [x] diff 已检查，不含 token、密钥、私有路径或意外本地资产。

## 8. 阶段 2：Hub 观测部署

- [x] 生产变更前保存 root-only 配置、数据库和服务状态备份。
- [x] 使用合成 token 在隔离环境完成 secure_bootstrap、legacy 和伪造 header 集成测试；`426` 只属于阶段 7 enforce 验收。
- [x] 验证 Caddy HTTPS -> secure listener 分类正确。
- [x] 验证直连 legacy listener 分类正确。
- [x] 验证旧客户端 fixture 在 `observe` 模式仍可用且被标记为 legacy。
- [ ] 验证新客户端使用临时目录生成本地密钥，响应和缓存均无服务端私钥。
- [ ] 验证 bootstrap、rotate、SQLite 投影和只读 readiness 报告端到端贯通。
- [x] 部署 Hub `observe` 模式；不拒绝现有旧客户端。
- [ ] 从 Hub 观测部署开始到正式 `T0` 之间冻结普通新 token 发放；只允许已登记的合成/canary token。
- [ ] 确认现有健康检查已改用 HTTPS 或本机 secure listener，不再依赖公网 legacy listener。
- [ ] 连续观察 24 小时，无审计空洞、分类错误或错误率异常。
- [x] 记录可信观测候选起点，但在可审计客户端产物及三类 canary 就绪前不设置 campaign `T0`。

### 阶段 2 退出门槛

- [ ] 观测链连续可靠。
- [ ] secure_bootstrap 与 legacy canary 均被准确分类。
- [ ] 旧客户端兼容行为未漂移。
- [ ] readiness 报告仍为 `ready=false`，且 blocker 符合真实清册。

## 9. 阶段 3：可审计客户端发布

### 9.1 通用发布门禁

- [ ] GUI、独立 CLI、Python SDK 的目标版本已批准。
- [ ] 版本来自各自现有产品元数据或显式发布参数，不复制到第二份运行时配置。
- [ ] 所有 release 构建的 `version` 输出不是 `dev`。
- [ ] 发布清单记录文件名、surface、版本、架构和 SHA256。
- [ ] 安装包与 token 不通过同一消息或渠道发送。
- [ ] 用户说明禁止粘贴 token、`client.key`、完整配置或未脱敏日志。
- [ ] 常规升级说明不要求执行 `logout`。

### 9.2 GUI

- [ ] Windows x64 GUI 构建及 bundled CLI 版本一致。
- [ ] 需要支持的其他架构有批准产物或明确改用批准 CLI。
- [ ] 完成停止、安装、启动、连接、状态和再次连接的真实验证。

### 9.3 独立 CLI

- [ ] Windows amd64 构建通过。
- [ ] Windows arm64 构建通过。
- [ ] 其他实际使用平台逐一确认；未使用的平台不虚构支持。
- [ ] `version --json`、`start --json`、`status --json` 通过。

### 9.4 Python SDK

- [ ] wheel/发放包包含批准版本的 bundled CLI。
- [ ] 使用真实任务所用解释器/venv 验证 `Client().version()`。
- [ ] connect/status 通过。
- [ ] 无人值守任务至少覆盖两个正常业务周期。
- [ ] 一台机器上的多个实际 venv 分别验收。

### 9.5 内部 canary

- [ ] GUI canary 完成双重证据。
- [ ] 独立 CLI canary 完成双重证据。
- [ ] Python SDK canary 完成双重证据。
- [ ] 每个 canary 都有客户端版本/SHA256、服务端 secure_bootstrap 和端到端成功证据。
- [ ] 连续观察 24–48 小时，无 P0/P1 问题。

### 9.6 阶段 3 退出门槛与 T0

- [ ] 三类客户端的批准发布产物和 SHA256 清单完整。
- [ ] 三类 canary 的客户端、Hub 和端到端证据全部通过。
- [ ] 观测链从候选起点至今无空洞。
- [ ] 没有未关闭的 P0/P1 发布或迁移问题。
- [ ] 将预迁移清册快照与当前 `TokenStore` reconciliation 后固化为正式 campaign 分母。
- [ ] 设置并记录 campaign `T0`；此前历史不得计入新版证明。
- [ ] 从 `T0` 起恢复新 token 发放时，只允许批准客户端和新协议，不再配置 legacy 私钥。

## 10. 阶段 4：真实用户分批升级

### 10.1 每个安装实例的模板

为私有迁移矩阵中的每一行复制下面的检查项：

- [ ] 负责人和安装实例已确认。
- [ ] token 只对应这一安装实例；否则先拆分。
- [ ] 客户端 surface、OS、架构和实际运行环境已确认。
- [ ] 使用批准下载渠道和 SHA256。
- [ ] 客户端已停止/断开后完成升级。
- [ ] 本机版本符合批准 allowlist。
- [ ] 未配置非批准的远程 HTTP API override。
- [ ] 执行了真实 `start/connect`，而不只是 `status`。
- [ ] 服务端记录 `secure_bootstrap`：HTTPS + client-generated key + no private key response。
- [ ] WireGuard 握手和代理端到端成功。
- [ ] 首次端到端验收已写入 campaign 成员表，并引用脱敏证据。
- [ ] 至少相隔 7 天取得第二次 `secure_bootstrap` 和端到端成功证据。
- [ ] 后续没有该实例的 legacy/direct/unknown 事件。
- [ ] 最终状态已更新为 `candidate` 或 `compliant`，并附脱敏证据位置。

### 10.2 批次

- [ ] 第一批：内部管理和测试实例。
- [ ] 第二批：最近 7 天活跃实例，先一半、观察 24 小时、再完成其余。
- [ ] 第三批：8–30 天活跃实例。
- [ ] 第四批：31–当前可靠历史窗口内的低频实例。
- [ ] 第五批：无人值守 SDK、管理残留和测试残留。
- [ ] 第六批：历史未观察到但仍启用且未过期的实例。

### 10.3 沟通和硬截止

- [ ] D-7：发送安全升级通知、批准版本、耗时、下载渠道、SHA256和截止时间。
- [ ] D-3：只提醒尚未完成的实例。
- [ ] D-1：最终提醒，明确到期隔离和重新激活流程。
- [ ] D-day：逐人确认，不在群聊公开用户清单。
- [ ] D+1：发送完成确认或安全重新激活说明。
- [ ] 从 T0 起最多 21 天完成主动升级追踪；例外必须有责任人和不超过 72 小时的到期时间。

### 10.4 休眠、无主和共享 token

- [ ] 已过期、用途不明、负责人离开或设备报废的 token 撤销，不做迁移。
- [ ] 仍启用但无观测的 token 主动联系、唤醒并验证，不能默认新版。
- [ ] 到硬截止仍 unknown 的 token 停用或撤销。
- [ ] 停用 token 后显式撤销对应旧 WireGuard peer/session；不能假定停用会终止已运行隧道。
- [ ] 返回用户先安装批准客户端，再通过安全渠道换发新 token。
- [ ] 所有共享 token 已拆分并撤销旧 token。

### 10.5 保守发现子阶段

本子阶段只用于继续发现低频安装实例，不代表阶段 4 已通过，也不允许进入 readiness 或生产收口。

- [ ] 无法在主动追踪期完成全量确认时，保持阶段 4 为进行中，并开始最多 90 天发现窗口。
- [ ] 发现到的低频实例回到逐实例升级模板完成验证。
- [ ] 90 天结束后仍 unknown 的 token 已停用或撤销。
- [ ] 被动观察本身不替代 secure_bootstrap 与端到端双重证据。
- [ ] 所有 unknown 处置完成后，才评审阶段 4 退出门槛。

### 阶段 4 退出门槛

- [ ] 100% 允许继续使用的安装实例都有双重证据。
- [ ] 所有 unknown 已验证、停用或撤销。
- [ ] 所有共享 token 已拆分。
- [ ] 临时例外清单为空。

## 11. 阶段 5：静默观察与 readiness

- [ ] 从最后一个有效实例取得完整证据后开始 30 天静默窗口。
- [ ] 建立每日自动/半自动 readiness 摘要，至少覆盖 legacy、direct HTTP、unknown、私钥响应和观测写入连续性。
- [ ] 每日摘要同时覆盖 HTTPS bootstrap/rotate 成功率、延迟、5xx 和 `wireguard_peer_apply_failed`。
- [ ] 每日摘要同时覆盖活跃 peer、allowed IP、握手、代理流量和 Android 出口回归门禁；Android 本身不在本阶段改造。
- [ ] 每个自然日将结果和脱敏证据位置追加到“观察窗口记录”，不能用一个 checkbox 代表连续 30 天。
- [ ] 无效 token 的公网扫描单独统计，不与有效客户端迁移事件混淆。
- [ ] 任一有效 token 出现 legacy/direct/unknown 时重置静默窗口并回到用户追踪阶段。
- [ ] 任一观测空洞从恢复时间重新开始静默窗口。

### 11.1 观察窗口记录

24 小时、7 天、30 天和保守 90 天窗口都使用本表。发生重置时必须填写原因和新的起点；不得覆盖旧记录。

| 日期 | 窗口类型 | 当前起点 | Campaign 分母 | Secure bootstrap | Legacy | Direct HTTP | Unknown | 观测空洞 | E2E/数据面 | 脱敏证据 | 决策或重置原因 |
|---|---|---|---:|---:|---:|---:|---:|---|---|---|---|
| 待填写 | 30 天静默 | 未开始 | 私有矩阵 | 待观测 | 待观测 | 待观测 | 待观测 | 待观测 | 待观测 | 待填写 | NO-GO |

### 提前收口门槛

- [ ] 所有有效安装实例均已双次验证。
- [ ] 最后一次 secure_bootstrap 和端到端证据都在当前 30 天窗口内。
- [ ] 连续 30 天没有有效 token 的 legacy/direct/unknown。
- [ ] readiness 报告返回 `ready=true` 且 blocker 列表为空。

## 12. 阶段 6：切换预演与生产备份

- [ ] 使用合成 token 演练 `observe -> enforce -> closed` 和完整恢复。
- [ ] 恢复演练不把真实私钥复制到测试环境。
- [ ] 记录目标 RTO：单批回滚不超过 15 分钟，全量恢复不超过 30 分钟。
- [ ] 配置 RPO 为 0；每次变更都有精确前置快照。
- [ ] 确认两个独立管理会话和供应商救援入口可用。
- [ ] 记录 zhhub、Caddy、防火墙、WireGuard 和 Android 出口基线。
- [ ] 保存 root-only 的 token 配置、Hub 配置、Caddy 配置、防火墙规则、WireGuard peer 映射和 admin SQLite 一致性快照。
- [ ] 生成备份清单和 SHA256；目录权限 `0700`，敏感文件权限 `0600`。
- [ ] 进入切换窗口后冻结 token、客户端和 Hub 的无关变更。

### 阶段 6 退出门槛

- [ ] 恢复演练在 RTO 内完成。
- [ ] 所有备份均可读取且权限正确。
- [ ] Go/No-Go 检查表全部满足。
- [ ] 迁移负责人、Hub 运维负责人和安全复核人共同批准进入生产收口。

## 13. 阶段 7：enforce 与 legacy 私钥清理

### 13.1 enforce

- [ ] 将 Hub 从 `observe` 切到 `enforce`。
- [ ] 验证旧协议、缺公钥和 direct HTTP 的有效请求返回 `426 upgrade_required`。
- [ ] 验证 426 响应不含客户端配置或私钥。
- [ ] 验证所有 compliant canary 仍能 login/start/status/rotate。
- [ ] 观察 24–48 小时，无遗漏旧客户端和新故障。

### 13.2 私钥分批清理

- [ ] 选择一个已验证的低风险 canary token。
- [ ] 只删除本批条目的 `wireguard.private_key`，不顺带修改地址、出口或路由。
- [ ] 校验 YAML，并确认 diff 只包含预期字段。
- [ ] 原子替换配置并按既有程序受控重载/重启 Hub。
- [ ] 验证本地健康、HTTPS health、bootstrap、rotate、WG peer、allowed IP、握手和代理出口。
- [ ] canary 稳定 24 小时。
- [ ] 清理约 25% 的已验证 token，并观察 24–48 小时。
- [ ] 清理其余已验证和已撤销 token，并观察 24–48 小时。
- [ ] 确认生产 token 配置中的 legacy 私钥字段计数为 0，不输出历史值。
- [ ] 全量无私钥状态浸泡 7 天。

### 阶段 7 退出门槛

- [ ] legacy 私钥字段为 0。
- [ ] 新协议私钥响应为 0。
- [ ] 没有有效客户端故障或 legacy 事件。
- [ ] HTTPS、WireGuard、代理和 Android 出口连续 7 天正常。

## 14. 阶段 8：关闭公网 legacy listener

- [ ] 确认所有外部健康检查和运维探测已改走 HTTPS。
- [ ] 记录切换前精确的 IPv4/IPv6 防火墙规则。
- [ ] 只关闭公网 legacy listener 对应的 IPv4/IPv6 放行，不改其他端口。
- [ ] 从至少两个外部网络验证 HTTPS health/bootstrap/admin 正常。
- [ ] 从外部验证 legacy HTTP listener 的 IPv4/IPv6 均不可达。
- [ ] 验证 Caddy loopback secure listener 正常。
- [ ] 观察 72 小时；需要回滚时只恢复精确规则。
- [ ] 停止公网 legacy listener，保留 loopback secure listener。
- [ ] 验证最终监听面只包含预期的本机安全入口。
- [ ] 再观察 7 天，无客户端回归。

### 阶段 8 退出门槛

- [ ] 公网 legacy HTTP 在 IPv4/IPv6 均不可达。
- [ ] Caddy HTTPS bootstrap/rotate/admin 正常。
- [ ] 没有回滚和未知客户端报告。
- [ ] listener 与防火墙状态符合目标边界。

## 15. 阶段 9：最终退役和证据归档

- [ ] 端口收口后连续观察 30 天。
- [ ] 移除 legacy 私钥响应代码和不再使用的兼容字段。
- [ ] release 客户端拒绝远程 HTTP；只允许明确的本地测试边界。
- [ ] 删除过期的 legacy 配置和无主 peer。
- [ ] 经单独批准销毁含 legacy 私钥的备份。
- [ ] 保留脱敏的 24 小时、7 天和 30 天终验报告。
- [ ] campaign 逐 token 数据按保留策略删除，只保留匿名聚合结论。
- [ ] 更新架构文档、安全 TODO 和运维 runbook，使其反映真实已部署状态。
- [ ] 最终运行安全扫描、项目本地完整门禁和公开树审计。
- [ ] 最终检查 `git status`，确认没有本地资产、秘密或无关改动进入交付。
- [ ] 将本计划状态改为 `COMPLETE`，记录批准人和完成日期。

## 16. 暂停和回滚触发器

出现以下任一情况，立即停止当前批次，不得继续扩批：

- 新客户端出现 legacy、direct HTTP 或 unknown 事件。
- 任一成功响应包含客户端私钥。
- 任一 token、私钥或完整配置泄漏。
- 同一批出现两个以上或超过 5% 的重复 login/start 失败。
- 出现未解决的 `wireguard_peer_apply_failed`。
- Caddy/Hub 两个外部探测点连续失败。
- WireGuard allowed IP 冲突或正常活跃 peer 集体失去握手。
- Android 出口回落到 Hub 出口。
- 审计写入失败、readiness 投影不一致或出现无法解释的观测空洞。

> 上述项目是触发条件清单，不是待完成任务；发生任一项时，应在执行记录中登记事故并启动对应回滚。

### 回滚原则

- [ ] 每个阶段已有精确、已演练的回滚步骤。
- [ ] 私钥批次只恢复仍授权且重新核验的受影响 token，不整体覆盖 token 配置；撤销、禁用、过期凭证及被替换公钥不得因回滚复活。
- [ ] WireGuard 只恢复受影响 peer 映射，不整体覆盖运行态 `wg0`。
- [ ] 设备授权执行边界按 Steelman P4 启用后，旧版服务恢复也必须经过该边界核验最新授权/撤销记录，不能直接 `wg set` 绕过。
- [ ] listener 和防火墙按正确依赖顺序恢复。
- [ ] HTTPS 故障优先修复 Caddy/DNS/证书，不默认长期恢复明文入口。
- [ ] 临时恢复公网 HTTP 必须限制来源、设置自动到期，并重新开始迁移静默窗口。
- [ ] 优先升级或换发 token；恢复服务端 legacy 私钥只作为仍授权且重新核验对象的最后、有期限应急措施，不能恢复已撤销权限。

## 17. 最终 Go/No-Go 检查表

以下项目必须全部勾选，才能执行生产收口：

- [ ] campaign 分母 100% 已处置。
- [ ] 所有继续有效的安装实例都有批准版本和 SHA256 证据。
- [ ] 每个有效实例都有至少两次、间隔不少于 7 天的 secure_bootstrap 和端到端证据。
- [ ] compliant 覆盖率为 100%。
- [ ] legacy、direct HTTP、unknown 和临时例外均为 0。
- [ ] 所有共享 token 已拆分。
- [ ] 所有无主、休眠未知和拒绝升级 token 已停用或撤销。
- [ ] 连续 30 天没有有效 token 的 legacy/direct/unknown。
- [ ] 同一 30 天内没有观测空洞。
- [ ] 私钥响应为 0。
- [ ] 没有未关闭的 P0/P1 迁移事故。
- [ ] 恢复演练通过且在 RTO 内。
- [ ] readiness 报告返回 `ready=true`，blocker 列表为空。
- [ ] 迁移负责人、Hub 运维负责人和安全复核人共同签字批准。

如果有任意一项未勾选，结论必须保持 **NO-GO**。

## 18. 执行记录

每完成一个有副作用的切片，追加一行；证据路径必须脱敏且不得指向公开仓库中的秘密文件。

| 日期/时间 | 阶段 | 动作 | 验证结果 | 脱敏证据位置 | 操作者/复核者 | 决策 |
|---|---|---|---|---|---|---|
| 2026-08-27 | 基线审计 | 确认当前遥测不足以证明客户端代际和入口 | `ready=false` | 本计划“当前基线” | Codex / 待人工复核 | NO-GO |
| 2026-08-28 | 文档准备 | 落盘长期清单、文档入口和工作记录 | 链接/隐私/格式检查通过 | `docs/90-history/worklogs/2026-08-28-client-security-migration-plan.md` | Codex / 只读复核 | NO-GO |
| 2026-08-28 | 阶段 2 授权 | 用户授权进入生产阶段；范围限定为可逆 observation-only canary | 不含 enforce、私钥清理、防火墙或安卓改动 | 本计划“阶段 2” | 用户 / Codex | GO（仅观测） |
| 2026-08-28 08:56 JST | 阶段 2 部署 | 上线双 listener Hub 并将 Caddy 客户端 upstream 切到 `18079` | 合成 canary、双 listener、HTTPS、SQLite、readiness 与 Android 出口验证通过 | 机器本地 `.local/zongheng-vpn/migration/phase2-observation-20260828/` | Codex / 两路只读复核 | GO（24h 观测） |

## 19. 进度摘要

每次实施后更新，不要从勾选框手工猜百分比。

| 项目 | 当前值 |
|---|---|
| Campaign T0 | 未设置 |
| 当前模式 | production observation-only；双 listener 和 Caddy `18079` 已上线，尚未建立 campaign |
| 有效安装实例总数 | 仅记录于私有迁移矩阵 |
| Compliant | 未知 |
| Legacy | 未知 |
| Unknown | 未知 |
| Disabled/Revoked | 未知 |
| 连续零 legacy/direct/unknown 天数 | 0 |
| Readiness | `false` |
| 当前 blocker | 客户端版本未批准/发布；campaign、端到端证据和逐实例清册未建立；最终收口保持 NO-GO |


## 2026-10-07 Steelman 接线补充

隔离开发分支已新增真实 CLI 代理控制、Hub v2 签名 API/后台对账、reverse 双端 mTLS 与本地发行门禁，见[运行时接线](../30-implementation/steelman-runtime-integration.md)。这些代码和本地测试未导入 legacy token/安装实例，未更改 campaign 分母、T0、30天窗口、readiness 或生产入口；当前观测不能自动认领为新版设备授权证明。

第二波补齐 legacy listener 来源伪造防护、读取预算、管理台真实状态和公钥输入检查，以及离线恢复/更新 metadata 的审阅能力；见[第二波记录](../90-history/worklogs/2026-10-07-zhvpn-security-boundaries.md)。恢复 planner 不自证 latest checkpoint，也不执行恢复；纯 update verifier 不持久提交 watermark 或安装。没有实际客户迁移、现场撤权/恢复和旧入口退役证据，NO-GO、campaign 窗口和分母保持原定义。

第三波补齐指定昂贵 HTTP 入口的来源准入与本地 WG/SSH 子进程预算，明确未启动的失败才补偿 lease，未知派发结果不自动重试。Admin 显示服务端 unknown 并跨页面重载阻断；该标记尚无跨 Hub 重启持久性，来源 IP 也没有升级为设备身份。此切片不关闭 campaign、受控导入或生产授权切换门禁，见[执行边界](../30-implementation/legacy-control-process-budget.md)。

第四波本地离线update CLI已保存明确批准Policy/Previous和累计floor；缺失登记/提交未知拒绝继续，旧候选不能重放覆盖较新水位。它尚未接入实际安装或外部防回滚锚，同存储owner恢复旧快照仍可回滚。本片不替代现有campaign分母/窗口、现场授权撤销或生产迁移。[更新状态边界](../30-implementation/trusted-update-state.md)。

第六波[provisional 清册](../30-implementation/migration-inventory.md)在隔离开发分支补齐人工批准 raw SHA 的固定 baseline、source 全生命周期、追加 unknown、单调负事实及持久 observer run/gap。它不等于正式 campaign：owner/installation 引用仅是声明，跨历史短ID/真实安装保管链未完成；逐负类别时间、rotate 全入口、T0 后连续30天窗口仍未闭合。旧异常 metadata 回填补一次 unknown 负事实、保留原 secure 记账，不能合计为去重请求总数，也无法恢复此前被覆盖的 provenance。T0、readiness、生产事实源和本计划的生产勾选保持原定义。
