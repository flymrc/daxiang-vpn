# 离线可信更新水位（2026-10-07）

状态：LOCAL_IMPLEMENTED。对应 Steelman P7.6 的受保护状态切片。真实 CLI 已消费纯验证器并持久保存批准 Policy、Previous 和累计下限；这不是安装器、签名发行或生产更新渠道。

## 命令与批准边界

入口为 `clients/cli/internal/updateclient`，`app.Run` 的 `update` 分支输出独立 JSON receipt。全部命令必须显式给出同一组 product/platform/architecture/channel；一份 home 只登记一个 scope，不自动选择渠道或从候选推断 scope。

```powershell
zhvpn update enroll --policy-file reviewed-policy.json --approve-policy-sha256 <已审核批准的原始文件SHA256> --policy-revision 1 --product cli --platform windows --architecture amd64 --channel stable --json
zhvpn update verify --metadata-file signed-envelope.json --artifact-file candidate.exe --product cli --platform windows --architecture amd64 --channel stable --json
zhvpn update policy-approve --policy-file reviewed-policy-v2.json --approve-policy-sha256 <本次审核批准的原始文件SHA256> --policy-revision 2 --product cli --platform windows --architecture amd64 --channel stable --json
zhvpn update inspect --product cli --platform windows --architecture amd64 --channel stable --json
```

`--approve-policy-sha256` 必须是人类独立审核后的、64 位小写 SHA-256。它绑定 policy 文件的原始 UTF-8 字节，包括空白，不是“候选自带的信任材料”。仅对未经审核的文件计算 hash 再传入不能建立可信 publisher。Policy 中的公钥 ring、scope、初始版本/协议/序列/floor 和 revision 必须先获批准；程序只检查显式批准摘要是否相同，无法代替人类批准过程。没有内置发行公钥，没有从网络或 candidate 导入 ring。测试随机签名身份只属于 owned fixture。

`PolicyDocument` 是 JSON 字段维护源，[policy-v1.schema.json](../../clients/cli/internal/updateclient/policy-v1.schema.json) 自动投影。必填字段为 schema_version、policy_revision、scope、min_version、min_protocol、max_protocol、min_sequence、security_floor、max_artifact_size、max_validity_seconds 和 keys。每个 key 有 key_id、canonical base64 public_key、有效窗口和序列范围。秒数在转成 `time.Duration` 前限制为 1..2592000；revision/序列/floor 不超过 JSON 安全整数。`updateverify.ValidatePolicy`、版本比较、scope 枚举与现有纯验证器共用同一源，不另定义信任规则。

revision 必须正整数、十进制无别名，policy 文件、命令 revision 与受保护状态一致。新批准 revision 严格增大，允许跳号；不得降低已累计的版本优先级、最小协议、发布序列或 security_floor。批准换 key 或撤销 key 保留 Previous 和旧水位，不能借 rotation 从 nil Previous 重新开始。security_version 的已验证高水位同样保留。Policy 本身不宣称当前运行程序已经达到这些 floor。

`verify` 不接受 `--policy-file`、审批或 revision override；`inspect` 也不接受候选参数。重复、无关、缺失的 flags、非法 scope、`--now`、`--json=false` 均拒绝。默认 deadline 15s，允许 1ms..30s；没有联网、启动产物或自动修复/reset 命令。

## 存储、并发与提交

固定私有路径只有 `update-v1-registration.json` 和 `update-v1-state.json`。复用现有 `proxy.PrivateState` 的 home 规范化、Owner/DACL 或 Unix mode、非 reparse/symlink、单链接及受保护临时文件能力，不自动修复既有不安全 ACL。当前真实 Windows 用户 profile 是否可用仍单独验收，合成私有 home 不能替代它。

每个命令，包括 inspect，持同一 home `operations.lock`：读取 anchor/state → 验证 → 提交 → 精确读回。`WithOperationLockContext` 与原 CLI 操作锁共用同一文件，取消会停止争用等待并在入锁后再次检查，不创建第二套独立锁。锁文件/目录本身可能创建；“离线”不表示命令没有本地状态副作用。

registration anchor 固定随机 registration ID、scope、首次 revision、批准摘要和首次 Policy 原始 JSON。初次登记先用 protected `Create` 原子 no-replace 创建 anchor，再 Create state；Create 使用同目录临时文件、保护/写/Sync 后 hard-link 发布并删除临时名字。不支持该机制的文件系统拒绝，不回落覆盖创建。两份登记文件之间不存在跨文件原子事务，因此 anchor 已成功而 state 未成功会留下 incomplete 登记并拒绝继续；不自动删除 anchor 或重试 fresh enrollment。

state 是唯一可变记录：同一原子替换保存当前批准 Policy 原始字节及 hash/revision、累计 Floors、HasPrevious 和完整 Previous receipt。初次登记后的每次批准/验证只替换这一份记录，不把 Policy 和 Previous 分开写。Previous 的摘要、scope、时间与 metadata 形状重新检查；floor 必须等于 initial/current Policy 与 Previous 的合并结果，HasPrevious 必须匹配实际历史字段。

任一端缺失、损坏、不同 registration ID/scope、策略回退、历史断链、非法 JSON 或文件权限/链接不符都拒绝。两端都不存在时 verify/inspect/policy-approve 返回 registration_required，也不会暗中 enroll。程序内没有 reset；人类手动删除整组文件后明确执行新批准 enrollment 属于新的信任决策，本片不能发现管理员抹去的历史。

跨进程串行化后总是使用最新水位：低序列拒绝，同序列不同正文/key 拒绝，精确相同候选可幂等，但仍重新验当前批准 key/期限/floor 和实际产物。较高序列还不能降低版本、security_version 或 security_floor。hash 完成后、提交前再次核验真实当前时间和选中 key 的窗口；clock 回退到本次 VerifiedAt 之前也拒绝。Policy floor 提升独立提交，因此随后坏候选不会撤回新的批准下限。

任何尝试提交后的 I/O 错误、精确读回失败或提交期间取消返回 commit_unknown/result_unknown，不输出 staging receipt 或 `watermark_committed=true`，不写回旧状态作补偿。成功写入但响应丢失的真实状态保留；新的 inspect 显示当前水位，重新 verify 精确包仍须全量验证。进程崩溃恢复及原子替换不等于已证明断电耐久性：现有 Write 是 temp protect→write→file Sync→Close→os.Rename，没有父目录 fsync 的跨平台保证。

同 UID/管理员拥有存储权，可以用完整旧备份替换 anchor/state，或一并删除二者。本地 ACL、摘要和操作锁无法抵抗这种 authority rollback；正式测试明确展示该范围外事实。需要外部防回滚锚或受控机器信任才能提升这个保证，不能称本片具有远端或硬件 anti-rollback。

## 输入与回执

Policy/envelope 是有界 regular 文件字节，artifact 以只读固定句柄流式读。Unix 使用 NONBLOCK/CLOEXEC/NOFOLLOW 后 fstat，FIFO 或最后一层 symlink 拒绝；Windows 在 Lstat 前纯语法拒 UNC/NT/device 前缀、DOS device、ADS 和 drive-relative 名字，再拒 remote/无效 drive，CreateFile 用 OPEN_REPARSE_POINT、disk/type 检查和仅 shareREAD。打开前后身份、size/mtime 必须一致，产物读取后再次核验。

这些公开输入的内容信任来自批准 SHA 或签名 hash，不施加 authority 的 private ACL/single-link 规则；公开 hardlink 和与 metadata.artifact_name 不同的输入 basename 可以接受。它们不授予执行路径，也不表示保存了不可变 staging 副本。artifact 的大小/hash 只对本次实际读到的字节负责；后续安装若重开路径仍须有独立安全合同和再验证。

没有应用级 HTTP fetch 或远端命令。显式 Windows remote/device 路径提前拒绝，但不能识别所有由 OS mounted filesystem 提供的网络行为。正常 local regular 文件读取是同步的，context 在 read 之间及提交前检查，不能承诺中断已经阻塞的内核文件 I/O；不创建可能遗留的 reader goroutine。policy 上限 16KiB、state/receipt 64KiB，artifact 受批准最大值及纯验证器 8GiB 全局上限约束。沿用 PrivateState 的底层读取上限 1MiB 后拒绝超 64KiB 状态，不把它用作 artifact reader。

所有输入/状态严格拒绝重复（包括转义后重复）、未知/不同大小写字段、null、缺失、尾随数据、非法 UTF-8、数字类型错误；JSON 深度≤8、总成员≤4096，key 数组≤16。错误码固定，不回显原始 I/O 错误、输入路径或文件内容。

公开 `Receipt` 的 [receipt-v1.schema.json](../../clients/cli/internal/updateclient/receipt-v1.schema.json) 从 Go 源自动维护。DecodeReceipt 增加 command/outcome、OK/unknown、floors/metadata digest 等关系校验；schema 单独通过不能产生资格。只有 verify 成功且提交/读回完成时返回 watermark_committed 和纯验证器 staging receipt。inspect 不产生新 staging receipt。输出写失败返回 ErrReported/非零，不能伪报 acknowledgement。

## 本地验收及仍待完成

```powershell
go run ./clients/cli/internal/updateclient/cmd/schemagen -check
go test -race ./clients/cli/internal/updateclient/... ./shared/updateverify/...
go test -race -tags integration ./clients/cli/internal/updateclient -run '^TestCLIUpdate'
go vet ./clients/cli/internal/updateclient/... ./shared/updateverify/...
```

正式测试包含真实 CLI binary、真实可执行候选但执行 marker 始终不存在、8 个 CLI 进程序列竞争、进程重开/响应丢失、幂等重验、key rotation/撤销、旧包/equivocation、单端丢失、历史/floor 断链、raw SHA 审批、极端秒数/JSON、输入平台安全打开及提交故障。Windows race、实际 Linux 普通运行及独立 private overlay 另见当日 worklog。Linux 未安装 C compiler，不声称 Linux race；没有 macOS 实机证据。

- [x] 明确批准 Policy、protected anchor+single state、跨进程序列水位、真实 CLI 接线与负例。
- [ ] 真正 publisher/签名身份、AuthentiCode/macOS 签名/公证、批准密钥管理。
- [ ] staging copy/安装维护协议、已有安装升级、受控恢复及真实健康检查。
- [ ] 外部最新安全水位/防回滚保管链、macOS 实机与生产启用/连续观察。

原 [纯 metadata 验证](trusted-update-metadata-verifier.md) 的函数职责未改变；本片增加本地 consumer，不把 release_ready、已安装或生产成功设为 true。
