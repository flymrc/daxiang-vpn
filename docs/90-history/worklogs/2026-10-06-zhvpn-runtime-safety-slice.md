# 2026-10-06 zhvpn Steelman 客户端安全首切片

> 状态：LOCAL SLICE VERIFIED；整体 Steelman 仍在进行中。用户要求“开始”后实施；没有推送、部署、改生产配置或运行 Jetstar。
> 总清单：[重构计划](../../30-implementation/zhvpn-steelman-refactor-plan.md)。本轮合同：[实例与代理恢复](../../30-implementation/client-runtime-safety-contract.md)。线上事实：[当日只读资产基线](2026-10-06-zhvpn-asset-baseline.md)。

## 基点与工作区保护

- 原工作区 HEAD 为 `e69645a`，包含本地尚未推送的 observer 与文档提交；GitHub main 为 `92379a3`。已推送 idle-preempt 分支包含 `e5be358` 与后续文档提交 `54c9eeb`。
- 在独立 checkout 合流两侧必要行为，基线为 `3c88880f37078c22b301f21e7b9a92e6fc3cc5bf`；本地实施分支 `codex/zhvpn-steelman-runtime`。该合流包含原本未推提交，不得将整支直接当成只含本次改动的发布分支。
- 工作树：`C:/Users/xuotq/.codex/worktrees/zhvpn-steelman-runtime/zongheng-vpn`。只调整该 worktree 的 `core.worktree` 以修复继承 submodule 路径；Codex 注册失败，未反复创建 checkout。
- 原工作区 79 个既有 dirty/untracked 文件保存 SHA256 清册；首轮总门禁后逐一复核均未变化。原工作区没有复制入整套既有 dirty 文件，本工作树只纳入必要计划/审计记录与本轮改动。
- 本地证据保存在父工作区 `.local/zongheng-vpn/steelman/2026-10-06/`，不进入仓库；没有保存真实 token 或私钥。

## 实现结果

1. 引擎控制：随机实例 ID、规范化 home、配置 generation、协议版本与 nonce/HMAC 认证。控制密钥不发送到控制端点、不进入公开 CLI JSON。PID 只用于诊断，裸 PID 强杀被拒绝。
2. 生命周期：home 操作锁与引擎 lifetime 文件锁；Windows 使用句柄锁、Unix 使用 flock。数据面初始化加启动者认证 activate 才能 ready；启动有到期期限，专用隐藏 child 可在卡住关闭时自行退出，库调用不退出调用方进程。
3. 丢失元数据：state/PID 消失而 lifetime lock 仍持有时，Inspect 返回 degraded、Stop 返回错误；logout 保留配置和凭据。Stop 等待锁释放，不仅凭 state 消失就报完成。旧 PID-only 记录拒绝自动认领/强杀。
4. 路径与文件：Windows 实际目录句柄解析 junction，home 别名归一；运行文件验证 Owner/DACL、单链接、无 reparse 和 file ID。只验证目录权限，不重写目录 DACL，避免继承传播影响目录外硬链接对象。初始化不再删除现用 session 配置及日志。
5. GUI 系统代理：v1 journal 保存四个 WinINET 字段的原始存在性、类型和 bytes，以及本次写入值；更改前持久化。无 journal 完全 no-op；遇到已观察的外部冲突、读写/通知/删除失败时保留记录、返回错误。恢复失败阻止 stop/logout/quit 并持续显示诊断；登录/加载页也持续观察错误，不清除用户输入。全事务文件锁覆盖 journal/registry 操作，禁止持锁删除/替换文件；启用前验证成功的 CLI ready/可达状态。
6. 本地门禁：新增 `scripts/check-client-safety.ps1`，各项失败立即停止；保留无 hosted CI 的既有政策。补齐当前 Node 24 工具链的类型声明，Svelte 检查为零错误、零警告。

CLI 系统代理租约迁移尚未实施；当前 journal 仍在 GUI。第三方程序不参与本应用锁，逐字段核对不能提供跨程序全局 CAS。完整 JSON/schema 生成、operation ID/rotate 幂等、设备授权与有效撤销、reverse TLS、签名发行均未因首切片自动完成。

## 基线失败与回归

- 干净合流基线的常规 Go 测试 112 项中 111 通过，RTT 调度测试失败；初始化日志保留负例在旧实现上确实失败。第一次尝试直接搬入新增测试因旧源码缺少 CanonicalRoot 而编译失败，不计为行为复现证据。
- RTT fixture 在写出 CONNECT OK 后立即关闭，导致 deadline 设置与 EOF 竞争并误触发 session failover。仅调整测试 fixture，保留 stream 直到客户端关闭；没有修改生产 reverse 调度、限额或 idle-preempt。相关 3 项 race 测试各重复 50 次通过。
- Windows 真实 loopback child 覆盖认证启动/停止、崩溃后重新启动、PID-only 拒绝、不可信控制端点、身份不匹配、跨进程操作锁、状态丢失及停止等待。
- 独立复核确认并修复 3 个首切片缺陷：目录 ACL 传播/硬链接影响目录外对象；遗漏 foreign owner；活引擎丢失 state/PID 后误报停止。外部 ACL 不变负例与真实 child 元数据丢失回归已进入测试源码。
- GUI 独立复核发现并修复 4 个接缝：失败/降级状态仍按配置地址启用代理；跨登录会话交错删除正在使用的 journal；不兼容记录缺少可执行归档路径；登录页漏看迟到的后台恢复错误。精确 journal 交错、独立句柄/真实 child 锁、ready 拒绝路径与全部格式错误的指引已进入回归。独立复核最终未见新增阻断缺陷。

## 验收记录

| 验证层级 | 当前结果 | 限制 |
| --- | --- | --- |
| 本机 Go | 最终总门禁 Go test/vet 与 CLI/shared/reverse race 全通过，采用 `with_gvisor` 产品 build tag | 不代表手机/Hub 已运行该源码 |
| Python SDK | 5/5 通过 | 现有 CLI 协议消费者回归，不代表生产业务 |
| GUI Rust | 最终 30/30 通过；check/clippy `-D warnings` 通过；独立复核也重跑 30/30 | 合成 HKCU 子键、文件、独立句柄与 child；不写真实 Internet Settings、不广播真实 WinINET 变更 |
| GUI 前端 | npm check 零错误/警告；npm build 通过 | 正式 sidecar/安装包未验收 |
| GUI 浏览器 | mocked Tauri IPC + 真实 Chrome：错误跨轮询持续、失败登出/断开保留、重试成功清除；登录/加载页迟到错误可见、输入跨轮询不变 | mock 不等于实际系统代理或真实 sidecar；截图在私有证据目录 |
| Windows 编译 | `windows/amd64` CLI，`with_gvisor` 产品 build tag 通过 | 本地未签名开发产物，不是安装包 |
| macOS 编译 | `darwin/amd64` 与 `darwin/arm64` CLI + proxy 测试二进制，`with_gvisor`、CGO=0 全通过 | 没有在 Mac 运行，GUI/提权/安装升级未验收 |
| 生产 | Hub/手机/Mac 只读资产对账 | 无新生产行为变更 |

最终整入口 `pwsh -NoProfile -File scripts/check-client-safety.ps1` exit 0。原始输出：私有证据目录 `client-safety-gates.txt`，首轮输出另保存在 `client-safety-gates.initial.txt`。编译 manifest 记录开发产物 hash 与 Go 1.26.5、Rust 1.97.1、Node 24.18.0；Darwin 产物仅编译证据，不署名为发行包。GUI library 验证局部使用 `TAURI_CONFIG={"bundle":{"externalBin":[]}}` 并恢复环境；这是免 sidecar 打包输入的测试构建，不是发行验证。

## 后续与保留限制

- P0 安装实例/peer 归属、版本与可恢复产物缺项仍待核验；`.30` 公钥冲突与未知基础设施 peer 均不自动修复。
- P1 完整合同与生成、P2 Mac 实机与旧版迁移、P3 OS 用户代理租约与真实安装恢复、P4 授权数据库/outbox/有效撤销、P5 手机 TLS 后续按主计划推进。
- npm 报告的 5 项开发依赖漏洞未用 force 升级隐藏，留 P6 按可达性处置。没有重新评分或宣称 ≥85 分、没有进入生产安全迁移。
- 复核的合成 child 均经认证停止。自动审批拒绝临时夹具清理，只返回 `blocked by policy`、未给具体原因；未换工具绕过。残留在私有 `D:/tmp/user-temp/zhvpn-runtime-review-0a0ffbccc57149dd9b849b25d8478b2f`，只含合成探针/配置和空锁，控制状态已认证停止删除。
