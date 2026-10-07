# v2 真实代理启动屏障实施清单

2026-10-07，第七波隔离开发实施；生产未部署。[合同](device-proxy-startup-barrier.md)与[工作记录](../90-history/worklogs/2026-10-07-device-proxy-startup-barrier.md)记录实现和证据层级。原实际反例：初始reconcile移除第一旧peer后第二Snapshot失败，TLS不listen，但第二旧peer仍可经既有数据面通流。逐peer清理和Tick成功都不是默认拒绝屏障。

- [x] 固定受保护的v2 source scope、protected/unknown保留范围与实际代理listener归属；绑定authority policy/profile摘要、epoch/interface/managed-by。所有受管地址须被覆盖，重叠/含糊来源拒绝，不由一次grant决定需要阻断的范围；不自动覆盖legacy10.66布局。
- [x] 在产品zhreverse普通/striped CONNECT及fetch入口、原CIDR门之后/资源与upstream派发之前接默认关闭Admission；登记受管client/upstream流。新增先Reserve后Open/Attach/Abort，关闭gate时连pending预约一并quarantine；实际Close未完成不发closed ACK。测试target包一层拒绝不能算实现。
- [x] Linux私有UDS校验peer credentials与受保护namespace；receiver nonce/session owner、单调序列、有限消息/登记流/lease，失联/过期/坏消息/旧owner/迟到grant均关闭。无control默认关闭；真实进程重启证据单列下一项，不由纯New测试替代。
- [x] deviceapi Service先取得closed ACK，再initialTick和fenced最终desired/applied/outbox/runtime收敛证明，最后grant/Listen；失败/取消关闭controller、不继续grant。真实supervised内核Snapshot跨expiry复现Tick nil、generation2/applied1、旧peer仍在；proof callback0、三路径503。下一Tick实际移除后才收敛grant。
- [x] 实际产品proxy+WG正负对照：旧.2移除、第二Snapshot失败时.4虽仍WGpresent但三路径503/target0；protected.3与scope外unknown.7成功。原生firstSnapshot/remove失败同样live-WG拒绝；DB outbox done写入ABORT时peer已移除，单列nogrant/noTLS/target0，不称SQL commit故障或live-WG拒绝。实际receiver字节通道丢弃closed/grant ACK后拒绝并回收，Service专项另验证取消/ACK失败顺序。
- [x] 实际CONNECT后controller SIGKILL/UDS EOF关闭managed既有流、拒绝新流，retained既有流保持。Linux receiver/产品流专项覆盖lease到期、迟到旧grant、profile/scope/epoch及容量拒绝。reverse SIGKILL自然断所有旧socket；重启无controller时managed默认拒绝、retained重新连接成功、WG不变，不冒称retained旧socket跨进程存活。启用gate仅TCP/TCP-TLS，实际二进制默认/显式QUIC在load/bind前拒绝。
- [x] 冻结canonical合同/实现，Windows race/实际ACK负例与Linux真实进程/产品reverse/WG通过；v11统一门禁、Linux final-v2、clean十目标/631项源码hash一致，架构/运维/安全文档与worklog同步。本地源码 `8323ca1`，未签名compile_only，release_ready=false；不代称正式发行或生产迁移。

该片先闭合受管proxy路径，不自动关闭同WG接口其它INPUT/FORWARD、独立未受管proxy，也不证明持续设备授权或生产撤销SLA。RemoteAddr只有来源IP，无法把同一来源IP同时认作owned客户和unknown/admin公钥；此类hosting须拒绝或另做可证明的身份/namespace分离。完整WG屏障仍需实际产品的隔离interface/原子scoped内核规则和启动单writer，不由proxy测试代称。

WSL owned user/net namespace内已运行内核WireGuard、veth、实际reversebinary/UDS与独立peer验收；没有安装宿主/生产规则或改宿主路由。root启动本地测试仅用于保留root祖先owner映射，未放宽产品reader。真实清册、持续会话、恢复、Mac/手机、发行和日志容量继续按[总计划](zhvpn-steelman-refactor-plan.md)验收，G01/G05不因本清单勾选。
