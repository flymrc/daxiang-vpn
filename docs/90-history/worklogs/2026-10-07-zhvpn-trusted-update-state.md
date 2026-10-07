# 2026-10-07 离线更新水位切片

基于第三波本地代码 `947c5f5` 与文档 `3e4502a` 的 clean gate/build 结果继续第四波。仅在隔离工作树实施；本工作没有推送、部署、生产访问或 publisher 私钥。

## 实现

`clients/cli/internal/updateclient` 提供真实 CLI enroll/verify/policy-approve/inspect、严格公开 receipt 及自动生成 policy/receipt schema。初次 enrollment 需要独立批准的 raw Policy SHA、严格 scope/revision；verify 只使用 protected 已批准状态。registration anchor 先 no-replace 创建，单一 state 保存 Policy+Previous+累计 floors。并发、缺失、旧包、key rotation 与不确定提交不允许丢弃历史或回传未提交资格。

只扩展 `shared/proxy/private_state.go` 两个固定 name；原有 ACL/private reader/write 继续复用。root 增加共用 operations.lock 的 context API、protected Create 和 app 入口早 setup JSON 边界。纯 `updateverify` 增加小型只读 validators/scope 枚举副本，不削弱 Verify。外部公开输入采用平台 safe opener；Windows UNC/device/remote drive syntax 在路径访问前拒绝，Unix FIFO/nofollow 真实验证。

专项合同见 [trusted-update-state.md](../../30-implementation/trusted-update-state.md)。root 负责总计划、README、统一门禁和最终冻结 SHA；本切片的细分测试结果不是完整 Steelman 终验。

## 验证

| 验证 | 结果 | 事实范围 |
| --- | --- | --- |
| Windows updateclient race（13 顶层：11 common+2 Windows） | PASS 2.267s | 合成 private home 的 actual file/Run、JSON/策略/水位/失败/cancel 和 Windows输入/authority权限负例 |
| Windows pure updateverify race（12 顶层） | PASS 1.284s | 既有纯验证回归与新只读 scope copy/版本 validator |
| Windows actual CLI integration 两顶层 | 普通 PASS 3.390s；测试 runner race PASS 4.598s | 构建当前真实 CLI、真实 native candidate 无执行 marker、跨进程/重开/单端丢失、八进程 N/N+1 与 equivocation/幂等 |
| WSL actual Linux `-tags integration` 全 updateclient（14 顶层：11 common+1 Linux+2 integration） | PASS 1.818s | native Linux CLI/process/file；NONBLOCK FIFO、nofollow symlink、Unix不安全状态mode拒绝；未做Linux race |
| WSL pure updateverify | PASS 0.020s | Linux普通纯验证 |
| focused vet、两个 generated schema -check、diff-check | exit0 | 当时对应切片源码；最终统一gate由root另登记 |
| 独立 state private overlay | Windows race PASS 2.328s；Linux native PASS 0.200s | 8 个 native子进程、水位/history/key/单端丢失；Windows实际 no-delete-share handle 造成NTFS Rename拒绝→commit_unknown，无成功permit |
| 独立 fresh scratch 真实 CLI | 31 条子进程 checks 全 PASS | HOME早JSON、8类Windows路径、候选不能自授trust、strict JSON/极端秒数、审批SHA、真实native payload不执行、失败不advance |
| 独立 OS public input overlay | Windows race PASS 1.608s；Linux普通 PASS 0.007s | Windows固定reader拒writer/rename；Linux FIFO NONBLOCK/nofollow；public hardlink明确允许 |

普通库测试不含 integration tag；真实 CLI fixture 的 executable 构建为正常 CGO0 可执行文件，`go test -race -tags integration` 指测试 runner 的 race，并不冒充 product binary 的所有执行都用 race instrumentation。独立 helper/fixture和日志保留在 agent 的私有目录，不进入源码或发布包。

Windows/WSL 集成均证实只验证本次字节和落水位，候选原生程序的执行 marker 没出现。输出丢失/提交 error 保留实际事实，可 inspect 和完整重验；没有自动 rollback/reset。两份完整旧状态由 owner 替换后旧序列可以接受的测试也明确成立：这证明本地存储无法抵抗同 UID/管理员的完整快照回滚，不能宣传为安全攻击已经被阻断。

独立审计未发现新增已知 P0/P1。31 条真实 CLI/OS输入复核的私有夹具位于 `D:/tmp/user-temp/zhvpn-updatecli-review-20261007-4a9f`，只保护新建合成 home 的 DACL；最终统一门禁结果由 root 在收尾登记，局部复核不推定整个第四波或 release 完成。

## 保留的边界

主线程已接入真实离线CLI及两个schema生成检查，并执行冻结v5 `check-steelman.ps1` exit0：全Go行为/vet/可用Windows race、实际CLI-Hub TLS、实际updateCLI、SDK33、Rust41、CLI/SDK builder9/16、NSIS13+完整模板、Admin20及npm省略拒绝通过。6个Go OS/arch的symbol/package各0，module1沿既有有期限例外；两棵npm全树各0；4个Rusttarget tree无漏洞/未审警告，例外到2026-11-06。原始证据为 `.local/zongheng-vpn/steelman/2026-10-07/unified-final-frozen-v5.log` 与 `security-final-frozen-v5/`，位于原workspace父目录，未入Git。

root的Context lock/Create正式4项及PrivateState既有负例Windows race通过；独立Windows race/WSL普通还验证保护读写与existing外部hardlink无变化。主线程另做[线上只读资产复核](2026-10-07-live-readonly-inventory.md)，没有把这些旧binary健康事实当作第四波部署或授权迁移。

第四波源码本地提交为 `a2ecd68865d3c7c5e9ba39924d51258a62e4a9bc`。该clean SHA上九目标 `build-steelman-dev.ps1` exit0，逐项manifest SHA-256核对完成；Windows实际CLI `version --json`为该完整SHA、clean、Go1.26.7、protocol2/contract1。产物/原始日志在私有 `development-wave4-final/`、`build-wave4-final.log`；全部unsigned/compile_only且release_ready=false。Darwin交叉编译不称macOS运行；后续文档提交不改写产物source SHA。

没有外部 publisher 信任批准、signed release、staging immutable copy、安装/升级/业务健康、macOS 实机或生产启用。原子 state replacement 没有父目录 fsync 的跨平台断电证据；同 UID 管理员可整体还原/删除状态。OS mounted filesystem 的网络或内核 blocked I/O 不在应用 deadline 可强制中断的保证内。生产客户授权/手机 TLS/campaign 与连续观察窗完全未变化。
