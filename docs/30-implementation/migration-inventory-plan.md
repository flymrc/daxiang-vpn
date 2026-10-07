# 固定迁移清册与持久观察缺口实施计划

2026-10-07 JST；第六波，基点 `3130109`。仍在隔离开发树实施；当前授权事实源仍是 TokenStore，生产保持 observation-only/NO-GO。本批交付 provisional 清册、完整阻断报告及真实管理台消费者，不自动开启 campaign。

既有独立实际 SQLite/HTTP 反例：实时 Resolve 过滤导致删除/停用/到期对象掉出分母；latest secure 掩盖历史 compat/legacy；有效 token 的失败请求只写 audit；观察写失败标志在重建 Server 后归零。修复必须逐项反转这些失败断言，不能仅增加报告字段。

- [x] canonical Admin OpenAPI/sqlc 定义 provisional inventory、固定基线/追加未知对象、source lifecycle、安装 lineage、单调历史、observer run/gap 与逐成员/global blockers；确定性生成 Go/TypeScript 并消费。
- [x] 显式人工批准原始 inventory SHA256 后才允许离线登记。严格有限 JSON、唯一稳定 token ID 与非秘密 opaque owner/installation 引用；初始清册不可替换或删除，相同批准摘要可幂等重开，变化的摘要拒绝。没有清册时仍报告已知对象与 inventory_not_registered。
- [x] 固定 baseline 数量与成员不因 token 删除、停用、过期或损坏 expiry 消失。新出现但未登记的 token 保留为追加 unknown/unregistered，source 与 history 分开；拒绝同一次 source 快照中的别名/重复短 ID，不能自动认领安装实例或撤销 disposition。跨历史快照的短 ID 碰撞保管链仍是余项。
- [x] bootstrap 成功/已知 token 失败、legacy/compat/unknown 均保留单调历史计数及首末观测时间，后来 secure 不清洗旧事实。版本格式严格校验；版本自报和 secure_bootstrap 不表示批准的发布或真实安装合规。本批按历史负事实永久阻断；逐负事件时间、T0 后连续静默窗口及 rotate 全入口观测另验收。
- [x] 挂 audit sink 前先持久打开 observer run。失败/崩溃/未完成旧 run 永久保留缺口；关闭只在本 run 无失败、无在飞写入时记 clean，sink 换代有归属与并发保护。不得依赖故障事务内还能成功写 poison，重启不能把失败洗成健康。
- [x] T0 恒 null、campaign_configured=false、ready=false；release、双次 E2E、lineage、处置/数据面撤销、连续观察、恢复均逐项阻断。没有人为设置 T0、批准当前 unsigned 构建、缩短30天窗口或自动更改 token/peer 的入口。
- [x] Admin 增加只读迁移页面并消费同一 canonical DTO；完整显示固定分母、追加未知、失效/缺失成员、历史负事实与 observer gap。畸形/失败/迟到403清除旧快照；secure_bootstrap 明确不等于 compliant；无批准或 T0 按钮。
- [x] 独立真实 SQLite/HTTP 故障、关闭/崩溃重开、登记并发/摘要变化与敏感输入负例通过；消费者负例及实际编译 Chrome 合成 API 验证；统一本地门禁、clean开发构建、相关文档和当日 worklog 完成后本地提交。

不把本批当作生产 campaign 启动、token→v2 device 的真实保管链、全量授权导入、观察窗口或最终 Go 审批。Mac/手机、持续会话撤销、WG/proxy 部分启动失败屏障、日志容量与签名发行继续按[总计划](zhvpn-steelman-refactor-plan.md)实施。原始证据留在产品 Git 树外的私有 `.local/zongheng-vpn/steelman/2026-10-07/`。

勾选证据：最终 Windows 四包 race/Linux native、独立正式10顶层/6子例和9原生子进程通过；旧 metadata 洗白反例先失败、修复后全套复验。Admin43消费者、实际编译Chrome空/缺口/失败/迟到响应/21条分页/五旧页签通过；真实分页先失败、修复后重验。最终冻结v8统一门禁exit0，canonical生成漂移通过。clean开发构建十目标/hash一致已登记，详见[本批 worklog](../90-history/worklogs/2026-10-07-migration-inventory.md)。
