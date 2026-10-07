# Legacy Hub 控制面执行预算

本切片约束现有 token/YAML 兼容入口的本地 WG/SSH 执行，并把明确 listener 的 HTTP 准入接到兼容、可信代理与管理台。它不迁移 token 权威、不改变 v2 device executor，不证明旧 peer ownership、跨 Hub sole-writer、生产容量或远端换 IP 完成。生产未部署这些改动。

## 本地合同

`BootstrapHandler` / `RotateIPHandler` 从固定 ingress 选择 Direct/LoopbackProxy source policy，共享 `Server.HTTPAdmission()`；管理台使用同一实例。普通 health/snapshot 不因执行名额耗尽而伪报故障。候选准入是 global/source 在飞 8/2、global 每秒20且 burst40、source 每秒1且 burst4；source 表容量1024、闲置回收10分钟。拒绝不排队，429 `rate_limited` 或503 `resource_exhausted`；预取消返回503 `request_cancelled`，不消费准入额度。所有数值是本地默认候选，不是生产性能承诺。

request context 贯通 WG、carrier、client/admin RotateEgress。bootstrap 协作 context 为5秒，本地 WG命令最多3秒；carrier最多1.5秒，继承调用方剩余时间。client rotate 协作 context为18秒，SSH dispatch最多15秒，ConnectTimeout=8仍只约束建连。管理台从自身 request context派生相同18秒执行预算。子进程 budget从申请名额前计时，WG在飞1个、SSH共享2个，不建立无界队列。

`ZHHUB_TOKEN_LEASE_SECONDS`、`ZHHUB_ROTATE_LOCK_EXTRA_SECONDS`、`ZHHUB_ANDROID_CARRIER_CACHE_SECONDS` 在秒数乘为duration前严格限定0..86400（24小时）。保留0的原有停用/不追加延迟语义；非法、超过上限或整数溢出均回默认30/45/300秒，日志只记录固定配置名和默认秒数，不回显原值。

bootstrap 在真实 HTTP driver 上设 handler开始后5秒的 body read deadline；owned TCP慢请求负例验证不完整body会拒绝且不启动native helper。不支持 ResponseController 的 synthetic ResponseWriter只证明context/执行约束，不证明阻塞ReadAll会被context打断。其余 body/header/socket 边界由共同HTTP server承担（read10秒、header5秒、write/idle30秒），context自身不是socket超时。

`processbudget.Runner` 只接结构化 executable/argv，参数不经本地shell解释，stdin为空。stdout与stderr累计上限4 KiB，超过后取消；仅成功的 carrier stdout用于现有显示名探测。stderr不保存、不回显；HTTP、错误、审计、日志只给固定类别，不输出命令行、安装/密钥路径或原始子进程输出。原有成功JSON、WG失败502 `wireguard_peer_apply_failed`、rotate失败502 `control_failed`和busy409语义保留。

carrier cache限制64个记录/在飞key、最多16个singleflight。全局mutex只保护cache/flight状态，外部SSH不持锁；相同地址等待可以取消，不同地址可以独立探测。成功和空结果都缓存，缺控制密钥、探测失败或预算拒绝仍使用配置显示名。

## 原生进程所有权

Windows先创建Kill-on-close Job，子进程以CREATE_SUSPENDED启动，入Job后再检查取消并resume。执行/取消持有本次Job，关闭与终止按mutex排序，避免句柄复用误杀。cleanup只有在direct child已Wait、Job active process为0时确认。

Linux需要pidfd与procfs；安装的binary和祖先只信任当前UID/root，拒绝非可信可写路径。当前Hub二进制的固定internal子命令运行独立helper：fd3是本次parent lifetime，fd4是cleanup receipt，不打开数据库或借用v2 authority/fence。helper独立subreaper枚举自己的全部thread children，pidfd终止并收回本次descendants；启动真实命令前同步检查lifetime pipe。receipt `C2`只在实际命令尚未启动且已确认拒绝时返回，`C0`/`C1`分别表示已启动且清理确认的成功/失败；丢失receipt不能当作未启动。父Hub固定helper私人PG，waitid WNOWAIT保持未reap身份，helper正常退出或自身被杀后先清普通PG，再Wait，避免PGID复用。普通子树、脱离session子树、parent死亡和helper被杀使用真实合成native进程验证。

取消只关闭本次lifetime或终止本次Windows Job。清理最多额外等待500ms；无法证明完成时返回 `process_supervision_unknown`，该名额在本进程内quarantine，受控monitor继续持有handles并等待真实reap，不提前释放，也不假称树已终止。失去procfs观测/不可终止kernel work可能使cleanup长期pending。Linux helper被毁同时descendant已经detached时，parent不能可靠认领/终止该detached树，结果unknown、席位quarantine；测试对私有逃逸fixture另用其owned pidfd清理，不能把此清理算作运行库保证。不存在“超时一定停掉所有本地/远端工作”的承诺。

## 启动前补偿与结果未知

已观察到的取消、输入拒绝、预算不足或missing executable在启动前拒绝。bootstrap claim使用单调版本收据与共享pending前驱节点；明确未启动的WG失败先标记自己的节点failed，即使当前版本已更新仍保留这一事实。只有自己是当前版本时才补偿，并跳过所有failed前驱，恢复精确之前状态；不会删除更晚的claim，也不会在两个请求都拒绝后复活较早失败的lease。成功或Started后的未知结果commit自己事实并截断前链；pending链仅保留实际在飞请求关联节点。invalid key/pre-cancelled不认领lease，非法配置WG地址返回固定pre-start拒绝。真正启动后的错误保留已有lease事实，不能把可能已应用的WG操作当作没执行。

rotate先预留本地锁；明确pre-start失败才释放。启动后的非零exit、输出洪泛、超时或无分类trigger错误都标unknown。unknown在本进程内不随普通cooldown过期重新启动，snapshot明确Unknown，管理台投影 `rotate_state=unknown`并禁用再次触发。没有安全clear API；进程重启丢失该内存标记不等于远端结果确认。

手机rotate脚本故意setsid后台执行，以确保airplane OFF不随SSH断线被取消。本地SSH超时不是“未派发/已回滚”证据，禁止自动重试，绝不杀远端恢复脚本。完整跨重启unknown保管、remote RID/receipt/互斥与确认后解锁属于后续维护协议，不由本切片提供at-most-once保证。

## 本地验收与边界

原生fixture均为本次owned test binary或新建私有文件，无真实WG、SSH、OS代理或生产连接。processbudget测试覆盖pre-cancel/missing executable/容量拒绝、成功与非零exit、stdout/stderr洪泛、ordinary/detached子树、held pipe、unrelated保活、parent死亡、Linux helper SIGKILL及无receipt quarantine、cleanup pending回退。auth负例用真实native fake WG验证失败/洪泛/超时与秘密不回显，并覆盖lease双拒绝/三请求乱序/较早或较晚commit/原expired状态恢复、配置上界和溢出、未知rotate到期仍阻断、carrier单飞/等待取消/其他key不被全局锁阻塞，以及owned TCP慢body不派发。

Windows race与Linux普通测试分别记录；Linux环境没有C编译器，不能称Linux race已通过。本地预算、helper和准入证据不能替代生产容量、外部SSH host identity、远端派发回执或实际生产迁移验收。P4.5的设备/会话租约身份与所有产品入口整体验收仍需后续收口。

2026-10-07冻结后验证：`go test -race ./hub/internal/processbudget ./hub/internal/auth ./hub/admin/internal/api ./hub/admin ./hub -count=1` 五包通过，分别8.499/8.370/2.544/4.301/1.891秒；Ubuntu-24.04 Go1.26.7普通同组通过，分别6.733/6.594/0.074/1.879/0.004秒。Windows同组`go vet`和`git diff --check`通过。独立Linux私有overlay验证在helper实际Start前已关闭lifetime时精确C2且无启动marker，open-life正对照C0与marker通过。

独立复核者在最终冻结后复跑原双拒绝幽灵lease反例，Windows `-race` auth1.709秒通过；同overlay的实际准入容量负例1.458秒通过，正式native四测试文件Windows `-race`8.554秒/WSL普通6.724秒通过。该局部复核未保留已知P0/P1；不代表整个Steelman或远端协议完成。
