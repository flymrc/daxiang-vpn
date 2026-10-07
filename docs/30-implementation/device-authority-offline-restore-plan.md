# Device authority 离线备份恢复规划

本功能只比较两个明确指定的离线 SQLite 快照和调用方核验的最新 checkpoint，生成供人审阅的 JSON 计划。`ready_to_restore` 恒为 `false`。程序不替换数据库、不调用 WireGuard、不执行恢复 SQL、不打开运行中的 `Store`，也不读取生产接口。该能力补齐恢复前的事实核对，实际备份恢复、迁移 epoch、authority 切换和恢复演练仍未完成。

## 输入与最新性边界

输入包括 backup、latest authority 和最新 checkpoint 三个文件，以及明确的 `--as-of`、`--max-checkpoint-age`。陈旧预算必须为 1 秒到 24 小时内的整秒，checkpoint 时间不能晚于规划时间，二者的间隔不能超过预算；checkpoint 时间也不能早于快照中已有的提交、撤销、消费或验证事实。

规划时间、checkpoint 和数据库的纳秒时间字段只接受 1970 年之后至 2200-12-31 的正纳秒范围；challenge 的 Unix 秒字段单独校验相同日历范围。先拒绝超界时间，再做 `UnixNano` 和过期比较，避免合法 RFC3339 的 9999 年输入在纳秒转换时溢出后误判有效期。

checkpoint 的 SHA256 必须匹配 latest authority 的完整文件，schema 必须为 authority schema 2，epoch、managed_by 和 policy SHA256 必须相符。checkpoint 严格拒绝重复、未知、大小写别名字段、非法 UTF-8 和尾随 JSON，文件最大 16 KiB。共同 JSON 解析器还限制 8 层嵌套和累计 40,000 个对象成员/数组元素，错误只返回固定诊断。

checkpoint 是调用方明确认可的输入，**不能证明自己是最新事实**。`latest_revocation_facts_confirmed: true` 表达调用方完成了外部核验，不是服务端签名或自动认证。若事故前的 checkpoint 之后发生过 revoke、disable、换钥或取消请求，必须取得包含这些事实的新 authority 快照与新 checkpoint；即使旧 checkpoint 仍在陈旧预算内，也不足以覆盖这些后续事实。调用方还需核验事故期间最后一次写入的来源与保管链，程序无法发现未提供的数据库分支。

checkpoint 格式：

```json
{
  "schema_version": 1,
  "authority_schema_version": 2,
  "authority_sha256": "<latest authority 文件的 SHA256>",
  "epoch": "<同一 authority 的 epoch>",
  "managed_by": "<同一 authority 的 managed_by>",
  "policy_sha256": "<meta.policy_json 原始 UTF-8 字节的 SHA256>",
  "verified_at": "2026-10-07T00:00:00Z",
  "latest_revocation_facts_confirmed": true
}
```

占位值不是有效 checkpoint。规划器不会替调用方生成或认可 checkpoint，也没有默认选择“最近文件”的入口。

## 只读与一致性校验

SQLite 输入必须是普通、单硬链接的完整单文件离线快照，拒绝 symlink/reparse point。旁边存在 `-wal`、`-shm`、`-journal` 时直接拒绝，不忽略未合并的事实。单个 DB 上限 256 MiB，累计读取上限 100 万行。

输入必须放在调用方已保护的私有目录内，程序不改已有文件或目录的权限。Windows 只接受本地固定卷；文件和直属目录必须有 protected DACL，owner 和允许的主体限当前进程用户、SYSTEM、Administrators。逐层固定目录句柄，拒绝 reparse point 和允许其他主体改 owner/DACL 的祖先；源文件只共享读取，排除并发写入、删除和重命名。Unix 先用 `unix.Open` 固定根目录，再从根到直属父目录逐层 `unix.Openat`，最后相对已固定的直属父目录打开源文件，全程使用 `O_NOFOLLOW`。文件属于当前 UID 且无 group/world 权限，直属目录属于当前 UID 且无 group/world 权限；更高祖先只信任当前 UID/root，拒绝 group/world 可写，root 所有的 sticky 临时根除外。执行前和结束前都重新检查已固定句柄和路径的身份、权限、大小、mtime 和摘要；Unix 可发生的源内容变化或路径替换必须拒绝，Windows 可阻断的并发变化在打开阶段失败。

所有源文件句柄持有到整份计划完成。查询不再重新打开源路径：程序从已认证源句柄读取精确字节，在该源的私有父目录中新建受保护的随机临时目录和 SQLite 副本，校验副本摘要后，只以 `mode=ro&immutable=1`、`query_only` 和读事务查询该私有副本。结束前再次校验源、副本和 sidecar。只删除本次创建的精确临时文件/目录，正常退出尽力清理；进程崩溃或清理受阻可能留下受保护副本，调用方需按本次 owned 路径单独核对后清理。私有父目录需要允许创建临时对象，源数据库始终只读。

这些文件约束保护本次读取的身份与内容，仍不代替备份系统的快照一致性保证，也不能证明调用方提供的是事故后的最新撤销事实。当前用户、root/SYSTEM/Administrators 属于受信保管边界；同主体恶意写入或系统管理员干预不在此文件机制的隔离保证内。

校验覆盖 `integrity_check`、`foreign_key_check`、所有 schema 2 表和精确列/类型/主键形状。未知的 deviceauth table、view、trigger 会拒绝。行校验包括 customer 角色、policy/epoch、地址保留与 binding 归属、current binding、generation、连续 operation 历史、applied generation 的已验证 outbox、credential 换钥链与创建回执、activation 消费、request/operation 绑定、取消回执和已提交请求互斥。

SQL TEXT 在逐行解码之前先按字段执行 byte-length preflight：policy JSON 上限 256 KiB，密钥、opaque ID 和固定状态字段上限 256 字节，其余文本上限 4096 字节。解码后再次校验字段类型、字节上限与严格 UTF-8；immutable 比较使用原生 string/int64/null 精确值，避免 JSON 将不同非法字节归一成相同字符。credential 和取消请求的身份公钥使用运行时同一严格 Ed25519 校验；历史中含弱身份密钥的整个快照会被拒绝，不输出可复授身份。WireGuard binding 仍使用其对应密钥规则。

backup 只是回退风险的比较输入，输出事实一律来自 latest authority。latest 必须保留 backup 已有的 immutable operation、request、credential receipt、audit、address reservation、cancel receipt 等记录。device owner、valid_until、role、binding 初始归属和创建 generation 不可改写；generation、executor watermark、binding 撤销/删除事实、credential 撤销/替换、activation 消费、已验证 tombstone 不可倒退。禁止用较新的 backup 配合较旧的 latest authority 丢掉后续历史。

executor watermark 只作为历史不倒退的校验项；数据库 inode、文件锁或 watermark 均不会使计划成为恢复安全证明。实际恢复仍需明确 epoch 策略、独占 authority、完整负事实和运行时 ownership 核验。

## 计划输出

输出包含三份来源的完整 SHA256 与字节数，明确的规划/核验时间和预算，最新各历史表的行数、相对 backup 的新增行数及 canonical row digest，最新 device state/generation，以及稳定的 `plan_id`。同样输入、同样规划时间与预算产生相同计划。

计划只输出稳定 ID、固定状态码、hash 和 count；不会输出 credential 公钥、activation 材料、verifier、challenge nonce、原始请求、自由文本 reason 或数据库路径。原始数据只用于私有读取副本、一致性比较和计算摘要，不进入计划或错误文本。

device 已 revoked、disabled、expired，或在规划时间已超过 valid_until 时，计划标记 `deny_grant`。旧 peer 不会从 backup 自动复装。可选 `--observed-peers` 输入是人工提供的离线 JSON inventory，字段为 `public_key` 与 `allowed_ips`；程序不会调用实时 `wg show`。unknown/protected peer 仅标记保留；已记录 binding 仍要求人工精确核验，撤销 binding 仅报告保留的删除义务，均不执行动作。输出 peer digest，不输出公钥或 IP。

challenge 属于短期认证材料，不参与 retained history 比较或恢复计划。计划明确要求后续实际恢复时全部作废，不能复用快照中的 nonce。

调用示例：

```powershell
go run ./hub/cmd/zhhub-device-restore-plan `
  --backup C:/private/offline-backup.sqlite `
  --latest-authority C:/private/verified-latest.sqlite `
  --latest-checkpoint C:/private/verified-checkpoint.json `
  --as-of 2026-10-07T00:00:00Z `
  --max-checkpoint-age 5m
```

CLI 参数、输入或校验失败时返回非零，stdout 不产生计划，stderr 只给固定诊断，不回显输入。程序没有 SQL 导出、自动修复、DB 覆盖、live authority、密码或 SSH 参数。

## 验证与未完成工作

真实 SQLite、合成离线数据回归覆盖备份后 credential 换钥与自撤销、disable、expire、activation 请求取消、时钟过期、较新 backup/较旧 latest、缺失或变化的 immutable history、错误 schema/epoch/ownership/generation/FK、陈旧或缺失 checkpoint、9999 年与数据库非法时间、非法 UTF-8、单个 2 MiB TEXT、弱历史 Ed25519 身份、非规范或超界 JSON、普通名称的 authority trigger、SQLite sidecar、全部三类来源的硬链接、重复规划和秘密不回显。平台合成私有文件用例分别验证 Windows 不可信 DACL 拒绝且无权限修复、并发写/删除/重命名阻断，Unix 不安全权限/symlink 拒绝、同字节路径替换及祖先权限改变检测；CLI 正向路径读取真实 SQLite 快照并验证源 hash 不变与无新增 sidecar。这些 fixture 是新建离线数据，不是生产 DB 或实际备份系统。

这些是本地只读规划证据。它们不证明生产 checkpoint 已取得、不证明事故后的全部撤销事实已掌握、不证明 live peer 归属、不证明实际恢复或 revoke SLA。下一步实际恢复设计必须解决 snapshot/WAL 采集、事故前后最新事实的保管链、恢复 epoch、live authority 排他切换、旧挑战作废、设备/peer 重新核验和现场演练，并另行提供具体可审阅的恢复方案。
