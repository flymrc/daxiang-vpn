# 2026-10-07 固定迁移清册与观察缺口

第六波基于第五波 clean 源 `17e7689`、构建收据 `3130109` 和计划 `867e458`，在既有隔离开发树实施。没有推送、部署、设置真实 campaign T0、修改 token/peer、发行客户端或跑 Jetstar。[checkbox 计划](../../30-implementation/migration-inventory-plan.md)与[总计划](../../30-implementation/zhvpn-steelman-refactor-plan.md)分别记录本批与完整 Steelman。

## 已核验的问题

此前实际 SQLite/HTTP 独立反例证明：readiness 用 Snapshot/Resolve 实时有效列表作为分母会遗漏删除/停用/过期成员；latest secure 覆盖报告会隐藏历史 compat/legacy；已知有效 token 的实际失败请求只留 audit；SQLite 观察写入 ABORT 后重建 Server 会把内存失败标志清零。现有硬编码 ready=false 防止错误 Go，但不能代替准确分母和故障事实。

本批目标是有限严格、人工批准 raw SHA256 的 provisional 清册和单调历史/持久 observer run/gap；安装引用仅为声明，release/双次 E2E/持续窗口/撤销与恢复仍阻断。T0=null、campaign_configured=false、ready=false，不提供自动批准或真实 campaign 启动入口。

## 独立补充

第五波实际 Windows CLI/WG fixture 新增 occupied-port 正负对照：在真实授权已 applied 后保留 foreign loopback listener，start 返回 local_port_occupied/rejected，无 engine identity、cache 或 private launch file；Inspect stopped，foreign listener 仍可连接，随后正常启动并完成真实目标/撤销原链路。`go test -race -tags integration,with_gvisor ./hub/internal/deviceapi -run '^TestRealCLIProxyBootstrapToWireGuardOwnedTargetAndRevoke$' -count=1` PASS3.540s。这项验证发生在第五波冻结构建之后，随本批统一门禁再次核验；未声称端口检查后的抢占竞争已测试。

文档另修正了手机基线时间归属：10-06 Pixel 记录保留为当时观察；10-07 用户提供 AGENTS.md 标明 Motorola/dx 兼容布局，待新实机只读对账。本轮未连接手机或 Mac，不把其中任何一个布局冒充新鲜核验。

## 验收登记

后端新增 canonical Admin contract2/sqlc，严格有限 raw inventory 审批与 immutable baseline、追加 extra、source 全生命周期、单调事实、observer 预落 open run 与 sticky gap。auth/bootstrap 的有效 token 拒绝/执行失败落同一审计事务，关闭/换代占 gate 时立即503而非无界排队。离线命令输出失败返回非零，DB可能已经提交，只以同 raw SHA 幂等恢复。Windows source 祖先短暂持有/Unix逐目录 NOFOLLOW 与本地FS allowlist、SQLite immediate transaction 的8原生进程竞争均有实证。

独立测试新增旧 secure 异常 metadata 的四个 SQL 子例：后来合法 secure 清掉 historical_unknown，首次实证 FAIL0.516s。修复在同初始化事务保存 immutable import flag 并补一次 unknown；已有 facts 也补，重复重开不递增。旧 secure 记账保留，历史栏不能相加当作去重请求总数，无法恢复升级前已被覆盖的 provenance。强化原子失败、崩溃缺口、坏字段不出 DTO、后来/等时/乱序 secure 与重复重开后，整个正式10顶层/6子例、9真实子进程最终 Windows race PASS3.783s、Linux普通 PASS0.884s；没有弱化原失败断言。

后端四包最终 fresh Windows `-race -tags integration`：db4.359s/API3.814s/command3.079s/auth8.114s；WSL本地 `/tmp` 普通：0.872/1.364/1.168/6.603s，包含 actual FIFO/symlink 拒绝。vet/diff-check exit0。原始独立日志另保留 `D:/tmp/user-temp/zhvpn-campaign-independent-20261007/`，第六波最终统一门禁会再执行 owned selector。

消费者43项全部通过，Svelte检查0错误/0警告，embed构建113 modules。实际 Chrome 使用编译后的页面与仅本地合成 API：1440/390视口的空清册、固定3/追加1与disabled/missing/expired、旧负事实/gap、503/旧精确v1/畸形计数清旧快照均通过。真实迟到403和10s迟到 ready 不复活旧数据；7008ms后实际200 reveal 也不复活秘密。并发行为验证之后最后只修改CSS，generation guards保持相同。

21个成员/21个run的真实窄屏分页先失败：20行卡片 flex-shrink 压至1.14px，真实按钮被后续内容遮挡。没有强制点击绕过；仅 mobile `.card.flush` 加 flex-shrink:0 后重建/重载，再正常点击20→1两组分页，下一页禁用、上一页启用；键盘 ArrowRight 实际横滚40px。五个旧页签分别在两视口带非空合成 token/lease/egress/event 验证10/10无页面横溢出，表格滚动限定卡片。最终6张空/缺口/失败图及分页图经查看，缺陷截图保留。最终 src/dist冻结13:10 JST；8个文件SHA逐一匹配 browser receipt。该证据不是生产登录/出口或真实 campaign。

采用 webapp-ui-skill：粗 state-coverage 实际扫描15文件、exit0但passed=false（缺 submitting）；只读迁移页无提交，记录该项不适用，不伪造 marker。visual-smoke HTTP200/非空检查执行，脚本截图driver缺失为skipped，实际Chrome截图补验。技能引用的两个 shared/privacy-policy.md、visual-verification.md 本机缺失；全部截图与合成证据留本机私有目录。

统一 `check-steelman.ps1`：v7 exit0仅登记为中间结果，分页CSS变化后以全新 evidence/log重跑最终v8 exit0。包括全部Go产品tags/test/vet/race、真实CLI/WG/初始service/清册SQLite故障与重启/离线updateCLI、SDK33、Rust41、安装模板与builders负例、GUI/Admin零type错误和Admin43消费者。Go六OS/arch扫描symbol/package=0、module=1的未导入OpenPGP既有例外保留到2026-11-06；两npm含dev树为0、Rust四target无vulnerability或未复核warning。没有缩小原门禁。

最终源码提交后，以新的 development-wave6-final 目录做十目标 clean compile/hash收据，完成后单独登记。原工作区79个dirty文件hash未变化。私有原始证据留在外层 `.local/zongheng-vpn/steelman/2026-10-07/`、`admin-migration-v6-9172/browser-receipts.json`，真实客户/凭据/路径清册不进入Git。

## 完整余项与后续顺序

整体 Steelman 未完成：provisional清册不等于真实安装lineage、受控授权导入、连续campaign窗口或恢复；实际反例中的外部旧WG数据面启动失败屏障、持续租约/撤销SLA、Mac/手机迁移、签名/安装维护及日志/性能继续未勾选。

下一安全片必须在真实 reverse 普通/striped CONNECT和fetch入口安装独立默认关闭的受管source屏障，固定scope不能由一次grant决定；closed ACK→initialTick→fenced最终convergence proof→短期grant，控制失联/过期关闭登记流，保留protected/unknown范围。Tick nil中途过期仍可能新增待撤销generation的组合仅为源码推导、尚未实测；不能误记为已复现。该片只能先闭合受管proxy路径，不能代称所有WG INPUT/FORWARD屏障或同IP的新旧公钥身份。WSL隔离user/net namespace可用，尚未安装任何规则或改宿主路由。

并行日志只读审查发现 macOS stdout/err追加无byte cap、Windows detached流进入DevNull、in-process sing-box默认stderr无受控保留；Android多个持久FD追加和systemd模板没有受验证byte限额。下一日志片应先做owned有限字段/有限队列与文件预算，不记录vendor自由文本或秘密；本机file I/O不可取消、Android多writer和宿主journald仍须独立合同/实际验收。SQLite行数政策不能推为整库/WAL或全系统日志容量。
