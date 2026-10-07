# 设备授权离线持久模型

> 2026-10-07 后续实现：本文记录 10-06 基础切片；API、真实控制器接线与执行监督的当前开发状态见 [运行时接线](steelman-runtime-integration.md)。生产启用与实机验收仍单独登记。

> 2026-10-06 JST。源码位于 `hub/internal/deviceauth`，未接入生产授权 API、TokenStore 或服务启动入口。完整目标见[授权与撤销合同](device-auth-revocation-contract.md)。

## 当前实现

沿用项目现有 SQLite driver，表名前缀为 `deviceauth_`。模型保存已获授权的 customer device、所有者/有效期/generation、地址保留、公钥历史、tombstone、幂等 operation、durable outbox 和执行 intent。`Actor` 必须由未来调用者先认证；字符串 ID 本身不是 credential，`Enroll` 不消费 token 或签发设备密钥。

`Submit` 在同一事务写 desired state、撤销历史、operation 与 outbox。相同主体/动作/幂等键且摘要相同返回同一操作；不同摘要、ownership 或 generation 冲突拒绝。apply 重新核验当前授权，历史幂等结果不会重新授予已撤销或过期设备。旧 key 不复用，地址不自动转给另一 device。设备永久 revoke 不被后来 disable/expire 降级；disabled/expired 的重新授权接口尚未实现。

构造时在任何归一化之前复制 policy 的所有 slice，数据库 policy fingerprint 与执行时保护清单使用该私有快照。调用方复用 Options 并发创建 Store 或之后重载原 policy，不会竞争写共享 slice 或静默松动既定保护策略。

换钥与撤销保留全部 binding 历史。A 已生效、B 仅提交、随后 revoke 的操作仍要处理 A 与 B 的全部撤销义务。新 generation 将旧 pending/degraded 操作标为 superseded，但不删除 intent/tombstone，也不重置未核验旧 key 的撤销预算。

## 执行与崩溃窗口

每个本地 file-backed SQLite 使用从规范化数据库路径派生的持久 `.deviceauth.lock`。所有模型写入和 executor 采用同一文件 fence；不删除锁文件，不依赖 PID/leader lease 推定执行资格。Windows 解析真实目录/文件句柄得到 junction/case 等别名的同一身份，拒绝 UNC、非固定磁盘、reparse 文件和多硬链接。取得 fence 后再次核验 DB 身份，并持有禁止 DELETE 共享的 DB 句柄贯穿操作，阻断排队期间旧备份替换被当成当前授权。Unix 使用 flock/UID/私有文件权限；平台编译不代替运行验证。

`Process` 在 fence 内分三步：提交 claim/intent；精确执行外部动作；读取运行态后提交核验结果。SQLite 与外部数据面不能组成同一原子事务，intent 用于收敛已执行但确认丢失的动作。每次重试读取最新 desired generation 与全部历史；过期任务不能覆盖新 revoke。即使旧 operation 已 done，显式 `Reconcile` 仍重新核验运行态，发现模拟数据面从旧配置复活时重新移除对应旧 key。

Snapshot/intent/apply 之间重新核验有效期。执行过程中发现过期会持久提交新的 expiry generation、tombstone 与 outbox；不会把过期 grant 认证为成功。外部动作内部越过期限仍需后续显式 reconcile 消除权限，当前没有后台 scheduler，不能保证无调用时到期自动生效。

Executor 是同步精确 peer 接口：所有动作必须在返回前结束，超时或结果丢失为 degraded/unknown，不伪报 done。未来真实 subprocess adapter 必须 kill/reap 后再返回，不能让 detached child 越过 fence。file fence 不能约束另一个独立 root/WG 写入者，生产切换必须先建立唯一受权变更边界。Windows 私有目录 owner/DACL 的安装与证明仍是 hosting service 的前置，当前模型没有完成生产权限安装。

## 保护与预算

只操作明确 `managed_by` 且 `role=customer` 的已登记 binding。不能因 desired row 的 key 与未知运行 peer 相同就认领；首次 apply 前要证明 key 不在运行态，之后使用自己的 durable intent/applied 历史恢复。公钥、地址池和保护清单均校验；unknown/管理员/RDP/出口 peer 不自动删除，也不整体覆盖 `wg0`。

`RefreshDeadlines` 持久标记超预算；未完成义务会阻止新的 grant，但不阻止 revoke。grant 历史数量有上限，撤销读取全部历史，不因超过 grant 配额截断旧 key 的移除义务。当前 30 秒是模型内的默认预算配置，尚没有真实 WG 撤销延迟的验收结果。

## 已验证与待实现

真实 Windows 进程、独立 SQLite handles 与文件模拟数据面覆盖提交/动作/核验阶段崩溃、延迟 apply→revoke→旧进程重放、换钥部分失败、保护清单、跨期、幂等、路径别名与 DB 替换。独立复核的原反例已变为拒绝：排队期间恢复旧 DB 返回 ErrPolicy，Snapshot 后已知过期不再执行 Apply。

这些证据证明模型与 fence 的行为，不证明真实 WG 隧道已经失权。尚缺激活/认证签名/nonce/credential、HTTP 合同与接线、导入/campaign、后台到期/启动 reconciler、privileged WG adapter 和权限安装、备份恢复撤销合并、真实隧道验收。现有 `tokens.yaml` 仍是生产授权事实源，完整 P4 仍未完成。
