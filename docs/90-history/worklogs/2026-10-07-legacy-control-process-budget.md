# 2026-10-07 Legacy 控制面执行预算

在隔离worktree继续第二波之后的P4.5本地源码切片，未push、部署、连接生产WG/SSH或更改真实OS代理。

- 新增独立processbudget，WG/SSH本地名额、总context、累计输出上限和固定错误；Windows suspended Job与Linux独立helper监督不借v2执行权限。
- bootstrap/client rotate/admin rotate贯通request context；carrier改为固定容量singleflight、可取消等待，外部probe不占cache mutex。
- bootstrap用版本收据与共享pending前驱补偿明确pre-start失败，跳过乱序拒绝的failed节点，避免两个pre-start失败复活幽灵lease；成功/Started未知commit截断历史。rotate未知不随普通cooldown到期再次触发，不自动重试/不杀手机远端setsid恢复。
- 三项legacy lease/rotate-extra/carrier-cache秒数先校验0..86400再乘duration；0语义保留，非法/溢出回默认，不把原值写入日志。旧公开RotateIP入口同样委托固定ingress准入，不能绕过共享预算。
- 同步专项文档：[legacy-control-process-budget.md](../../30-implementation/legacy-control-process-budget.md)。main supervisor入口、跨auth/admin准入、canonical rotate_state与UIunknown禁用由主线程同步。
- 冻结后Windows `go test -race ./hub/internal/processbudget ./hub/internal/auth ./hub/admin/internal/api ./hub/admin ./hub -count=1`通过，五包8.499/8.370/2.544/4.301/1.891秒；WSL Ubuntu-24.04 Go1.26.7同组普通测试6.733/6.594/0.074/1.879/0.004秒通过。Windows同组vet与diff check通过。Linux无C编译器，未宣称Linux race。
- 独立Linux私有overlay实际验证关闭lifetime时helper返回C2且未创建实际启动marker，open-life正对照C0+marker通过；过程所有权测试为真正owned native进程，auth慢body为真正owned loopback TCP，均不是生产验收。
- 独立复核冻结后复跑原幽灵lease反例，Windows-race auth1.709秒通过；实际准入容量overlay1.458秒通过，独立native Windows-race8.554秒/WSL普通6.724秒通过。该局部范围无剩余已知P0/P1，不扩展为整项目或远端派发完成证明。

未完成：生产容量与受控部署、跨重启unknown/remote RID回执、legacy peer ownership/sole-writer以及完整P4.5设备/会话身份。无法确认本地cleanup时返回unknown并quarantine，不伪报终止；helper丢失同时detached逃逸与不可终止kernel工作保留明确边界。

## 主线程最终接线与门禁

- Admin 与客户端借用同一准入对象；3项正式 integration 测试覆盖满载时bootstrap/rotate/login/exit-IP拒绝、健康快照仍能读取、错误来源/nil预算拒绝以及unknown未来到期仍不重派。
- canonical OpenAPI补429/502/503及Retry-After、可选rotate_state；由真实配置重新生成Go/TypeScript。管理台消费者20项、类型检查0错误0警告、编译dist完成；不手改生成文件。
- 实际Chrome在本次编译production页面上跑10项状态、3项权限刷新时序、5项server unknown/cooldown/idle/重载/缺失运行事实负例，全通过，无JavaScript异常。合成API只证明消费者；截图含synthetic标记，没有真实Hub/手机数据。测试自有Chrome与127.0.0.1:14270 preview已关闭。
- native正式顶层测试Windows9项、Linux11项；新增auth边界顶层10项。独立原幽灵lease反例race转绿，C2关闭lifetime负例不启动child，C0正对照真实执行。Linux没有C编译器，本轮普通native测试不能写作Linux race。
- 冻结v4统一 `check-steelman.ps1` exit0。Go行为/vet/race、合同、SDK33/Rust41、CLI/SDK builder9/16、NSIS13+完整模板、Admin20及真实npm omission拒绝均通过。6个Go目标symbol/package各0、module1受期限例外；desktop/Admin两树npm各0；4个Rust目标tree无漏洞/未审警告，现行例外到2026-11-06。
- 私有原始日志目录为 `.local/zongheng-vpn/steelman/2026-10-07/`（位于原workspace父目录）：`unified-final-frozen-v4.log`、`security-final-frozen-v4/`、`admission-integration-final.log`、`admin-state-cases-third.log`、`admin-authority-refresh-third.log`、`admin-rotate-unknown-final.log`、`processbudget-independent-native-final-{windows-race,linux}.log`；不将证据、私钥、可执行产物或DB入Git。

这些结果不改变生产参数/授权事实源，也不证明跨重启at-most-once、真实OS代理、Linux race或远端恢复完成。Steelman G01–G05仍需完整后续验收。
