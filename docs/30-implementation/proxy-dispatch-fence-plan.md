# 受管上游派发 fence 清单

2026-10-07，第八波，补第七波 `ctx.Err → SetDeadline → command write` 与clear-deadline竞态。[屏障合同](device-proxy-startup-barrier.md)定义范围与未完成事项；生产未开启。

- [x] 单次 `AttachFenced` 同时交接已预留的真实上游连接及write/deadline fence，迟到连接仍属原quarantine，scope外连接保持原样。
- [x] permit获取与invalidate使用同一Gate决策锁；网络IO不持该锁，每conn最多1个write/1个普通deadline/1个revoker deadline，超容量立即拒绝。
- [x] 取消后未来/clear deadline拒绝；已获permit但迟到完成的deadline修复为past，随后command/relay write不可重新获permit。
- [x] Close实际返回且此前permit完成才允许Release/closed ACK；公开误用Release/Untrack不能擦掉fenced资源预算。
- [x] 同一真实HTTP/yamux/TCP fixture同步复现旧源码两个窗口失败，新实现通过，已有retained连接保持echo；阻塞Close/Write保持quarantine直至真实完成。
- [x] 同源schema、Windows race、Linux产品/WG门禁、十目标clean构建及实现/安全/运维文档收据完成。

取消前获准的Write是在途IO，不能撤回内核已提交bytes；closed ACK只约束本批登记连接、Close调用和permit完成，不证明yamux内部stream/closeTimer全部消失。contextual Open、每stream reset/内部容量、全部WG路径和持续撤销SLA仍是后续工作。

六项分别有Windows race、Linux实际产品新旧对照、最终Windows v13/Linux v3及源码3b5a2c5十目标clean构建/hash收据，见[本波worklog](../90-history/worklogs/2026-10-07-client-engine-observability.md)。中间失败仍保留，源码/编译不代称生产持续授权。
