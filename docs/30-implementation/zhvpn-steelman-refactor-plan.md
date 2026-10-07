# zhvpn Steelman 重构计划

2026-10-07 16:39 JST：前八波代码及兼容部署记录已快进并推送GitHub main，本地/远端仅保留main；原dirty工作区和旧checkout转detached保留，见[发布与分支清理](../90-history/worklogs/2026-10-07-main-publish-branch-cleanup.md)。发布Git不代表完成签名发行、v2/手机迁移或G01–G05终验。

> 创建：2026-10-06 JST。状态：IN_PROGRESS；第八波源码 `3b5a2c5` 已本地提交，Windows v13统一门禁/Linux v3实际产品验收exit0，十目标clean开发构建/hash一致，见第八波worklog与收据。16:14–16:21 JST另完成Hub/reverse与单Windows CLI兼容canary部署/真实连通；v2授权、手机和正式发行仍未切换。源码、实机、生产实施分别记录。
> 基线：[2026-10-05 多维审计](../90-history/worklogs/2026-10-05-zhvpn-project-audit.md)。49/100 是该日的工程成熟度评估，不能作为本日运行状态或后续验收结果。

## 1. Steelman 的完成定义

目标是把当前项目做成能够持续维护和发行的产品：身份可信、状态明确、权限真正生效、异常可恢复、变更可验证、产物可追溯。保留根 Go module、嵌入 sing-box、CLI 唯一客户端控制面和现有 Tauri/Svelte；模块拆分服务于明确的状态归属与故障边界。

- [ ] **G01 安全**：生产授权与 reverse 链路均有受验证的认证加密；客户端私钥不出设备；撤销覆盖已建立的数据面权限与 reverse session。
- [ ] **G02 生命周期**：PID 复用不误杀、并发不产生不可管理实例、代理恢复不覆盖无归属状态、崩溃后能恢复或明确提示人工处理。
- [ ] **G03 真实状态**：实例身份、代理协议、隧道、出口健康分别可观察；端口监听、HTTP 200、CONNECT 建立不冒充业务成功。
- [ ] **G04 可重复交付**：干净检出可运行本地门禁并构建；发布有版本、提交、工具链、签名、hash、兼容矩阵与恢复步骤。
- [ ] **G05 证据闭环**：F1–F14 全部有处置和有效验证；九维重新评估目标为综合 ≥85/100、每维 ≥8/10。分数不能覆盖任何未关闭的 P0/P1 或未完成的生产安全迁移。

checkbox 使用规则：完成一个任务后记录提交、命令、平台、结果和脱敏证据位置，才能把对应 `[ ]` 改成 `[x]`。阶段门禁另有 checkbox；源码完成、本地通过、实机通过、生产启用分别记录，不能相互代替。

## 2. 范围与不变量

范围包括 Windows/macOS CLI 与桌面 GUI、共享代理引擎、Python SDK、Hub 授权、reverse 出口安全及发行运维。客户 Android App 保持 Slice 0 的既有范围；共用 API 变化须做兼容评估，本计划不把其正式发行或内部 Alpha 视为已承诺交付。

以下约束贯穿所有阶段：

- 同一 OS 用户与规范化 `ZHVPN_HOME` 管一个引擎；不同 home 可以独立运行。系统代理是用户级共享资源，其租约按 Windows SID/macOS UID 归属，不能按 home 分出多个互相覆盖的所有者。
- GUI/SDK 调用 CLI 的同一套生命周期规则；系统代理副作用逐步归 CLI 管理，GUI 不再另持一套恢复状态。
- 客户 peer、RDP、管理员、手机控制面及未知历史 peer 分开登记。reconciler 只变更明确归自己管理的客户 peer；未知资产只报告，不自动删除；不得整体覆盖运行中的 `wg0`。
- 正常目标流量保持手机出口。无可用手机时返回可定位故障，不以 Hub VPS 作为出口兜底；保留双会话、限额、idle reaper 和已部署的 idle-preempt 行为。
- 本地代理、系统代理、TUN 的覆盖范围及 DNS 行为分别说明，不能把“设置系统代理”承诺为所有应用流量均已接管。
- Windows 提权保持原用户会话/凭据边界；不能以切换另一 OS 用户的方式静默获取不同 home 的配置。
- 生产新切换遵守 AGENTS.md 的具体操作确认要求；本地实现、离线测试与可审查产物先准备好。此前 idle-preempt 部署授权不扩展为所有安全迁移的部署授权。
- 用户已取消 Jetstar；验收默认使用受控探测和合成服务，不依赖该网站。

## 3. 目标边界与事实源

| 边界 | 唯一责任 | 维护源与约束 |
| --- | --- | --- |
| CLI 入口 | 参数、JSON 输出、兼容适配 | 不自行拼装第二套业务状态 |
| 客户端 runtime | 引擎身份、状态机、操作、锁、恢复、代理租约 | 版本化状态/journal；实现落在 CLI internal runtime 边界 |
| shared/proxy | sing-box 配置与引擎数据面适配 | 不负责 GUI 状态、系统代理租约或 token 生命周期 |
| shared/systemproxy | Windows/macOS 的 OS 读写适配 | 新增该边界须有实际跨入口调用；所有权/journal 由 runtime 控制 |
| GUI / Python SDK | 展示、交互、语言封装 | 消费版本化 CLI 协议，不直接管理引擎或重新定义超时与成功 |
| Hub auth | 激活、设备身份、授权、租约、换钥、撤销 | 复用现有 SQLite 能力建立版本化持久模型；不引入第二套数据库服务 |
| peer 执行者 | 串行执行受授权 WG 变更、核对 generation | durable outbox + 受控单一执行边界；不能仅凭 leader lease 假定旧进程已停止 |
| reverse | 已登记出口身份、认证传输、调度和转发 | 设备级 credential，不以同一共享 token 代表所有出口 |
| 本地发布门禁 | 检查、扫描、构建、签名、发布清单 | 一个可执行入口，发行脚本必须调用；保持不增加 hosted CI 的现有政策 |

目标授权事实源为持久数据库中的设备/凭证/授权关系；`tokens.yaml` 通过有版本的导入迁移退出可变授权事实源，后续只承担有明确用途的非秘密配置或兼容投影。迁移前仍以现有实现为当前状态，不提前改文档宣称数据库已接管。禁止 YAML 与数据库双向独立写入同一事实。

Admin API 沿用当前 OpenAPI/oapi-codegen/sqlc。客户端 HTTP API 建立一份版本化 OpenAPI 维护源；CLI JSON/IPC 采用一份公开 DTO/schema 维护源，确定性导出消费者需要的类型或校验信息。生成流程、消费者测试与兼容版本一起维护，不因重构引入另一套无收益的合同平台。

## 4. 阶段顺序

| 阶段 | 结果 | 依赖 | 对应审计 |
| --- | --- | --- | --- |
| P0 | 可恢复的代码/运行/资产基线 | 无 | 全部，文档漂移 |
| P1 | 协议、状态、持久化与迁移合同 | P0 | F3、F7–F10 |
| P2 | 客户端引擎生命周期 | P1 | F6、F8–F11 |
| P3 | 系统代理与桌面恢复 | P1；集成依赖 P2 | F4、F5、F12 |
| P4 | Hub 身份、持久授权与有效撤销 | P1 | F2、F3、F7、F10、F13 |
| P5 | reverse 认证加密与手机迁移 | P0/P1；设备身份依赖 P4 | F1 |
| P6 | 模块整理、依赖、日志与性能 | 相应行为边界稳定后逐块推进 | F11、维护性、扫描 |
| P7 | 本地自动门禁与可追踪发行 | 门禁骨架从 P1 开始；最终依赖 P2–P6 | F14、签名与升级 |
| P8 | canary、生产切换与兼容退役 | P7，及既有安全迁移门禁 | 全部运行效果 |
| P9 | 重新审计与 Steelman 终验 | P8 观察完成 | 九维评分 |

P1 后可以并行推进 P2/P3、P4、P5 的离线实现；每个切片仍按自身依赖通过门禁。代码切片可分别合入，所有生产变化不得合成一次大切换。完整工期受真实客户端清册、签名条件、平台实机及迁移观察窗口制约，不能承诺用几天编码完成全部生产收口。

## 5. 可执行 checkbox 清单

### P0 — 冻结基线与资产边界

- [x] **P0.1** 对账主工作区、GitHub main、idle-preempt 分支、未推 observer 和既有 dirty 文件；记录每项归属与处理办法，在合适的独立工作树建立实施基点，保留全部无关工作。证据：隔离合流基线 `3c88880`、原工作区 79 个 dirty 文件的本地 hash 清册，见 [10-06 首切片 worklog](../90-history/worklogs/2026-10-06-zhvpn-runtime-safety-slice.md)。
- [ ] **P0.2** 保存当前 Windows/macOS 客户端、SDK、Hub、手机的版本/协议/hash/启动路径能力矩阵；生产字段只读复核，10-05 的运行记录只能作为历史参考。
- [ ] **P0.3** 备份并比较 WG 运行态、源配置与 token/设备映射；建立客户受管 peer、受保护 peer、未知 peer 的清单，使用稳定非秘密标识保存公开证据。
- [ ] **P0.4** 把 F1–F14 的复现/触发条件纳入回归矩阵；首次执行记录现有失败，不隐藏 race/计时敏感测试或平台未验收。
- [ ] **P0.5** 固定支持平台、系统代理/TUN/DNS 覆盖语义、正常联网/并发/重连的基准场景；定义退出恢复、授权撤销、资源和性能预算。
- [ ] **P0.G** 基点和资产可恢复、受保护对象明确、无旧结论冒充本日事实，才进入 P1。

### P1 — 先固定协议与状态

- [x] **P1.1** 定义引擎身份：随机 instance ID、规范化 home、配置 generation、协议版本及可信控制通道。PID 仅为诊断字段；CLI JSON、IPC、错误码及 schema 版本有明确兼容规则。维护源、生成漂移、真实 Windows child/HMAC 与消费者负例已通过；见 10-06/10-07 worklog。跨平台运行验收仍在 P2.G。
- [ ] **P1.2** 定义 `starting/ready/degraded/stopping/stopped` 状态机；分别返回进程、代理协议、WG/隧道、IPv4/IPv6 出口健康与验证时间，明确缓存有效期和 `unknown`。
- [ ] **P1.3** 定义操作 `accepted/running/succeeded/failed/unknown`、request ID/operation ID、幂等作用域、保留期限、查询、等待与取消；明确“换 IP 已触发、网络已恢复、IP 已变化”三种结果。
- [ ] **P1.4** 固定配置/state/journal 的 schema、原子提交、generation、凭据迁移和跨版本读取；定义原始旧配置损坏、迁移中断、回退时的行为。
- [ ] **P1.5** 定义激活凭证、设备身份、credential、WG peer ownership/换钥/撤销合同与数据库迁移；客户端私钥永不进入 Hub 响应或公开持久化。
- [ ] **P1.6** 建立唯一合同生成/校验入口与 CLI/GUI/SDK/Admin 的消费者测试；早期可用旧字段适配，新增语义不能静默改变旧布尔字段含义。
- [ ] **P1.G** 全部关键转换、拒绝路径、超时结果和兼容矩阵可测试；独立复核通过后，才按切片改变行为。

### P2 — 引擎身份、并发与崩溃恢复

- [ ] **P2.1** Windows/macOS 实现具有崩溃释放语义的跨进程操作锁与实例所有权；规范化 home 的别名/大小写；操作锁不能阻塞引擎停止 IPC。
- [ ] **P2.2** 以受访问控制的本地 IPC 优先执行引擎自退出。Windows 必要强停使用持续持有且验证过的进程句柄和受管进程树；macOS 使用可信监督/子进程所有权，不能回退到“查一次 PID 再 kill”。
- [ ] **P2.3** 启动按配置准备→引擎初始化→代理监听→身份握手→ready 提交执行；故障只清理本次 generation，正常/异常退出均处理自己的状态，不覆盖新实例。
- [ ] **P2.4** 修正 status：普通 TCP listener、其他代理、配置 generation 不一致、引擎活着但出口不可用均不能显示为已验证连接；快速轮询不隐式 bootstrap 或反复公网探测。
- [ ] **P2.5** SDK 等待预算按操作统一，客户端超时后仍可查询远端操作；响应丢失和重复 rotate 请求最多触发一次，不自动新建第二次换 IP。
- [ ] **P2.6** 覆盖两平台并发 login/start/stop、PID 复用、身份变化期间 stop、逐阶段崩溃、损坏/缺失 state、机器重启和不同 home 共存。身份不能确定时返回恢复错误，禁止误杀。
- [ ] **P2.G** Windows/macOS 均有真实进程/IPC 证据，最多一个受管引擎、无不可查询孤儿操作、无误杀，才完成客户端生命周期阶段。

### P3 — 系统代理归属与桌面恢复

- [ ] **P3.1** 将系统代理租约/journal 纳入 CLI 控制面；GUI 仅发 typed commands。按 OS 用户协调全局代理，其他 home 引擎继续独立运行；定义 GUI 退出与 CLI/SDK 共享引擎的 stop 所有权。
- [ ] **P3.2** 未接管代理时 disconnect/退出不写 OS 配置；接管前保存原值与本次写入值，包括字段是否存在、类型和值，区分缺失与空值。写入后读回确认，恢复按逐字段归属执行而非盲回整份快照。
- [ ] **P3.3** 他程序修改系统代理后识别冲突、退让并提示；记录 Windows 注册表没有跨程序全局 CAS 的限制，不能承诺所有外部竞争均可无损消除。
- [ ] **P3.4** 解析、读取、写入、通知或部分恢复失败均返回真实错误；保留恢复 journal，只有恢复确认后才清理。GUI/引擎崩溃、升级和下次启动有恢复入口。
- [ ] **P3.5** 安装升级精确匹配当前安装路径、会话与受管实例；不能确认归属时延迟/拒绝升级，移除按 `zhvpn.exe` 进程名全杀。明确关闭窗口、留在托盘与真正退出的语义；第二 GUI 通过可信 IPC 激活已有实例，不能按窗口标题认领或争抢代理租约。
- [ ] **P3.6** fake OS adapter 测允许/拒绝/部分失败，Windows/macOS 受控实机分别测实际系统设置、退出/重启恢复、共存与升级；GUI 清晰显示连接中、部分健康、恢复失败和可执行处理。
- [ ] **P3.G** 未接管不改他人代理、失败保留恢复材料、共存与升级不破坏其他实例；mock 通过不能替代 OS 实测。

### P4 — Hub 身份、持久授权与撤销

- [x] **P4.1** 建立一次性激活→设备登记→设备 credential 的模型；挑战具有 nonce、用途、期限与请求绑定，设备证明私钥持有。并发消费、重放、跨用途重放、过期激活及幂等重试均受事务约束。实际 CLI/Hub/SQLite TLS、Windows race及Linux普通回归已通过；这是隔离v2入口，当前生产迁移仍在P4.2/P8。
- [ ] **P4.2** 将可变授权关系迁入现有 SQLite 能力：device/public key/address/generation/state/expiry、唯一约束、撤销记录和操作结果；完成受控导入、校验、备份和恢复，不保持 YAML/DB 双权威。保留 token→安装实例→device 的历史关联、campaign 分母、处置与观测记录，同步其对账合同；切换事实源不重设 T0 或缩短窗口，也不自动把旧 token 观测视为新设备的合规证明。
- [x] **P4.3** API 事务提交 desired state 与 durable outbox；单一受控 peer 执行者按 generation 应用/撤销，跨进程互斥并防止旧任务覆盖新撤销。响应区分已提交与已生效。Windows Job/Linux helper真实进程崩溃、跨Store竞争、迟到取消负例以及第五波真实用户态 WG/CLI 流量撤销通过；第七波真实受管proxy启动失败屏障也已通过。其他WG路径、持续撤销SLA与生产切换仍在P4.G/P8。
- [ ] **P4.4** 授权状态变化产生可重试 apply/revoke：禁用、到期、删除必须 revoke；换钥撤销旧公钥；重新启用是显式重新授权，保留历史撤销记录。A 不能认领 B 或管理 peer 的地址/公钥。reconciler 只处理已登记资产，Hub/API/WG 重启后不从旧 conf 复活撤销权限。
- [ ] **P4.5** 租约基于明确设备/会话身份，IP 作为审计信号；XFF 只由可信入口和明确代理赋值。所有入口设置请求体/字段、header/read/write/idle、并发、速率及子进程总截止时间。
- [ ] **P4.6** 演练同时换钥/禁用、重复/乱序任务、提交/执行中崩溃、同 NAT、多网络切换与备份恢复；初始撤销目标为健康 Hub p99≤5s、最长≤30s，异常明确 `revoke_pending/degraded` 并告警，不能伪报生效。
- [ ] **P4.G** 已建测试隧道确实被撤销、旧任务/重启不恢复权限，RDP/管理员/手机控制面及未知 peer 保留；线上迁移仍受既有 NO-GO 门禁约束。

### P5 — reverse 认证加密与手机能力迁移

- [ ] **P5.1** [10-06 实机基线](../90-history/worklogs/2026-10-06-zhvpn-asset-baseline.md)中的 Pixel 7a 记录仅作历史证据。当前用户提供的 AGENTS.md 标明生产 Motorola 仍使用 `dxreverse`/`dxandroid-control` 兼容实现；须重新只读盘点其真实二进制、TCP/TLS/证书、存储、启动和自愈能力，不能以历史 Pixel 记录代替。选择受控替换手机 client 或明确封装方案，不能因协议兼容推定手机已支持 TLS。
- [ ] **P5.2** 目标默认采用 TCP + TLS 1.3、双端身份验证及每出口独立 credential；证书身份绑定登记出口，单纯“同一 CA 签发”不授予 session 权限。沿用 TCP/yamux 路线，不默认切回未重新验收性能的 QUIC。
- [ ] **P5.3** 实现信任锚/证书更新、设备 credential 换发及受限重叠窗口、到期撤销；错误 Hub、错误出口、过期/撤销凭证均拒绝，存量 session/stream 的撤销也在预算内完成。
- [ ] **P5.4** 建立新版双端本地及受控手机 canary；迁移 listener/端口由实测后写入变更单，不在本计划猜测。新协议认证失败不得自动回落裸 TCP。
- [ ] **P5.5** 保留手机出口、IPv4/IPv6、双会话调度、活跃/拨号保护、96/48 限额与 idle-preempt；验证拥塞、断网、WiFi/蜂窝切换、冻死会话和恢复，不用单次出口 IP 成功替代耐久证据。
- [ ] **P5.6** 手机部署保证只有一套受管 supervisor，验证开机/异常恢复及远程管理仍可用；完成合法节点迁移和观测后，轮换旧共享认证材料并退役 raw TCP。
- [ ] **P5.G** 双端身份/撤销负例、真实手机恢复与性能证据均通过，裸 TCP 已按迁移门禁退役；未换手机或仍有 raw TCP 时不能勾此阶段。

### P6 — 模块整理、依赖与可观测性

- [ ] **P6.1** 按已验证的责任边界拆分 app/reverse 编排、协议、状态、调度与平台适配；每次机械迁移与行为变化分开核验，不为了行数重写全部实现。
- [x] **P6.2** 按实际支持平台/build tags 更新工具链及受影响依赖；分别核查 module/package/symbol 报告的可达性，保留有期限与依据的例外，不盲目全量升级 major 或 `audit fix --force`。10-07第八波冻结v13：6个Go OS/arch、desktop/Admin两棵含dev的npm tree、4个Rust target tree通过，有效例外截止2026-11-06；见各波worklog。
- [ ] **P6.3** 凭据经 Windows/macOS 受保护存储与访问控制适配；引擎优先通过可信本地通道取得秘密，配置迁移失败可恢复，提权/不同用户不静默扩大访问。
- [ ] **P6.4** 初始化不删除现用日志；统一轮转、容量、脱敏和受控诊断包，记录 operation ID、状态 generation、出口身份与构建标识；不输出 token、私钥或完整敏感配置。第八波仅补专用CLI后台client有限typed日志/独立health，见[日志清单](client-engine-log-plan.md)；Hub/手机、operation/出口关联、受控诊断包仍未闭合，不勾整体。
- [ ] **P6.5** 指标区分建立、转发、异常结束、出口验证与撤销延迟；提供明确告警及恢复命令。在受控条件比较 CPU/内存、连接建立 p95、吞吐和恢复时间，安全切换不能以关闭认证换取性能。
- [ ] **P6.G** 依赖处置、日志保留、指标语义和性能比较有有效证据，运行手册与已实现边界一致。

### P7 — 本地自动门禁与可追踪发行

- [x] **P7.1** 建立一个本地自动门禁入口，失败立即中止发行：合同/生成漂移、Go test/vet、可用环境的race、GUI check、Rust test/check、SDK测试与安全扫描；各失败保持原始输出和证据。10-07第八波冻结v13 `check-steelman.ps1` exit0；SDK35/Rust41、真实CLI-HubTLS/WG及日志留存、离线更新CLI、CLI/SDK builder9/16、NSIS13+完整模板、Admin消费者43及npm省略负例。proxygate与typed event同源schema接正式gate；Linux v3日志slice/真实产品/内核WG八项屏障fixture与vet通过。Linux race未执行，正式签名/发行仍在P7.G。
- [ ] **P7.2** Windows/macOS 与 Hub/Android 目标组合均有编译检查；需要行为证据的 OS 用实机/受控 VM 运行，交叉编译不勾平台验收。用可控时钟/同步条件修复计时敏感测试，不能放宽断言掩盖失败。
- [ ] **P7.3** 干净 worktree 使用锁定依赖、显式产品/版本/协议/完整 SHA/工具链和空产物目录构建；本地 dev 可标 dev，release 不接受不可追踪的 `local` 或错误父仓库 VCS 标记。
- [ ] **P7.4** 发布清单记录构建与源码关系、hash、依赖清单/SBOM、安全扫描及有期限的例外、兼容矩阵和批准产物；明确可复现/可追溯边界，不以不同 OS/工具链必须字节相同作为未经证明的承诺。
- [ ] **P7.5** Windows Authenticode、macOS 签名/公证与更新包签名分别实现和验收；可信密钥在受控构建机管理，未签名构建不能混称正式产物。
- [ ] **P7.6** 更新渠道具备签名验证、版本策略、分批、失败恢复和健康检查；签名元数据绑定产品、平台、架构、渠道、协议与产物 hash，拒绝不匹配和旧发布重放。受控回退只能到满足当前授权 schema/撤销状态的最低安全版本，不能允许未签名或已禁止版本。
- [ ] **P7.G** 真实安装/升级/失败恢复、GUI/CLI/SDK 协议与签名验证通过；任何门禁未执行或平台缺失时，发行保持未就绪。本地自动门禁不依赖 hosted CI。

### P8 — canary、生产切换与兼容退役

- [ ] **P8.1** 准备逐切片变更单：准确二进制/服务/端口/peer、前后值、影响、备份、停止条件、恢复版本及验证；遵守 AGENTS.md 在具体生产改状态前确认。
- [ ] **P8.2** CLI/GUI/SDK 先内部受控 canary；Hub 持久授权/peer agent、手机 TLS、legacy enforce、私钥清理、防火墙和 listener 分成各自维护窗口，不同切片的回滚互不覆盖。
- [ ] **P8.3** 沿用 [客户端安全迁移计划](../40-security/client-security-migration-plan.md) 的 campaign、双次验证、unknown 处置、静默窗口和 Go/No-Go；本计划不缩短其 30 天窗口或用测试 token 代替有效安装实例分母。
- [ ] **P8.4** HTTP 收口、legacy 私钥清理、raw TCP 退役各自获得真实消费者与关闭证据；runtime 已安全、新实例不再产生 legacy 授权，才删除相应兼容代码。
- [ ] **P8.5** 演练“禁用设备→部署失败→回退→重启”：被撤销设备仍禁用、受保护 peer 仍正常。备份恢复合并其后撤销记录，不恢复旧权限或长期重新开放明文认证。既有迁移计划的 token/legacy 私钥应急恢复仅适用于仍授权且重新核验的对象；不得复活已撤销/禁用/过期凭证及被替换公钥，旧版恢复也不能绕过授权执行边界直接写 peer。
- [ ] **P8.6** 每日观测记录错误率、延迟、资源、peer/credential 撤销、代理恢复与手机出口。非 P0 波动先定位，不能把普通广告失败或部分 IPv4 退化自动当作必须撤回安全改造的依据。
- [ ] **P8.7** 按实际变化更新 README、架构、server-access、diagnostics、实现与安全文档，每个实质切片有 worklog；私有证据/密钥/无关 dirty 文件不进入交付。
- [ ] **P8.G** 有真实安装实例、真实运行与连续观察证据，全部安全门禁通过、兼容退役落实；代码合入或端口测试通过不能勾生产完成。

### P9 — 最终独立审计与收尾

- [ ] **P9.1** 对 F1–F14 逐项独立复核：旧行为的失败断言、修复后的允许/拒绝/恢复结果、相关消费者、平台与运行证据，明确源代码和部署版本。
- [ ] **P9.2** 九维沿用审计权重重新评分，达成 G05；没有以例外、未签名产物、未关闭明文入口或人工绕过门禁换取高分。
- [ ] **P9.3** 安全演练包括伪造身份、重放、过期/撤销、跨设备认领、XFF、乱序任务；恢复演练包括断网、CLI/GUI/引擎崩溃、损坏 journal、磁盘写失败、重启、升级与安全回退。
- [ ] **P9.4** 封存脱敏终验报告、受支持版本/平台矩阵、未纳入范围的明确限制与运维交接；删除失去用途的迁移适配须核实消费者并保留安全恢复能力。
- [ ] **P9.G** G01–G05、各阶段门禁及既有安全迁移终验全部通过，再将计划标记 COMPLETE。

## 6. 首批实施切片

首批顺序：P0 基线与资产对账 → P1 实例/状态/恢复合同 → P2.2 身份安全终止与 P3.2/P3.4 代理归属/恢复 → P4 授权撤销模型。手机认证加密的离线双端验证可同步准备，生产新端口/证书/手机部署另按成熟切片执行。

这样优先控制误杀和用户网络设置受损，同时为数据面撤权和 TLS 提供明确身份模型。基础本地门禁从第一个行为切片开始建设，不等全部重构结束才补测试。

## 7. 进度与执行记录

| 日期 | 任务 ID | 提交/产物 | 验证层级与结果 | 脱敏证据 | 未完成限制 |
| --- | --- | --- | --- | --- | --- |
| 2026-10-06 | PLAN | 本计划 | 文档准备；代码/本地/实机/生产实施均未开始 | 当日 worklog | 全部实施 checkbox 未勾 |
| 2026-10-06 | P0.1；P1/P2/P3 首切片 | 合流 `3c88880` 后的 `codex/zhvpn-steelman-runtime` | 最终本地门禁通过；Windows 真实 child、GUI Rust 30 项及 mock 浏览器；Windows/Darwin CLI 编译 | [首切片 worklog](../90-history/worklogs/2026-10-06-zhvpn-runtime-safety-slice.md)、[资产基线](../90-history/worklogs/2026-10-06-zhvpn-asset-baseline.md)、[客户端合同](client-runtime-safety-contract.md) | P0/P1 全阶段门禁、CLI 代理租约迁移、Mac 实机和生产仍未完成 |
| 2026-10-06 | P1.6/P3.1/P4.2–P4.6 局部基础 | 基于首切片 `9113a3a`，共享合同/SDK、离线授权和代理租约核心 | 生成漂移及统一本地门禁通过；SDK 17、代理基础 24、授权 30 实质套件；独立反例修复 | [后续基础 worklog](../90-history/worklogs/2026-10-06-zhvpn-contracts-foundations.md) | 租约未接入 CLI；授权未接 API/真实 WG；Mac/手机 TLS/发行/生产未完成 |
| 2026-10-07 | 运行时/设备消费者与第二波边界 | `072bbc4`、`490e9fd` | 冻结v3统一gate exit0；第二波独立恢复/HTTP/公钥与Chrome管理台反例通过；两份九目标clean开发编译 | [第二波worklog](../90-history/worklogs/2026-10-07-zhvpn-security-boundaries.md) | 实机、当前授权迁移、真正备份恢复、更新水位/安装与生产仍未完成；legacy执行预算继续实施 |
| 2026-10-07 | 第三波兼容执行/共享准入与unknown | `947c5f5`、build receipt `3e4502a` | 冻结v4 gate exit0、九目标clean开发构建、Windows race/Linux native、独立原幽灵lease与编译Chrome负例通过 | [执行预算worklog](../90-history/worklogs/2026-10-07-legacy-control-process-budget.md) | 远端结果/跨重启unknown、设备租约身份、生产容量仍未完成 |
| 2026-10-07 | 第四波离线更新水位与当前只读资产 | `a2ecd68` | 冻结v5 gate exit0、九目标clean开发构建、实际CLI八进程/31条独立检查、Windows race/Linux普通；Hub/本机运行binary只读复核 | [更新worklog](../90-history/worklogs/2026-10-07-zhvpn-trusted-update-state.md)、[当前资产](../90-history/worklogs/2026-10-07-live-readonly-inventory.md) | 安装/签名/外部防回滚、Mac/手机、全部生产门禁仍未完成 |
| 2026-10-07 | 第五波 v2 设备凭据与真实代理数据面 | `17e7689` | 冻结v6 gate exit0；九目标clean开发构建/hash一致；Windows实际 CLI/TLS/SQLite/sing-box/WG/owned proxy/target 与撤销；WSL实际 WG/TLS、native control 与初始 Service 负例 | [第五波 worklog](../90-history/worklogs/2026-10-07-v2-proxy-bootstrap.md)、[设备启动合同](v2-proxy-bootstrap.md) | 初始对账部分失败时外部旧 WG peer 仍可达的实际反例；持续租约/撤销 SLA、迁移、实机与生产仍未完成 |
| 2026-10-07 | 第六波provisional清册/单调历史/持久observer | `bf0f608` | 冻结v8 gate exit0、十目标clean/hash一致；actual SQLite/HTTP、9原生children与旧坏metadata洗白修复；Admin43/编译Chrome21条分页和五旧页签 | [第六波worklog](../90-history/worklogs/2026-10-07-migration-inventory.md)、[清册合同](migration-inventory.md) | 真实lineage/受控导入、逐负类别时间/T0连续窗口、外部数据面屏障、日志/恢复/实机/签名与生产未完成 |
| 2026-10-07 | 第七波真实proxy启动/续期屏障 | `8323ca1`；本地构建收据见worklog | 冻结v11统一gate exit0、十目标clean/hash一致；Linux真实产品/内核WG八项验收、Shared/Reverse专项和vet通过；半撤销、跨expiry、ACK丢失、真实SIGKILL/重启正负对照 | [第七波worklog](../90-history/worklogs/2026-10-07-device-proxy-startup-barrier.md)、[屏障合同](device-proxy-startup-barrier.md) | 其他WG路径/同IP公钥、完整设备会话与生产撤销SLA、日志/恢复/实机/签名/正式campaign继续未完成 |
| 2026-10-07 | 第八波client安全日志与上游派发fence | `3b5a2c5`；本地构建收据见worklog | Windows v13统一gate/Linux v3 exit0、十目标clean/hash一致；cache stderr旧反例/新拒漏、真实detached child/blocked IO、actual yamux未来/clear旧失败新通过 | [第八波worklog](../90-history/worklogs/2026-10-07-client-engine-observability.md)、[日志合同](client-engine-log.md)、[派发清单](proxy-dispatch-fence-plan.md) | 原Linux v2 expiry计时失败未复现/根因未定；整体P6.4、yamux contextual Open/内部对象、其他WG/持续授权、恢复/实机/签名与生产仍未完成 |

本轮切片检查点单独登记，不代替上面的完整任务/阶段门禁：

- [x] Windows 认证实例启动/停止、旧 PID 拒绝、崩溃后重启与跨进程锁回归。
- [x] 活实例丢失 state/PID 返回降级；停止须等待 lifetime lock 释放；登出保留凭据。
- [x] Windows junction 规范化、hardlink/owner/权限负例；初始化保留现用配置与日志。
- [x] Go test/vet/race、SDK 5 项、前端零错误/警告的首轮本地门禁；Windows CLI 与 Darwin 两架构编译。
- [x] GUI 代理 journal、跨会话事务锁、ready 授权校验及登录页错误可见性完成补充复核与测试；真实用户代理/双登录会话尚未演练。
- [x] CLI JSON 五份同源投影与生成漂移门禁、CLI/GUI 类型消费者及 SDK v1 校验/legacy unknown/安全异常回归，见后续基础 worklog；不代替完整 HTTP/Admin 合同。
- [x] 离线设备授权/历史撤销/outbox/intent/fence；独立旧 DB 替换、过期 apply 与 policy alias 反例修复；不代替真实 API/WG 撤权。
- [x] CLI 代理 v2 租约核心与合成 Windows adapter；Notify 后变更、journal 别名 panic、跨进程锁回归；不代替真实 phase gate/命令接线。
- [x] CLI 用户级代理租约接入真实 phase/lifetime gate；认证 child + 合成 OS 验证停止恢复失败保留引擎与 WAL、跨用户/错误lease拒绝及原owner崩溃恢复。
- [x] 默认关闭的 Hub v2 API、独立客户 WG executor/scheduler；Windows Job 与 Linux helper/Hub SIGKILL 真实进程负例通过，仍不代替真实 tunnel。
- [x] reverse 本地真实 mTLS/CONNECT/yamux/target 回环，阻塞写入撤销、登记 exact fields 与 Unix namespace 负例通过，保留正常调度和限额。
- [x] 新设备 CLI→Hub 真实 TLS 互通、响应丢失 receipt/cancel 和发行/扫描统一门禁完成最终源冻结复核；Chrome mock IPC补现旧ready残留，修复后的冻结v2统一gate exit0。
- [x] legacy/trusted/Admin 实际 HTTP server 的来源、完整 JSON/body/header/read/write/idle 预算；真实慢连接及 XFF 伪造负例通过。
- [x] 指定昂贵入口共享有限准入、request context 到 WG/SSH、Windows Job/Linux helper native 监督；明确未启动的并发失败链不复活 lease，服务端 unknown 到期/重载不解锁。Windows race/Linux普通、独立原反例及编译 Admin Chrome 通过。设备/会话身份、远端 RID/跨重启 unknown、所有入口与生产容量未闭合，P4.5 保持未完成。
- [x] Admin 合同/生成配置 guard、npm omission 拒绝和真实状态消费者；Chrome 编译页面合成 API 验证空数据、失败、迟到权限和 mutation 刷新并发。生产消费者与完整 durable operation 仍未完成。
- [x] 更新 metadata 纯验签、scope/版本/安全 floor/Previous 与实际产物检查；Windows race/vet/schema drift 及独立反例通过。不代替可信持久水位、签名发行或安装。
- [x] update CLI明确批准Policy、protected registration anchor+单一Policy/Previous/floors状态、同home跨进程锁与水位提交接线；实际CLI八进程/重开、31独立check、NTFS unknown与Linux FIFO负例通过，schema同源门禁已接入。同owner旧快照回滚、安装/签名和生产仍未完成。
- [x] device Ed25519 输入共享 canonical/small-order 拒绝；离线 authority 恢复比较、保护文件读取及极端时间/非法 UTF-8/超大 TEXT 独立反例通过。`ready_to_restore=false`，实际恢复/最新事实保管链未完成。
- [x] device bind/start 经正常 TLS 取得短期配置投影；本地独立 WG 密钥、generation/profile/精确路由绑定实际 engine，错误 bytes/迟到取消拒绝；真实 CLI→WG→目标 marker、实际撤销与 protected peer 保留通过。Service TLS initial Tick 不是完整外部数据面屏障，P4.G/G01 仍未完成。
- [x] provisional清册显式批准raw SHA、immutable baseline/extra、失效source保留、单调历史及observer预落run/sticky gap；旧坏secure metadata后来合法观测洗白反例修复。canonical Admin contract2/只读迁移页、实际21条分页与迟到权限/秘密负例、Windows race/Linux普通及冻结v8统一门禁通过。真实lineage、逐负类别时间、T0连续窗口及正式campaign仍未闭合。
- [x] 实际TCP/TCP-TLS proxy默认关闭Admission、protected UDS及最终fenced DB/WG grant；先Reserve后Open与有限quarantine，迟到Attach/容量同session ABA反例修复。冻结v11统一gate及Linux实际产品/内核WG半撤销、跨expiry、SIGKILL/重启验收通过；QUIC gated hosting在load/bind前拒绝。仅本route，不代称全部WG屏障/持续设备授权/生产SLA。
- [x] 专用CLI有限typed日志/独立purpose-HMAC健康、64项队列/单writer、1MiB自有namespace和Win/Unix保护；Windows缓存stderr/raw logger泄漏反例修复，实际child正常Stop后立即重启、真实bind失败和同步阻塞Store IO负例通过。受管上游permit/Close/Release fence，实际yamux两个旧失败新通过、retained已有echo保持；冻结Windows v13/Linux v3通过。不代称完整日志/诊断包、kernel在途零bytes或yamux内部全部回收。
- [ ] Mac 实机/真实 WinINET/已安装升级、生产授权导入/campaign、备份恢复撤销合并、手机迁移及签名/更新链按阶段继续验收。

| 里程碑 | 当前状态 |
| --- | --- |
| 计划 | 已编写，按切片执行中 |
| 实现 | CLI/GUI/SDK代理接线、Hub v2 authority及监督、设备消费者、reverse mTLS、本地门禁；provisional清册/持久observer及只读页面；真实proxy启动屏障/有限quarantine与上游派发fence；专用CLI安全日志/独立health；生产迁移与跨平台余项仍推进 |
| 新验证 | 第八波冻结Windows v13统一gate/Linux v3日志与实际产品/内核WG验收exit0、十目标clean/hash一致；第六actual SQLite/HTTP/原生进程与编译Chrome消费者通过。先前10-07只读快照为旧binary；随后16:14–16:21兼容canary已部署并真实连通，见部署worklog。owned fixture和合成HKCU仍不替代真实系统代理/Mac/手机TLS证据 |
| 生产切换 | 已完成3b5a2c5兼容Hub/reverse+单Windows CLI canary及真实HTTPS/IPv6连通；v2/手机TLS/正式发行未切换，既有安全迁移仍NO-GO |
| Steelman 终验 | 未完成 |

- [x] 2026-10-07第八波兼容binary部署及单Windows新版CLI真实连通：来源3b5a2c5、三目标hash一致、手机双session恢复、28peer映射保留、ready/logging healthy、代理HTTPS/IPv6响应通过，见[部署记录](../90-history/worklogs/2026-10-07-wave8-compat-deployment.md)。这是一项受控dev canary，不勾选P8整体、安全迁移或正式签名发行。

## 8. 2026-10-07 接线与发行边界

本轮已把之前的离线模型接到产品调用路径，不能再把“只有基础模块”当作完整进度。详细源码/负例/证据见 [运行时集成](steelman-runtime-integration.md) 与 [10-07 worklog](../90-history/worklogs/2026-10-07-zhvpn-runtime-integration.md)。安装器尚只支持可验证的全新目标：不调用旧卸载器、整包 staging 后无覆盖发布；已有安装升级/自动卸载明确拒绝，不能勾 P3.5/P7 的完整升级验收。Mac OS adapter仍unsupported，真实Roaming ACL拒绝不以改profile放行。

后续依赖按实际顺序：完成本轮源冻结门禁/开发产物 → 受控凭据/平台适配与迁移清册 → 干净可信发布/签名及更新协议 → 逐实例 canary与手机迁移 → 当前授权事实源切换和完整观察 → P9多维独立评分。生产切换、30天连续窗口、Mac/手机物理证据不能由本地测试或代码量勾选；全部 G01–G05 仍未达终验。

剩余不仅是硬件验收：Mac OS adapter、真实campaign/installation lineage、逐负类别时间与持续窗口、其他WG路径/同来源IP公钥隔离、持续设备/会话授权、yamux contextual Open/内部容量、受控导入、最新撤销事实保管链与实际恢复、安装维护协议/外部更新防回滚、远端结果确认、Hub/手机日志及跨组件诊断/性能指标仍有源码缺口。第六波已补provisional固定分母/完整source与历史阻断，第七波已补真实受管proxy启动屏障，第八波补client有限日志与原子派发permit；它们不能代称正式campaign、全部WG隔离、完整连续租约或P6.4。初始对账部分失败的实际反例现经产品proxy负对照阻断；不能把局部完成勾作整个阶段或生产安全迁移。见[第六波worklog](../90-history/worklogs/2026-10-07-migration-inventory.md)、[第七波清单](device-proxy-startup-barrier-plan.md)及[第八波worklog](../90-history/worklogs/2026-10-07-client-engine-observability.md)。
