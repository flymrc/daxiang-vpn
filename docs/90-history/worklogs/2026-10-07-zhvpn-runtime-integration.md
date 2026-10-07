# 2026-10-07 zhvpn 运行时接线与独立审计

状态：IN_PROGRESS。工作目录为独立 worktree `codex/zhvpn-steelman-runtime`，基点 `415d6950599ab283dfc42be5d84a2a23e3b75aeb`。本日没有 push、生产 SSH/ADB 变更、真实 Internet Settings/profile ACL 写入或 Jetstar 流程。

## 已落实的行为

- CLI 系统代理租约在真实 HMAC runtime-action + phase/lifetime gate 中执行，保存原用户 SID/home 和精确 lease。Stop/信号先恢复自己尝试建立的 WAL，恢复失败继续提供引擎；崩溃旧owner仅在原home停止且持lifetime fence时recover。
- GUI typed lease不认领复用/SDK租约；v1只保留受控恢复。无配置状态仍进入登录页。GUI/SDK授权码改stdin，GUI清除浏览器token缓存；CLI token cache、WG key、device state改实际ACL/owner/private原子存储，旧unsafe读取拒绝。
- Hub TLS-only opt-in v2 authority：一次性激活、Ed25519 proof、bound nonce、同事务desired/outbox、换钥、有效状态查询、到期/启动对账。独立客户interface不与legacy writer共享，默认不启用、不导入YAML。
- Linux WG helper继承同一flock fence，Hub SIGKILL（含setsid子孙）后清理才解锁；helper自身SIGKILL也由Hub监督，无法确认detached子树时保持fence/degraded。Windows用Job Object；均为真实进程+fake WG文件，非真实tunnel。
- Linux wg/helper 在构造、每次执行前及 helper 内验证文件与全部祖先的 owner、权限、普通文件与无 symlink；仅 root-owned sticky 祖先例外。WSL root fixture 实际 chown 65534 的文件/祖先负例已通过。root/同 UID writer 和 Hub/helper 同时毁损且子孙脱组不属于已验证保证。
- reverse双端tcp-tls提供TLS1.3/角色身份/hostname/已登记egress+leaf及credential撤销；阻塞目标写也可被撤销关闭。登记JSON exact fields、私有父目录与trusted ancestors、水印generation保护。旧tcp/quic兼容仍在，生产手机未迁移。
- 设备CLI独立v2 receipt，正常CA验证，先落private intent后提交；已提交响应丢失由receipt恢复，不自动新建mutation。显式resolve-or-cancel在同fence提交永久cancel tombstone，拒绝迟到原请求。
- Go1.26.7与针对性依赖修复；锁定npm/Cargo版本。统一gate保存原始扫描并验证完整性/target tree/期限例外；GUI/SDKbuild调用gate、干净release/真实签名条件，dev明显unsigned/hash追溯。
- 旧 Windows/macOS CLI 构建入口统一委托新 builder，默认 release 在编译/创建输出前拒绝，开发显式 gate+双架构+source manifest+环境恢复。SDK 直接 wheel 路径也拒绝；自动 bundled discovery 核对协议、manifest、宿主与实际 PE 架构、SHA256，未签名开发包需显式启用。未把构建关系当成正式 publisher 信任链。

## 独立反例与修复

1. Hub JSON大小写别名覆盖签名命令值：exact field whitelist拒绝。
2. Hub在过期grant拒绝前消费nonce/requestID：同事务回滚，另事务仅降级旧任务，同nonce/request可恢复后重试。
3. Hub崩溃释放fence但旧WGchild继续写：Linux真实SIGKILL复现，增加helper+parent监督和持fence清理。
4. 无私钥攻击者滚动填永久challenge：challenge发行需PoP、短期重复同proof复用、清过期、保留行/永久历史配额；安全revoke/expire有保留额度，不删tombstone/audit。
5. reverse持state mutex做阻塞target.Write导致撤销不能关闭：独立target-first cancellation+write序列化，state mutex不跨阻塞IO。
6. 登记generation/Generation别名：嵌套schema精确字段拒绝。
7. GUI把无配置非零exit全当异常，无法登录：只允许明确client_config_unavailable degraded/false状态路由登录；失败ready仍拒绝。
8. NSIS探针过后新进程锁住sidecar，安装exit0却新GUI+旧CLI：仅fresh target、旧卸载器执行前拦截、可信private staging/完整hash后目录无覆盖发布。已有升级/自动卸载仍拒绝，未勾完整安装升级阶段。
9. SDK 成功输出或大小写私有字段回显：成功/拒绝都递归红化输入及收集到的私有值，永久测试包含非 ZH 前缀授权码和嵌套 echo。
10. 旧 CLI builder / 直接 wheel 可以绕过新统一 gate，wheel 标签按宿主而非 payload 架构：关闭正式 CLI/SDK wheel 发行旁路，开发目标与 metadata/PE 交叉核验。
11. 浏览器 ready 后 IPC 查询失败仍显示旧“已连接”与换 IP：真实本地 mock IPC 浏览器复现，冻结门禁 v1 已中断并保留。限定修复后清状态/IP并禁用未知操作，迟到IP不复活状态，新完整ready只清临时状态错误、保留action/recovery诊断；Chrome mock复验和npm check通过，fresh门禁另登记。
12. SDK 在 identity/pip 验证前写成功 manifest，读取继承的交叉编译环境作为宿主，且自身 JSON receipt 让源码变脏：身份与可选安装成功、源码再校验后才写成功清单，GOHOST 与完整 env恢复、精确忽略两个 artifact receipt；所有失败保留原安装，不产生成功清单。

## 证据位置与层级

原始证据只在私有目录 `.local/zongheng-vpn/steelman/2026-10-07/`，不入仓：Hub/reverse/CLI-GUI独立反例、mock浏览器图、源码扫描JSON、target tree、统一gate原始输出和开发产物manifest。

设备消费者最终 Windows 19 顶层+15子例（race）与 Linux 18 顶层+15子例（普通）通过；Hub三包 Windows race 和 Linux普通 test/vet通过；SDK 33项、Rust41项、CLI builder 9 个合成场景、NSIS13行为场景+完整模板实际编译通过，最终统一结果另登记。Linux可信路径的14负例+2次执行前复验包括真实 owner 拒绝，无 skip。

Windows true child/HMAC/lease tests 与Linux WSL真实进程fault tests有效；注册表是synthetic key，GUI浏览器是mock IPC。Rust库/stdio child与NSIS synthetic安装目录不等同真实Tauri AppHandle、用户WinINET、正式安装包或签名。Linux未装C compiler，Linux race未运行；Windows race实跑。Mac仅源码扫描/编译，不是实机验收。

6个Go OS/arch source-symbol扫描均为0 symbol、0 package、1 module-only例外（GO-2026-5932，产品不导入OpenPGP且无修复）；npm全树0漏洞；Cargo0已知vulnerability，7个warning按Windows/macOS 4 target trees及2026-11-06截止处置。新/过期/更强tier发现阻断，不把例外称无风险。

原工作区原79个dirty文件仍在（missing=0）；最终相对当前快照有README、架构、diagnostics三份文档被并行工作更新，保留新hash，未覆盖回旧manifest。主工作区HEAD仍e69645a，当前89个展开dirty条目包含并行新增。隔离分支包含先前未推observer祖先，不能直接push整枝冒充仅本轮改动。

## 最终门禁与产物

冻结v2统一门禁 `pwsh -NoProfile -File scripts/check-steelman.ps1 -EvidenceDirectory <fresh>` 已exit0。行为包括生成漂移、全Go test/vet、Windows客户端/reverse/authority race、真实CLI↔Hub TLS、SDK33、scanner parser6、CLI builder9、SDK builder16、NSIS13行为+实际完整模板编译、Svelte0/0、Rust41与Clippy。SDK builder的compiler/gate/pip是受控双，version是实际native child；不等于SDK产品安装或签名证明。

fresh安全扫描6个Go OS/arch均为0 symbol/0 package/1 module-only有期限例外；npm全树0、Cargo已知vulnerability0，4个target tree的既有warning全部按期限处置。原始输出 `unified-final-frozen-v2.log` 与 `security-final-frozen-v2/` 保留；v1因浏览器新反例主动中断，不算通过。23份PowerShell脚本语法及diff check通过。开发矩阵/本地提交在随后记录，整体计划仍IN_PROGRESS。

## 明确剩余项

macOS系统代理/权限适配与实机、真实Windows用户代理/双会话/升级、设备lineage/YAML受控导入及备份撤销合并、campaign清册/T0/连续窗口、签名公证/可信更新链、真实WG隧道/手机canary与长期性能/运行观察、全部F1–F14和九维终审仍须单独验收。当前生产TokenStore/wg0、raw TCP、readiness与NO-GO保持既有状态。新SQLite草案schema_version=2，v1数据库保留并明确拒绝，未做自动迁移或清空。
