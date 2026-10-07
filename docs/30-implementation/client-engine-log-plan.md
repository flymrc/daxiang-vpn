# 受管客户端引擎日志实施清单

2026-10-07，第八波本地实施，基于 `82f69c0`。本片补 P6.4 的客户端部分，不代称 Hub journald、Android supervisor、诊断包或 Mac 实机验收。原 Windows 后台 child 未接 stdout/stderr，错误丢弃；Darwin 将两个原始日志无限 APPEND。初始化保留日志已在前波完成，不重复计入。

## 合同与边界

只在 CLI hidden dedicated engine 启用安全日志；一般 `RunEngine` 库调用不修改进程全局输出或创建新日志。`logs/engine-events-v1` 为独立私有 namespace，旧 `zhvpn*.log` 和未知文件不写、不删、不改变权限。单 writer、4 个自有槽位，每槽实际上限256 KiB减64 bytes，为最大256 bytes的owner/lock marker留出空间，整个自有namespace不超过1 MiB；每条最多2048 bytes。不是整个 home 或旧历史日志的总容量承诺。

日志只接受枚举事件/错误码与有限公共 instance/generation/build引用；没有自由message、原始error、路径、完整config/control record或credential字段。version/sequence/time由存储生成；本地时间不是外部验证时间。schema由存储codec同源生成并检查漂移。本片不改数据库/Device HTTP/OpenAPI；CLI v1新增可缺省的logging状态字段。

sing-box `PlatformLogWriter` 会隐式开启Cache/Clash服务，与本产品最小registry不兼容，因此不接该adapter。专用child将stdout送永久null，stderr经匿名pipe中的固定前缀计数器，依赖warning/error只保存固定类别和计数，原文直接丢弃，不做可能跨块漏秘密的文本替换。专用child强制sing-box日志为无色、无时间前缀的warn级stderr，覆盖任何外部 `Log.Output` 文件路径，不能绕过到依赖0644无限追加。全局输出隔离仅在专用子进程入口发生，不借此改变普通库调用。

保留现有signed控制v1响应字节与生命周期/代理恢复行为；日志health走独立认证只读 `/v1/log-status`，绑定command/nonce/完整expected instance身份/MAC。CLI `status --json` 增可选 `logging_state=healthy/degraded/unknown` 与 `logging_error_code`；旧engine/未取得可信响应为unknown，不加强进程、隧道或出口证据。runtime日志失败sticky degraded，不把内部ready改为degraded或破坏OS代理恢复gate。

生命周期事件只进入64项有界队列，唯一worker写盘，不创建替代worker。满队列给固定sticky错误码；health读取不等磁盘锁，正常写盘在途为unknown。收尾预算与专用child已授权取消后的3秒自退出监督分别验证；不把磁盘阻塞时的有界返回称为已flush，也不提前释放旧writer的生命周期归属。

## Checkbox

- [x] 实现同源codec/schema、有限typed事件与健康状态，拒绝任意文本/秘密/畸形或过长公共元数据。
- [x] 实现私有namespace、单writer、NOFOLLOW/owner/ACL/单hardlink/固定目录与文件身份；未知现有内容拒绝，不修权限或认领历史日志。
- [x] 自有槽位轮转与总量预算，持久失败sticky；覆盖partial尾/轮转中断/重新打开，不删除未证明归属的文件。
- [x] 专用child接线typed启动/初始化/运行/退出事件与build引用；原始多行/跨块输出丢弃，一般RunEngine与CLI JSON行为保持。
- [x] 独立认证日志health RPC与CLI/SDK/GUI同源投影；旧控制MAC兼容、旧engineunknown、伪造/错实例/迟到响应拒绝。
- [x] Windows实际后台child和sing-box故障留存；真实安全文件/容量/并发/失败负例，Linux实际运行与Darwin交叉编译分别登记。
- [ ] 冻结统一门禁、clean开发构建/hash、架构/安全/运维文档和worklog，本地提交收据。

不承诺磁盘/内核IO硬截止、SIGKILL/panic/断电前最后记录已flush，或恢复全部旧日志。实际Mac运行、Hub/手机日志、跨组件operation/出口指标与完整诊断包仍独立验收，P6.4/P6.G保持未完成。

前六项证据为[本波worklog](../90-history/worklogs/2026-10-07-client-engine-observability.md)的冻结Windows v13和Linux v3结果；构建/提交收据完成后才勾最后一项。
