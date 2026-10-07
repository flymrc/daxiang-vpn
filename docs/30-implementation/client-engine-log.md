# 专用客户端引擎事件与日志健康

2026-10-07，第八波隔离开发实现；16:18–16:21 JST本机Windows CLI已做未签名dev canary部署并通过认证ready/logging healthy及真实出口请求，见[部署记录](../90-history/worklogs/2026-10-07-wave8-compat-deployment.md)。尚未正式发行或全面分发。对应[实施清单](client-engine-log-plan.md)与[工作记录](../90-history/worklogs/2026-10-07-client-engine-observability.md)。普通 `RunEngine` 库调用保持原有输出与生命周期，只有CLI隐藏后台child启用本合同；Linux客户端后台启动仍不因此实现。

## 持久合同和归属

`shared/proxy/engine_log.go` 的typed record、事件/错误码/计数配对为维护源，`EngineLogSchema()`确定性生成 `engine-events-v1.schema.json`。`go run ./shared/proxy/cmd/logschemagen -check`只读核验；不带check显式生成。codec要求Go struct紧凑JSON和LF framing，拒绝未知/重复/缺失/null/大小写别名/非canonical编码。普通门禁不因继承环境变量而修改schema。

记录固定字段为version=1、sequence、time_unix_nano、event、code、build_id、instance_id、generation、count。事件为starting/control_bound/initialized/ready/stop_requested/stopped/failed/dependency_warning/dependency_error/raw_output_suppressed。前三种依赖类别只记有限计数，其他事件count=0；sequence/count不超过JS safe integer。时间由本地存储生成，不能作为外部验证时间或单调墙钟承诺。

没有message、原始error、路径、config、完整control record或credential字段。build_id为空或完整小写十六进制公共源码引用；CLI仅在source_state=clean且完整40字符SHA合法时传入，dirty/unlabelled为空。instance/generation为空或既有固定长度摘要。日志不是由build_id建立发布者信任或签名收据。

独立 `logs/engine-events-v1` 包含owner.v1及events-0.v1..events-3.v1，owner同时为跨进程锁。4槽每槽实际上限256 KiB减64 bytes，预留总256 bytes marker，整个自有namespace最多1 MiB；单条包括LF最多2048 bytes。旧 `zhvpn*.log` 或其他历史日志不打开、不轮转、不删除、不改权限；整个home或磁盘不受本容量约束。

Unix逐段NOFOLLOW打开并固定目录句柄，重新核对owner/mode与目录/文件身份；系统root-owned sticky临时祖先为明确例外，home/logs不得允许其他账户修改，leaf和文件不允许group/other访问，文件为单hardlink普通文件。Windows拒绝reparse与多hardlink，验证owner/DACL与持有句柄身份，新对象使用私有ACL，不自动修复既有宽权限。相同OS身份仍在本地信任边界内，不能防该身份直接改自己的日志。

重新打开先验证全部自有header/记录与namespace绑定，再允许裁掉有限的不完整尾记录；完整坏行、未知条目、未证明归属的空文件拒绝。首次创建中断留下不完整namespace可能需要受控人工核验，不承诺所有断电窗口均自动恢复。文件错误sticky；轮转不自动扩大配额、重新认领文件或修权限。

## 原始输出与磁盘隔离

专用child的stdout永久转null，stderr转匿名pipe。单个固定buffer reader丢弃原文，只按强制无色、无时间前缀的 `WARN[` / `ERROR[` 计数；跨块、多行、ANSI、非法UTF8、长消息只成为匿名suppressed计数。sing-box日志强制warn级stderr，覆盖外部Log.Output，避免依赖原始文件无限APPEND。没有使用PlatformLogWriter：该入口会隐式启用Cache/Clash，不兼容产品最小registry。

Windows同时退休原标准os.File句柄并重绑Go默认logger，避免init时缓存旧stderr的logger绕过新通道。同一个有效HANDLE被两个不同os.File包装时，无法安全同时关闭包装，专用child固定返回engine_capture_failed，不启动控制/日志namespace或依赖代码；同File指针重复仅退休一次。Unix标准fd1/fd2由dup2转到sink，替换过的其他Go输出对象单独退休。这不是对任意第三方预先DuplicateHandle或其他非标准输出渠道的沙箱承诺。

生命周期线程只向64项有界队列尽力追加；单writer执行文件IO，不为卡住写入启动替代worker。原始pipe reader独立于磁盘，不因logger失败保存消息。队列满发布 `engine_log_queue_overflow`，不暂停引擎、改变ready或绕过系统代理BeforeStop恢复门。安全日志namespace初始化失败拒绝专用child启动；运行期写入失败单独degraded。

health读atomic快照，不等文件锁；正常文件操作在途为unknown，不能因为unknown丢弃生命周期事件。首次固定失败保持sticky；正常shutdown等待唯一worker收尾后才释放control/lifetime归属。有界收尾超预算为 `engine_log_shutdown`，不称已经flush。已授权取消后原worker未回收时继续持有旧归属，由专用child外层3秒自退出监督覆盖。一般库调用从不被该监督杀进程，BeforeStop失败也不授权自退出。

不承诺磁盘/内核IO硬截止、SIGKILL/panic/断电前最后记录持久保存、历史完全连续或原始栈可恢复。日志仅保留安全类别；需要更详细故障原因时另设计受控诊断，不要求用户粘贴原始秘密配置。

## 独立认证观测

既有signed EngineStatus和控制v1生命周期MAC不改。新增loopback只读POST `/v1/log-status`，请求/响应各用不同purpose-domain HMAC，绑定version=1、command=log-status、fresh nonce、完整expected EngineIdentity；body最多2048 bytes，客户端1秒预算、不跟redirect或代理。读取前后重新核对private control record，错实例/坏MAC/迟到替换/malformed为unknown或固定拒绝；旧child404为unknown。

CLI JSON v1新增可缺省logging_state和logging_error_code，维护源仍为 `shared/contracts/source.go`；Go/GUI/Rust/Python消费者共享schema/fixture。healthy/degraded只对当前可信starting/ready/stopping身份有效，degraded必须有有限固定错误码；旧字段running/engine_state与网络证据不由logger改变。一般库引擎没有本sink时为unknown。普通status不隐式bootstrap或对Hub发请求。

日志healthy只说明当前sink观测，不能说明整个历史完整、引擎/代理/WG/出口健康或业务成功。Windows实际后台CLI、真实sing-box故障、protected namespace/轮转、blocked IO和独立RPC分别验收；Linux本地原生与Darwin交叉编译分别记录。Mac实机、Hub/Android日志、operation/出口关联、诊断包与P6.4整体仍未完成。
