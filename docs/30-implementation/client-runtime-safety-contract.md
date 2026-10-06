# 客户端实例与代理恢复合同

> 2026-10-06。Steelman 首切片实现与验收说明；不代表生产客户端已升级。
> 总清单见 [重构计划](zhvpn-steelman-refactor-plan.md)。授权/撤销的后续合同见 [设备合同](device-auth-revocation-contract.md)。

## 当前责任边界

CLI 调用 `shared/proxy` 中的实例控制、操作锁与 sing-box 数据面；GUI/SDK 继续通过 CLI JSON 使用同一引擎。首切片把控制实现按文件与协议分离，没有为了目录结构引入反向 internal 依赖。

Windows 系统代理暂由 GUI 的纯 `proxy_journal` 状态机与 WinINET adapter 管理。迁入 CLI、按 OS 用户协调不同 home 的共享代理租约仍是 P3.1 后续工作，不能宣称已实现。

## Home 与实例身份

- `paths.CanonicalRoot` 解析绝对路径与现有祖先，再拼接尚未创建的目录。Windows 通过目录句柄解析 junction；macOS 使用真实符号链接目标。保留访问路径的实际大小写。
- 每个规范化 home 有操作锁和独立的引擎生命周期锁。锁属于打开的文件句柄，崩溃由 OS 释放；不删除锁文件来实现解锁。
- 随机 `instance_id` 标识一次引擎实例，`config_generation` 绑定本次 runtime 配置，`control_protocol_version` 描述本地控制协议。PID 是诊断信息，不是停止权限。
- CLI 对旧 PID-only 状态拒绝自动停止和认领；人工核验旧引擎可执行路径、home 与运行身份，确认其退出后再归档旧记录，不能仅凭旧 PID 调用旧版强杀。不能把“PID 存在”当作新版引擎身份。

## 本地控制与就绪

公开 `EngineIdentity` 与 CLI JSON 维护源为 `shared/contracts/source.go`；`shared/proxy` 使用生成身份类型，控制消息与 `EngineStatus` 仍由该包维护，控制地址仅监听 loopback。凭据保存在受访问控制的运行目录，不进入公开 JSON、日志或诊断包。

请求与响应使用随机 nonce 和 HMAC，绑定实例、home、generation、协议与消息用途；控制密钥不发送到目标端点。重用端口、普通代理 listener、伪造响应及身份不匹配均不能授予停止权限。

停止通过验证后的控制通道请求引擎自行结束。所有裸 PID 强杀入口拒绝执行，控制不可达或停止未完成时返回可定位错误并保留恢复材料。清理仅作用于匹配本次身份的记录。

state/PID 文件消失不证明进程退出。状态检查与停止完成检查还须确认生命周期锁已释放；锁仍被持有而身份文件不可读时返回 `degraded` / `engine_control_unavailable`，拒绝继续登出清除凭据，也不猜测进程身份。崩溃留下的新格式记录只在取得生命周期锁并复核身份后回收。

`ready` 要求数据面初始化和本次启动授权握手均完成；未确认或超时的启动不得无限期留下新实例。生命周期状态的含义如下：

| 状态 | 意义 |
| --- | --- |
| starting | 已识别本次初始化中的实例，尚未确认就绪 |
| ready | 已验证本实例的数据面初始化与启动授权 |
| stopping | 已请求该实例停止，等待其关闭 |
| stopped | 没有已验证的运行实例 |
| degraded | 旧记录、权限、控制或恢复异常，不能宣称运行正常 |

`ready/running` 不证明 WireGuard 有握手、手机已在线或出口请求成功。`proxy_reachable` 与出口 IP 证据仍分别报告；配置里的出口名称是配置标签。快速 `status --no-ip-check` 不隐式 bootstrap、不刷新授权或探测公网出口；端口有其他 listener 不冒充本实例。

公开 CLI status 保留旧字段并增加 `engine_state`、`instance_id`、`config_generation`、`control_protocol_version`、`error_code` 和端口冲突诊断 `port_occupied`；控制秘密不在这些字段中。本地已建立 [CLI JSON v1 同源生成与 SDK 校验](cli-json-contract-v1.md)，输出增加 `contract_version=1`；完整 HTTP/Admin 合同、GUI 运行时校验和跨版本矩阵仍待 P1.6 验收。

Windows 显式设置并读回运行文件的 ACL，限 home 所有者及已有机器控制权的 SYSTEM/Administrators；同时核验对象 owner，不能只看 DACL。文件必须是无重解析的单硬链接普通文件，在持有句柄下核对 file ID 后才设置权限。home 和运行目录验证所有者与修改权限，拒绝目录越界、junction 和其他账户可修改的目录；不重写目录 DACL，避免 ACL 继承传播改变目录外硬链接对象。不靠 Go `0600` 推定安全。macOS 验证 UID 与文件权限。控制安全不承诺隔离同一 OS 用户或机器管理员的恶意程序。

## Windows 系统代理恢复

恢复 journal schema v1 保存允许字段的原始存在性、registry type/bytes 和本次写入值，区分不存在与空值。更改 OS 配置前先持久化 journal。

启用系统代理前必须取得成功的 CLI status 命令结果，并确认 `running=true`、`engine_state=ready`、`proxy_reachable=true`、没有 error/error_code 且地址有效。配置地址存在或此前 start 曾成功都不能替代这次核验；拒绝时不进入系统代理 adapter，诊断持续可见。状态核验与随后进程崩溃之间仍有窗口，尚未实现与 CLI 生命周期共享的原子租约。

GUI 代理事务在同一用户共享的 app config 目录持有持久 `proxy-operation.lock` 文件句柄锁，覆盖 journal 加载、registry 读写、通知和 journal 删除；竞争者先返回错误，不进入这些副作用。打开时禁止删除共享，释放只 unlock/close，不删除锁文件，避免跨 Windows 登录会话把锁拆成多个文件。验证采用独立文件句柄的实际内核锁，尚未演练真实双登录会话；这不代替 P3.1 的 CLI 用户级代理租约。

- 无 journal：restore 不查询、不写用户代理，不默认关闭代理。
- 有效 journal：只恢复仍属于本次写入的字段；已是原值的字段可幂等跳过。
- 外部修改：整组预检查与逐字段再次读取遇到冲突就停止，不盲回快照；保留 journal 和诊断。
- 读取、解析、写入、通知、最终读回或删除失败：保留 journal，返回真实错误，可重试。
- 旧格式/损坏 journal：拒绝自动猜测归属，显示文件位置与人工核对步骤。
- GUI 状态持续显示恢复错误。退出恢复失败时保留 GUI 与引擎，正常窗口关闭仍按原语义进入托盘。

第三方程序不参与本项目锁，Windows registry 没有本方案可用的跨程序全局 CAS。逐次读取与读回缩小竞争窗口，不保证外部竞争绝对无损。

若是旧/损坏记录，先在 Windows 设置中人工核对并恢复目标代理，再将该记录重命名归档后重试退出；若是外部冲突且用户决定保留第三方新设置，人工确认后归档记录即可解除恢复阻断。程序不自动删除证据或替用户做这个决定，不能只提示无限重试。

## 验证边界与未完成合同

本地门禁入口：`scripts/check-client-safety.ps1`。GUI 先在其目录执行 `npm ci`。门禁采用产品 build tags、Go test/vet/race、Python SDK 消费者、Svelte check 与 Rust library tests；失败立即停止，不能继续发行。

Rust library 验证临时禁用 sidecar 打包输入，只对合成注册表键运行 adapter，不广播真实 WinINET 设置变更。这不是正式安装包、签名、真实用户系统代理或 macOS GUI 验收。

全阶段的请求/operation ID、rotate 幂等与查询、完整合同消费者、OS 用户代理租约集成、跨版本完整迁移、Mac 实机、签名/安装升级和生产观察仍按总计划保持未完成。局部实现不将这些阶段声明为已完成。

平台 API 依据：[Microsoft LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)、[UnlockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-unlockfileex)。锁范围与句柄释放规则须保持一致；不能借此承诺 Windows registry 的全局 CAS。
