# v2 真实代理启动屏障实施清单

2026-10-07，第六波之后的下一安全片；仅计划，尚未实现或部署。原实际反例：初始reconcile移除第一旧peer后第二Snapshot失败，TLS不listen，但第二旧peer仍可经既有数据面通流。逐peer清理和Tick成功都不是默认拒绝屏障。

- [ ] 固定受保护的v2 source scope、protected/unknown保留范围与实际代理listener归属；绑定authority policy/profile摘要、epoch/interface/managed-by。所有受管地址须被覆盖，重叠/含糊来源拒绝，不由一次grant决定需要阻断的范围；不自动覆盖legacy10.66布局。
- [ ] 在产品zhreverse普通/striped CONNECT及fetch入口、原CIDR门之后/资源与upstream派发之前接默认关闭Admission；登记受管client/upstream流，关闭gate时关闭登记流。测试target包一层拒绝不能算实现。
- [ ] Linux私有UDS校验peer credentials与受保护namespace；receiver nonce/session owner、单调序列、有限消息/登记流/lease，失联/过期/坏消息/旧owner/迟到grant均关闭。无control和reverse重启仍默认关闭。
- [ ] deviceapi Service先取得closed ACK，再initialTick和fenced最终desired/applied/outbox/runtime收敛证明，最后grant/Listen；失败/取消不grant。中途expiry可能新增removal generation而Tick忽略ErrExpired返回nil的组合仅为源码推导，必须实际复现并验证proof不放行。
- [ ] 实际产品proxy+WG正负对照：旧.2移除、第二Snapshot失败时.4虽仍WGpresent但受管proxy零目标流量；protected.3与scope外unknown.7继续可用。firstSnapshot/remove/verifySnapshot/DB结果提交失败、closed ACK丢失与取消分别覆盖。
- [ ] actual CONNECT后controller SIGKILL/UDS EOF/lease到期/迟到旧grant/reverse重启，验证登记流关闭且无关流不受影响；profile/scope/epoch错误和容量越界拒绝。
- [ ] 冻结canonical合同/实现，Windows合适负例与Linux真实进程/产品reverse/WG通过，统一门禁/clean构建/架构与运维安全文档收据后本地提交。

该片先闭合受管proxy路径，不自动关闭同WG接口其它INPUT/FORWARD、独立未受管proxy，也不证明持续设备授权或生产撤销SLA。RemoteAddr只有来源IP，无法把同一来源IP同时认作owned客户和unknown/admin公钥；此类hosting须拒绝或另做可证明的身份/namespace分离。完整WG屏障仍需实际产品的隔离interface/原子scoped内核规则和启动单writer，不由proxy测试代称。

当前WSL普通uid无cap，但owned user/net namespace实查可用、nft读取成功；尚未安装规则或改宿主路由。真实清册、持续会话、恢复、Mac/手机、发行和日志容量继续按[总计划](zhvpn-steelman-refactor-plan.md)验收，G01/G05不因本清单勾选。
