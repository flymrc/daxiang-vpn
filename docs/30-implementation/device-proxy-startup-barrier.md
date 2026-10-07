# v2 真实代理启动屏障

2026-10-07，第七波隔离开发实现；生产尚未开启。对应[实施清单](device-proxy-startup-barrier-plan.md)与[工作记录](../90-history/worklogs/2026-10-07-device-proxy-startup-barrier.md)。本合同新增内部 Go 控制协议和受保护文件，不改 Device HTTP/OpenAPI 或数据库 schema。

## 归属和固定范围

`shared/proxygate.Policy` 与生成的 `proxygate-v1.schema.json` 是 canonical 合同。version=1；epoch、interface、managed_by、authority policy SHA256、profile SHA256、固定 IPv4 listener、controller_uid、managed_sources、retained_sources 全字段绑定。读取只接受该 Go struct 的紧凑 `json.Marshal` 字节；pretty JSON、末尾换行、重复/缺失/null/大小写别名/未知字段拒绝。摘要是内容标识，不建立安装者或发布者信任。

文件为操作者控制的绝对普通文件，最多32768 bytes，单 link、当前 owner、无 group/other 权限；父目录逐段 NOFOLLOW 固定句柄并核对 identity。私有leaf建议0700，reader实际要求当前owner且没有group/other权限位，并非精确匹配0700。既有 hosting authority policy 也使用有限、精确 canonical 编码；旧离线 policy 命令的输入合同保持原定义。不能通过 chmod 源文件、删除锁或自动登记新 scope 绕过拒绝。

managed_sources 与 authority allocation pools 完全一致且不重叠，protected prefix 不得落入 managed scope。retained_sources 记录显式保留范围，所有 managed scope 外来源也保留。普通 IP 来源不是设备公钥：runtime unknown/protected key 与 managed 地址重叠时，最终收敛证明拒绝。不能把同 IP 的管理员/旧公钥和客户同时区分。没有沿用生产 10.66 布局的默认值，也不自动接管 wg0。

## 产品入口与控制

zhreverse 新增 `--proxy-gate-policy-file` / `--proxy-gate-control-socket`（YAML 对应 snake_case），必须成对。实际绑定 listener、启动选项、Policy.listener 精确匹配。两项都未配置时，旧入口保持既有行为；配置一半或不可信文件时启动失败。`proxy_gate_enabled=true` 仅说明配置启用，不是流量就绪收据。

启用 gate 仅支持 `tcp` / `tcp-tls`。默认或显式 QUIC 在读取 policy、绑定 listener/控制 socket 或创建 TLS 文件前返回 `proxy_gate_unsupported_transport`；gate OFF 的既有 QUIC 行为不变。原因是当前 QUIC stream Close 仅关闭发送方向，不能用它证明登记流双向实际回收。

普通 CONNECT、striped CONNECT、诊断 fetch 在原 CIDR 门之后、限额/上游 command 派发之前获得 Admission。受管来源默认关闭；保留来源沿用原 CIDR/业务限制。真正 net/http accepted socket 与 yamux upstream 登记到同一 Admission，upstream 必须先 Reserve 再 OpenStream、Attach 或 Abort；失效后的迟到连接不能发送 command bytes，未完成预约也阻断新 owner 的 closed ACK。受管来源不使用 tunnel-bench 诊断派发入口，保留来源的既有诊断继续可用。

上述迟到拒绝指Attach/请求已观察到取消或ErrClosed，以及closed ACK后旧登记流不再派发。ctx.Err检查与后续SetDeadline/command写入尚无原子write fence；取消可发生在检查后，仍有在途command或deadline覆写窗口，其资源继续归原quarantine。不能将invalidate时刻承诺为瞬时零在途bytes；严格派发fence与撤销SLA留后续transport子片。

Linux 控制 UDS 位于当前owner的私有目录（建议0700，无group/other权限）；校验路径归属、固定namespace和双端peer credentials。controller UID属于安装合同，当前receiver/controller必须使用同一有效UID，Policy.ControllerUID须等于各自os.Geteuid()，不能用配置授权另一个UID。receiver发新session/nonce，绑定完整Policy digest；controller单调sequence、精确有限消息不允许重放/旧owner/迟到grant。UDS不提供远程管理员API；Windows/Darwin hosting返回unsupported。

控制命令预算1s、消息最多1024 bytes、最大 lease 5s；初始 closed idle budget40s。Hub 使用2s绝对截止 grant，每500ms重复 Tick 与最终 proof；deadline 不得超过任一 active device/current credential 的期限。GrantUntil 传绝对时间，sender 迟到和 expired ACK 拒绝；不是收到请求时重新加 TTL。

## 启动、续期与失败

profile-enabled deviceapi Open 要求 `ZHHUB_DEVICE_PROXY_PROFILE`、`ZHHUB_DEVICE_PROXY_GATE_POLICY`、`ZHHUB_DEVICE_PROXY_GATE_SOCKET` 同时存在且与 authority/interface/profile 绑定。profile-OFF legacy Service 顺序保持。新顺序为：

1. 与实际 receiver 完成 fresh closed ACK。
2. 初始有界 scheduler Tick。
3. 持有 pinned execution fence 与 SQLite BEGIN IMMEDIATE，核对最新 desired/applied、当前完成 outbox/intents、binding/reservation、历史 revoked key/tombstone，以及实际 WG Snapshot；范围内未知身份或未完成 generation 均拒绝。
4. fence 保持至短期 grant ACK、期限复查和 SQL commit，随后才 TLS Listen。续期重复完整证明。

Tick 返回 nil 不是 readiness。expiry 发生在 Snapshot 后可能新增 removal generation、Tick 忽略 ErrExpired；proof 必须另查最新 generation/runtime，不能直接 grant。执行/核验/提交失败或取消关闭 controller、停止本次 HTTP/scheduler，不全接口关停，也不碰保留 peer。

receiver观察到controller EOF、租约到期或坏消息后立即invalidate，取消登记context、设置全部登记socket deadline，拒绝受管新请求。最多4096 Admissions、每项4个资源（含预约）、一个有限cleanup batch、16个cleanup workers。Close返回只表示失效；AwaitClosed成功才确认本批登记连接的Close调用与待创建预约完成。yamux FIN物理发送受阻时可超过1s；控制端无closed ACK、返回 `proxy_gate_cleanup_unknown` 或连接失败，保持quarantine，不允许通过重连/重复Close新建无界任务或重新放行。外部retained stream和整个reverse session不因此关闭；reverse进程死亡则自然断所有旧socket。

Admission 容量只约束 gate 登记，不覆盖所有 HTTP Accept/等待 header、legacy TCP reverse session 或 scope 外流量的全局容量。产品 factory 保证单 Gate/单 receiver；多个 ControlServer 共用 Gate 不是本合同。登记连接的 SetDeadline 遵守非阻塞连接语义，不能由任意自定义阻塞 adapter 推导硬截止时间。

不可取消的底层 Close/文件 I/O 仍有系统可用性边界；quarantine 不等于全部资源已回收。grant 在 SQL commit 之前实际可见，ACK 后 commit 失败的窗口由 EOF/lease 关闭；本片不承诺该窗口零 upstream bytes，也不声称跨主机实时原子提交。

hosting 每个 Service 只允许一个 Run/coordinator writer；并发复用 Run/手动 Tick 不在本生命周期证明内。500ms周期先完整Tick，合法慢Tick超过约1.5s也可能耗尽2s lease并使Service停止：正确拒绝不等于负载下可用性已验证。Enrollment是持久数据面授权；仅在存在current HTTP credential时，用其更短期限限制grant。0行credential不会自动撤销设备WG，不能把单独credential撤销或logout当作device revoke。

TCP/yamux的实际Open适配器仍调用不接收context的库Open；10s OpenCtx只是调用者预算，不能证明实际创建10s完成或立即取消。库SYN等待/发送预算可能达到75s/30s。预约保持到真实Open返回并Attach或确认无连接的Abort；超时关闭前台流、保持quarantine、不发假ACK，不能以关闭整个共享session强行证明清理，因为其中有retained流。持续创建/回收SLA与库适配仍在后续工作。

yamux v0.1.2的Stream.Close完成本地FIN发送调用后，若对端不回FIN，内部stream/closeTimer仍可保留至默认5min close timeout。closed ACK不证明这些内部对象或缓冲全部消失；4096×4是产品登记/预约/待Close调用预算，不是库内部对象或历次授权的全局容量。实际应用读写由deadline/context终止，owned per-stream reset与内部容量验收仍需后续transport子片。

## 验收与明确限制

正式原生 fixture 构建实际 zhreverse server/client、executor/helper，在 owned user/network namespace 内使用内核 WireGuard/veth 和独立客户 namespaces。初始 closed 时真实普通/striped/fetch 必须 HTTP503、owned target 计数不增；部分实际撤销后第一旧 peer 不在、第二仍在 WG，第二仍通过 live handshake 得到503，protected/unknown 的目标响应保持成功。另一链路完成真实收敛 grant，controller EOF 关闭已建 managed CONNECT、新请求拒绝，已建 protected echo 继续。

同一原生WG fixture还覆盖实际Snapshot后推进注入Store时钟：Tick nil但generation2/applied1、旧peer仍在，proof callback0；下一Tick实际移除后才grant。firstSnapshot/remove故障保留live-WG而三路径503；DB outbox done写入ABORT时peer已移除，证明nogrant/noTLS/target0，不称SQL COMMIT故障。实际controller SIGKILL保留retained已有echo；reverse SIGKILL/重启只证明retained新连接恢复、WG不变。

实际receiver字节通道覆盖closed/grant ACK丢失；Linux UDS+产品handler+实际yamux/TCP echo专项覆盖lease到期（非同一独立binary/kernelWG fixture）。其他合同层分别覆盖取消、错误scope/epoch/profile、容量、replay/迟到和Close卡住。真实yamux FIN拥堵必须无假ACK；释放owned stall后才允许freshowner。Windows race是共享状态和拒绝/消费者证据，不能冒充Linux peer-credential/WG验收。运行 `scripts/check-proxy-barrier-linux.sh` 与Windows统一门禁分别登记。

该片只关闭这个受管 proxy route。其他 WG INPUT/FORWARD、独立未受管 proxy、同来源 IP 的公钥隔离、所有设备会话持续授权、生产撤销 p99/SLA、跨备份恢复、Mac/手机、发行和正式 campaign 未闭合。G01–G05、P4.G 和生产迁移不因本片通过而勾选。
