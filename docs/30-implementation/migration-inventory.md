# Provisional 迁移清册与观察事实

2026-10-07，第六波隔离开发实现。本文件描述新源码；当前生产未部署，生产 observation-only v1 报告不能充当本文 contract2。实施与验证见[checkbox](migration-inventory-plan.md)和[worklog](../90-history/worklogs/2026-10-07-migration-inventory.md)。

## 事实边界

TokenStore 继续决定当前 legacy 授权，本批不导入或接管授权。新 Admin SQLite 只保存人工批准的 provisional 清册、被动追加的未知对象、观察聚合与 observer runs。声明的 owner/installation 引用不是实际安装证明；版本自报及 secure_bootstrap 都不证明批准发行、双次 E2E 或 v2 device 的历史保管链。

`GET /admin/api/migration/readiness` 消费 canonical OpenAPI contract2。始终 `ready=false`、`mode=observation_only`、`campaign_configured=false`、`t0=null`，没有设置 T0 或产生 compliant 的入口。固定 blockers 包括发布、E2E、撤销、session 清理、恢复、静默窗口、连续性和数据面启动证据未完成；逐成员再附 source、lineage 和历史负事实。

## 人工批准与登记

`hub/admin/cmd/campaign-register` 是独立离线运维工具，不是 Hub 开机自动批准动作。输入 version1：

```json
{
  "version": 1,
  "registry_id": "32位小写hex",
  "members": [{
    "token_id": "12位小写hex",
    "owner_ref": "空或32位小写hex",
    "shared": false,
    "installation_refs": []
  }]
}
```

这是形状说明，不是可直接登记的真实清册。token 明文、联系方式、IP、运行路径、公私钥和 T0 均不属于该合同。raw JSON 最多4MiB、1–4096个唯一成员、每成员最多16个唯一安装引用；所有字段必需，重复/大小写别名/未知/null/尾随内容拒绝。

操作者先审阅实际原始文件并明确批准其 SHA256。命令只接受三个唯一的完整 flag/value 对：`--db`、`--inventory-file`、`--approve-inventory-sha256`；没有 env 默认值、自动选库或 --force/reset。错误批准/输入在创建数据库目录前拒绝。Store 自己克隆 raw bytes 并重新严格解码/比摘要，不能将已批准 SHA 与不同成员配对入库。

首次登记在同一 SQLite 事务中提交 sealed singleton header 和 baseline rows，半途失败全回滚。相同 registry/raw摘要幂等返回 already_registered；更换摘要或 registry 拒绝，不覆盖旧分母。没有任何生产清册在本批实际登记，实际登记及其 DB 写入仍须另行授权。

输入拒绝 symlink/重解析、非普通文件、hardlink、FIFO/设备和已识别网络路径/文件系统；Windows 打开叶子前短暂持有不可重命名的祖先句柄，Unix 逐目录 openat/NOFOLLOW，并采用本地文件系统 allowlist。以持有叶子句柄/identity/size/mtime核对有限原始文件，不修改其权限。DB 路径须为操作者控制的本地目录；输入文件的持有策略不能外推为 SQLite 打开期间固定 DB/WAL 的完整路径防护，更不能作为抵抗同账户恶意篡改或整份旧 DB 回放的安全承诺。

## 分母、成员与历史

`baseline_member_count` 恒为批准快照数量。报告 `member_count` 包含基线及追加 extra；`valid_token_count` 单独只统计当前 enabled source，不能再当分母。一次完整 TokenStore snapshot 导出 enabled/disabled/expired/invalid；清册有而 source 无的成员为 missing。失效或缺失成员保留 disposition_required，不能自动视为 revoked 或安全退出。

已登记清册下，GET 的一致数据库事务会被动追加 source/history 中尚未登记的 extra，其 owner/ref 均未知，永远 extra_unregistered；这不是审批或授权写入。观察事务失败则503，不返回健康快照。没有批准清册时报告 unregistered 和 inventory_not_registered；此时仅有 source、历史事实的并集，没有获批的固定分母。

所有对象仍沿用现有12hex TokenID；同次 source 快照的空白别名/重复短ID拒绝整个报告。跨历史快照的短ID碰撞检测、真实 token→installation→device 保管链没有完成。

成功 bootstrap 的最近产品/版本/协议/来源/公钥模式与整体历史分开。已识别有效 token 的拒绝或执行失败也在同 audit 事务中增加 denied/error facts。secure/legacy/unknown/compat 与失败计数单调，后来 secure 或乱序成功不清旧负事实；旧 DB 的观察记录有限回填保留计数，坏 metadata 不继续作为当前 secure transport。Strict SemVer 只检查传输候选格式，不能批准任何构建。

旧 latest 行若被宽松规则标为 secure、其 metadata 实际不合法，初始化事务通过不可删除的 migration_import_flags 补一条 unknown 负事实；已有 facts 同样补，重复打开不重复计数，后来的合法 secure 不清除。旧 secure 计数保留原记账，因此历史列不能相加宣称去重后的真实请求总数；已在升级前被覆盖掉的旧 metadata provenance 无法由这个回填恢复。

本批用历史负计数永久阻断；first/last 为全部已保存观察的首末时间。最近成功元数据不等于最后一次尝试；逐负类别时间、rotate 全入口、未能识别 token 的早期 decode/admission 拒绝、T0 后30天静默窗口仍未闭合。

## Observer 生命周期与资源

Server 挂 sink 前先持久创建 open run；失效/未结束的前序 run 保留 failed/gap。callbacks、sink 换代和关闭有 ownership/锁；旧 owner 不能卸载新 sink。正常关闭先阻止新 mutation 并 drain 后，只有无失败的本次 run 才 closed。写失败 sticky；poison 标记自身失败时，预先落盘的 open run 在重启后仍转为 unclean_shutdown。

换代/关闭占有 gate 时新请求直接503，不排队到预算之后再执行。Admin 已关闭实例同样拒绝新请求。没有全段可证明的监控时，全局 observer_continuity_unavailable 始终存在；clean restart 不证明此前与下次运行之间的静默窗口连续。

成员、facts、observer runs 和 import flags 每类最多4096，列表查询使用4097探测溢出后整体拒绝；计数限定 JS safe integer，时间限定可往返 UnixNano 且不能明显未来。旧负事实和 runs 不受一般 audit retention 清理，不自动截断或删旧gap。容量到限时明确失败/NO-GO，保管归档协议仍待实现，不能删表解除阻断。

新管理台只读消费该报告，明确显示 NO-GO/T0未设置、固定分母与追加、source lifecycle、lineage和完整 blockers。未知或失败清旧 snapshot；不能用演示数据、旧 contract1/v1 或一条 secure 观测补齐健康。审批/T0/真实处置与生产切换均不由页面执行。
