# Steelman 运行时接线（2026-10-07）

状态：IN_PROGRESS。本文件记录隔离开发分支的实现，线上版本仍以运维文档和实际运行结果为准。源码、合成 OS、平台进程、真实用户设置、生产启用分别验收。

## 客户端代理租约

`clients/cli/internal/runtime/leasecontrol` 把既有 lease Manager 接入 `shared/proxy` 的真实控制器。CLI 隐藏引擎入口安装 hooks；共享库提供阶段/存活锁下的 callback capability，不导入 CLI internal 或操作 OS 设置。

```text
GUI / CLI → home operations lock → signed /v1/runtime-action
          → exact engine identity + phase/lifetime gate
          → original token SID/home → user proxy lock → v2 WAL → WinINET
stop / SIGTERM → same phase gate → restore own lease → stopping → data close
recover → home operations + free lifetime lock → exact old owner → restore
```

控制请求和响应绑定版本、随机 nonce、完整引擎身份、动作、原用户 scope 和 lease ID。请求额外绑定截止时间，存活期内 nonce 不可重复消费，缓存有上限；超时报告 `engine_action_result_unknown`，不推定未执行。新 endpoint 与旧 `/v1/control` 分开，旧 status/activate/stop 的签名字节不因新 DTO 字段改变。

```powershell
zhvpn system-proxy inspect --json
zhvpn system-proxy acquire --json
zhvpn system-proxy release --lease-id <32位十六进制ID> --json
zhvpn system-proxy recover --json
```

`system_proxy_state` 为 `absent/recorded/foreign/acquired/released/recovered`；`owned/noop` 显式输出布尔值，缺失不能当作 false。`recorded` 仅证明恢复意图。Acquire 复用本实例 WAL 返回 `noop=true`，不授予后来打开的 GUI“自己新建租约”的退出归属；复用不保证 OS 无需修复。Release 不停止引擎，显式 ID 不匹配拒绝。Recover 无记录返回 `absent`，仅在匹配 home 引擎确实停止且持有 lifetime lock 时恢复旧实例，不能由新实例认领旧租约。

正常 stop、logout 和 SIGTERM 在数据面关闭前恢复自己实际尝试创建的租约。任何读/写/通知/冲突/恢复记录删除失败，都保持引擎及 WAL；正常无租约的 CLI 不打开 WinINET。强制终止或机器故障留下 WAL，下一次明确 recover，不能承诺崩溃瞬间自动恢复。

Windows 使用原进程 token SID、固定 KnownFolder、规范化 home、实际 Owner/DACL 与 no-delete handle。跨账户提权不会把原用户请求映射到管理员的 HKCU。这台机器现有 Roaming ACL 不满足新 resolver，真实用户路径拒绝；没有拓宽 SID allowlist 或改 profile ACL。macOS 的机器/网络服务所有权尚未实现，新 CLI OS adapter 返回 unsupported，交叉编译不能替代实机验收。

GUI 仅通过 typed CLI 新建/释放 v2 租约；旧 v1 只保留原 GUI 的受控恢复入口，不升级归属。真正退出仅释放本 GUI 成功新建的 lease，保留共享引擎；disconnect/logout 调 CLI stop。错误不能被状态轮询或退出清掉。第二 Windows GUI 在单例锁失败时拒绝启动；可信 IPC 激活仍待实现。

## Hub 与 reverse

Hub canonical OpenAPI 在 `hub/internal/deviceapi/spec/openapi.yaml`，公用 DTO 从同一份源生成到 `shared/devicecontract/types.gen.go`。新 v2 API 实现一次性激活、Ed25519 用途/原始 body/nonce/request/期限签名、同事务鉴权和 desired/outbox 提交、换 credential、有效状态查询与后台到期/启动对账。只返回公钥与凭证标识。`accepted` 不代表已执行，`effective` 是最新 WG allowed-ips generation 的核验，不证明 WG 私钥持有或流量。

新入口默认关闭，显式启用需要私有 Linux hosting、TLS、独立客户 interface、明确可执行程序和受保护资产策略。与 legacy writer 的 interface 分离，未把 `tokens.yaml` 导入或接管当前 `wg0`；尚无 CLI v2 login/bootstrap、campaign lineage 导入、备份撤销合并或真实隧道验收。Linux 的 `zhhub-device-executor` helper 继承同一 flock file description 并持有执行 fence，通过父进程断开 pipe、subreaper/pidfd 清理 WG 子树后释放；Hub 也监督 helper 的私有进程组。WSL 实测 Hub SIGKILL（含 setsid 子孙）、helper SIGKILL，以及 helper+detached 组合故障下持 fence 拒绝新写入。后者不能证明陌生子进程归属时保持 degraded，而不误杀其他组件或提前解锁。要求 Linux pidfd/procfs 与显式 `ZHHUB_DEVICE_SUPERVISOR_BIN`，不可回落不受监督 WG。测试是文件 fake WG/真实 Linux 进程，未证明真实 tunnel 流量撤销。

reverse 的实际双端 `tcp-tls`、角色 URI/Hub hostname/登记 egress 与 leaf、短 credential overlap、存量撤销、registry watermark 及初始化纪律见 [协议合同](../../egress/reverse/SECURE_PROTOCOL.md)。新认证失败不降级；兼容 raw TCP/QUIC 仍保留，当前生产手机尚未迁移。客户 key bundle 在 reconnect 读取，两个旧 session 占满时需要排空或受控重启，不能宣传透明热换钥。

## 门禁与交付

`scripts/check-steelman.ps1 -EvidenceDirectory <私有目录>` 串行运行合同漂移、Go test/vet/race、GUI/Svelte、Rust library、SDK 和安全扫描；任何错误立即停止。GUI 构建也调用统一行为与安全门禁、锁定依赖，开发包明确 unsigned，release 需要干净源和真实签名条件。

`scripts/build-steelman-dev.ps1 -OutputDirectory <全新目录>` 交叉构建 CLI Windows/macOS、Hub/Linux及其执行 helper、reverse/Linux 和 Android 控制目标。显式 full commit、source state、源文件 hash 清册、工具链及 artifact SHA256；目录必须全新，源在构建中变化不写成功 manifest。产物均 `release_ready=false`、`compile_only`；源码可追溯与签名、实机安装、生产启用分别记录。

本轮最低 Go 为 1.26.7，按官方修复升级受影响 x/crypto/x/net/x/sys/x/text/x/mod/chi 等依赖。govulncheck 的 JSON exit=0 不能当作无漏洞；解析符号/package/module 三层，新发现须复核。模块级 `GO-2026-5932` 的 OpenPGP 无修复且产品未导入，例外仅覆盖 module tier，到 2026-11-06，不能覆盖将来的 package/symbol 证据。[官方报告](https://pkg.go.dev/vuln/GO-2026-5932)。

本地测试不改变既有生产安全 campaign、30 天观察窗或 NO-GO。Mac 实机、真实 WinINET、手机 canary、签名/公证与更新链、当前客户授权迁移、长期存储维护和最终九维审计仍须按 [checkbox 计划](zhvpn-steelman-refactor-plan.md)验收。

## 第二波：兼容边界与离线能力

基于本地提交 `072bbc4` 继续实现共同 HTTP 来源/读取预算、Admin generation 状态与 runtime consumer、canonical Admin 漂移门禁及两棵 npm tree 扫描。具体默认值和 legacy 外部执行/页内 mutation 的限制见 [HTTP/Admin 合同](steelman-http-admin-boundaries.md)。

`shared/signingkey` 统一 device Ed25519 公钥和可信 update ring 的非 canonical/small-order 输入拒绝。纯 `shared/updateverify` 只校验签名 metadata 与产物，返回 `verified_for_staging`；安装、水位持久化和 release 签名仍未接入。[更新边界](trusted-update-metadata-verifier.md)。

`zhhub-device-restore-plan` 只比较两个离线 authority snapshot 与 caller 核验 checkpoint；源身份全程固定，查询受保护 scratch 副本，输出不含凭据或路径。时间、字段/JSON 资源预算与 immutable negative facts 严格核验，仍恒 `ready_to_restore=false`；实际最新事实保管链、现场切换/恢复未完成。[恢复边界](device-authority-offline-restore-plan.md)。

本轮包含真实 Chrome 编译 Admin 的合成 API 回归及独立恶劣返回顺序验证；它不能代替生产 Admin 或真实客户代理验收。完整独立发现、修复和证据见 [第二波 worklog](../90-history/worklogs/2026-10-07-zhvpn-security-boundaries.md)。

## 依赖扫描的证据层级

本轮使用 Go 1.26.7、govulncheck 1.8.0 与 cargo-audit 0.22.2。Windows/Linux/Darwin 的 amd64/arm64 源码扫描分别登记，不能外推其他 build tags 或架构。前端 npm audit 检查包括 devDependencies；Rust 所有已知 vulnerability 均阻断，warning 必须逐条有限期处置。当前 5 个 UNIC unmaintained warning 来自 Tauri URL pattern 依赖；proc-macro-error unmaintained 与 glib unsound 仅在对应 Windows/macOS target tree 不包含时允许。全部例外到 2026-11-06，不允许例外覆盖新的漏洞、实际目标包含或更强可达证据。

独立审计新增并修复：Hub exact JSON 字段校验、拒绝过期 grant 不消耗 nonce/request ID、Linux 崩溃进程监督；reverse 阻塞 target write 下仍可撤销、登记 JSON exact 字段与 Unix trusted/private 目录。验证命令和原始证据最终登记在 [10-07 worklog](../90-history/worklogs/2026-10-07-zhvpn-runtime-integration.md)。

## 客户端凭据与 v2 消费者

legacy 客户端的 token cache / WireGuard key 和新设备 state 都改为 fixed-path 私有、原子存储，复用运行时 Owner/DACL、no-reparse、单链接与规范化 home 策略。新写入先保护文件后写秘密；旧继承/不安全文件读取拒绝，程序不自动拓宽权限。已有 legacy 服务返回的 server-generated WG 私钥兼容行为仍待 campaign 迁移退役；新 Ed25519 私钥只在设备生成和保存。

GUI/Python SDK 的 legacy 登录改为有界 stdin，不把授权码放进 argv。GUI 删除 `zhvpn.lastToken` 浏览器缓存，不再保存该键。CLI 保留 positional login 作为明确旧兼容，面向新消费者推荐 `login --token-stdin --json`；不能宣称旧客户端或同用户恶意进程因此隔离。

GUI 的状态查询失败会清除旧就绪快照/IP并显示待确认，禁用依赖当前状态的动作；迟到 IP 不恢复旧状态。新状态只清临时查询错误，操作/代理恢复诊断另行保留。真实本地浏览器使用 mock IPC 验证，未将其称为打包 Tauri 或生产会话，见 [桌面边界](desktop-fresh-install-boundary.md)。

旧 CLI 两个平台构建入口都进入统一 gate，正式签名/公证未验收时 release 拒绝，development 有完整 source inventory/空目录/环境恢复与 compile_only清单。SDK 直接 wheel 同样拒绝；自动 bundled discovery 核对协议、实际PE架构及hash，开发未签名包需显式启用。SDK 清单在身份/可选安装/源码校验成功后才发布。正式 publisher 信任、平台 wheel 和可信更新链仍是未完成的独立边界。

新 `device` 命令独立消费 canonical TLS v2 API，默认不替代 legacy login/bootstrap，也不自动改变当前 proxy/WG 配置。公开 device receipt 是 contract version 2，和现有 CLI JSON v1 分开。先落私有 intent 再提交；激活/换钥/命令响应丢失通过原key/request/idempotency只读 receipt 查询恢复，不新建第二次mutation。真实 CLI executable 与实际 Hub handler/SQLite 的 TLS 互通已测试；WG执行是内存 fake，未证明真实隧道或生产采用。
