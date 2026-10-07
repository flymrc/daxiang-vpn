# 2026-10-07 真实 proxy 启动屏障

第七波基于第六波源码 `bf0f608`、收据 `2704141`，继续在隔离工作树实施。[合同](../../30-implementation/device-proxy-startup-barrier.md)和[checkbox](../../30-implementation/device-proxy-startup-barrier-plan.md)分别说明行为与验收。没有推送、部署、改生产peer/token/入口、设置T0、连接Mac/手机或跑Jetstar；原工作树的79个dirty文件继续保护。

## 原因与实现

第五波真实反例中初始Tick先清掉旧.2，再Snapshot失败，TLS虽不listen，仍存在的.4却可经外部旧proxy访问目标。因此仅API启动门不够。新增canonical proxygate policy/control v1、Linux私有UDS、真实reverse普通/striped/fetch Admission与Hub closed ACK→Tick→最终fenced DB/WG proof→绝对短期grant→TLS。profile-only hosting现在明确拒绝，legacy默认OFF。

独立审查发现yamux Stream.Close的FIN可受物理写入阻塞：仅设置stream deadline不能声称Close有限或已回收。修为立即失效/取消/设置全部deadline、一个有限quarantine batch与16workers；AwaitClosed超时不发closed ACK、不给新owner/grant。再发现OpenStream尚未返回时不在旧登记表中，增加先Reserve后Open、Attach/Abort；pending预约也计入同一quarantine。受管source不能经tunnel-bench绕过上游屏障，保留诊断和无关流保持原限制。

## 独立实际验收

本机WSL的owned user/network namespace可运行内核WireGuard。使用本地WSL root启动测试再unshare -Urn，以满足受保护reader对root祖先owner的真实校验；不降低reader检查。全部IP/veth/WG操作在子namespace，退出由owned进程和namespace回收；没有宿主routing/firewall或生产动作。

正式fixture实际构建zhreverse server/client、supervisor和独立客户helper；四个独立peer通过内核WG访问产品proxy和owned HTTP/echo target。初始关闭普通/striped/fetch均HTTP503且target0，.3 protected/.7 unknown目标响应成功。DB准备用实际supervised WG apply，显式先删除自有synthetic旧key，不能假装adopt；重置自有peer握手避免客户端旧120s缓存掩盖live-handshake负例。

故障wrapper允许第一Snapshot并实际remove .2，从第二show起持续失败（不是只失败一次：Tick仍会继续其他设备）。Service不监听，实际wg peers证明.2不在/.4仍在；.4三路径实际503/target0，.3/.7成功。后续新authority实际收敛/grant，建managed/protected CONNECT；controller EOF后managed既有echo明确非timeout关闭、新请求503，protected既有echo继续，unknown新目标继续成功。中间native-v7 PASS1.877s，最终源码冻结后另登记fresh gate，不拿此中间结果冒充最终。

反例开发曾因不存在的CLI flags、重复idempotency ID、client旧握手缓存失败；修正fixture的真实产品调用，不把dial失败降为proxy拒绝、不修改失败断言。这些中间日志保留私有目录。

原生追加Snapshot跨expiry：实际WG Snapshot后推进注入Store时钟，Tick返回nil但generation2/applied1、旧peer仍在；最终proof callback0、三路径503、protected/unknown成功。下一Tick实际移除后才允许proof grant。另有firstSnapshot/remove失败（两受管peer仍在而三路径503）、DB outbox done写入ABORT（两peer已移除、nogrant/noTLS/target0；不冒充live-WG负例）、真实controller helper SIGKILL和reverse SIGKILL/重启。重启只清理按owner/inode/parent确认的本fixture staleUDS；旧reverse进程死亡自然断所有旧socket，证据是retained重新连接成功、WG完全不变、managed仍默认拒绝。

最后独立审查又发现late Attach发布ready后再全局close可能关闭随后成功的新owner；修为已quarantine只发布本次ready，否则先按captured old owner捕获资源再发布。原旧顺序的私有overlay增加显式1ms调度停顿确实失败；无停顿250轮未抽到，不能宣称原始必现。最终源250轮freshowner竞态通过。v9统一gate虽exit0，仍仅登记为中间结果；修复后用全新v10证据重跑，不复用扫描结果。

再审查发现容量判断unlock后仅按owner字符串关闭存在同session重新grant的ABA：旧capacity失败回调可能关闭新的激活。容量判断必须在同一mutex决策内立即失效并捕获cleanup task，锁外才设置deadline/做Close；v10同样按中间登记，最终以修复后的全新v11门禁为准。真实yamux Open不接收context，10s OpenCtx不代表底层10s收尾，late预约/quarantine只解决归属与假ACK，真实创建/回收SLA仍未宣称。

最终 transport 复核发现 QUIC stream Close 仅关闭发送方向，不能用于双向回收证明。本片启用 gate 仅支持 TCP/TCP-TLS；默认/显式 QUIC 在 policy/load/bind 前固定拒绝，gate OFF 保持原行为。实际 Linux 构建二进制两项拒绝均 exit1，未创建 policy/control/TLS 文件。最终 shared Windows/Linux 专项分别1.991s/1.812s；reverse 专项2.161s/2.515s，均通过。实际字节通道丢弃 CLOSED/GRANT ACK 后客户端失败，EOF回收受管流、protected/unknown双向echo保持可用；这两项不是 mock 直接返回错误。

库源码复核补充：yamux Stream.Close完成本地FIN发送调用后，若对端不回FIN，其内部stream/closeTimer仍可保留至默认5min超时。closed ACK限定为登记Close调用/待创建预约完成与应用IO失效，不证明库内部对象或缓冲消失；4096×4也不是整个server或历次授权内部对象的全局容量。per-stream reset/contextual Open/内部容量验收留后续transport子片。

ctx.Err检查与SetDeadline/command写入之间尚无原子write fence，取消检查后仍可有在途command或deadline覆写；其资源仍属原quarantine，closed ACK等待登记Close返回。已观察取消/Attach ErrClosed以及ACK后旧流不派发，不等于invalidate瞬间零在途bytes。本波不勾持续撤销SLA，严格派发fence另列transport子片。

## 限制与完整余项

仅本proxy route的源scope屏障；grant在SQLcommit前可见，commit失败窗口由EOF/lease收紧但不承诺零upstream。unknown清理不等于资源已回收，也不承诺底层不可取消Close硬SLA。来源IP不能区分同IP公钥，其他WG INPUT/FORWARD或独立proxy、持续设备/会话授权、生产撤销SLA、日志容量、恢复、正式lineage/campaign、Mac/手机及签名/安装维护仍需实施。整体Steelman与G01–G05未完成。

## 冻结验收

最终 `check-steelman.ps1` v11 exit0，完整Go/product tags/test/vet、Windows race、实际CLI-HubTLS/WG/离线update、SDK33/Rust41、Svelte零错误/警告、Admin43、builder/NSIS/省略dev负例通过。6个Go OS/arch扫描symbol/package=0、module=1（既有未导入OpenPGP例外截止2026-11-06）；两棵完整npm tree零漏洞、4个Rust target零漏洞/未审阅警告。证据 `check-steelman-v11.log` / `security-v11/`；v9/v10均保留为中间结果，不冒充最终。

最终 `sh scripts/check-proxy-barrier-linux.sh` exit0，shared1.814s/reverse2.540s/deviceapi8.744s/deviceauth0.169s，schema/vet通过。实际native父测试7.83s、owned namespace6.01s，上述八项明确PASS，记录 `wave7-linux-final-v2.log`。顶层helper-only子测试的SKIP仅因它由父测试重新exec进入自有namespace；父测试与八项真实链路均已执行，不是缺能力后整组跳过。

原工作区HEAD仍 `e69645a1bee6bb28927e18caa3e43b2021296129`；基线79项逐文件SHA漂移0。当前all-untracked status为89行，新增10项RDP文档/脚本独立工作也未动，不能把基线79说成当前总文件数。

clean十目标开发构建、source/hash和本地提交收据随后补登。私有日志/产物位于外层`.local/zongheng-vpn/steelman/2026-10-07/`，不入Git。
