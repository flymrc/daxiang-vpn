# 2026-10-05 zhvpn 项目重新审计

结论：综合工程成熟度 **49/100**（加权原始值 48.5）。角色划分和核心 Go 骨架可继续使用，连接与构建能力仍在；离可持续发行的桌面产品还有明显缺口，主要来自权限生命周期、公网认证链路、客户端退出恢复和发布门禁。无需先做大规模重构；应优先收敛确定的安全与生命周期缺陷。

## 范围、版本与证据边界

- 时间：2026-10-05 JST。本轮重点为 `clients/cli`、`clients/desktop-gui`、`shared`、`sdk/python`，同时检查其依赖的 Hub 授权、reverse 出口、构建和运维路径。客户 Android App 只按现有 Slice 0/canary 状态识别，不作为已发布产品评分。
- 主工作区 `HEAD=e69645a`；GitHub `main=92379a3`。`fa4190a`、`e69645a` 是本地未推送提交；工作区另有既有 Android、文档、`go.mod/go.sum` 未提交改动。本轮没有把它们混进部署分支。
- 生产 Hub 本日已上线 `e5be358` reverse idle-preempt；部署文档 `c737cb0` 已推到 `fix/hub-idle-preempt`。审计期间远端追加了另一会话的文档提交 `54c9eeb`，为 `c737cb0` 后代且无源码修改。本轮不把该会话的 Jetstar 报告算作自己的验收证据；用户已取消本轮 Jetstar 测试。
- 安装在本机的 CLI 返回 `product=cli version=local protocol_version=2`，文件修改时间为 2026-10-04；GUI 源码版本三处一致为 `0.4.11`。版本 `local` 与嵌套仓库的父仓库 VCS 元信息不足以映射到可追踪的客户端发行提交。
- Hub 只读检查窗口 01:31–01:41 JST：`zhreverse` 两条手机会话、96/48 限额、120000ms idle timeout、10000ms preempt threshold；手机 IPv4/IPv6 出口实测成功。01:41 快照的 `proxy_idle_preemptions=20` 证明线上发生过抢占，不能证明某个网站业务完成，且首轮活跃连接突发仍可能合法拒绝。
- 没有审计性生产写入、真实 token bootstrap、换 IP、客户端停启、升级安装、真实过期用户测试或压力攻击。前一项已授权部署的公钥追加和服务重启另有部署工作日志。
- 有效发现区分 **实时运行证据**、**隔离复现**、**源码确定**和**尚未验证的环境行为**。本报告不宣称发生过凭据泄露、攻击入侵、全部 HTTPS 被解密或真实用户误杀。

## 多维评分

分数为 0–10 工程成熟度判断，非性能 benchmark 或统计置信区间。8 分代表关键失败路径及持续发行证据较完整；6 分代表基本可用但有明确保障缺口；4 分代表重要行为或权限边界尚未收尾。综合分按下列权重计算。

| 维度 | 分数 | 权重 | 加分证据 | 主要扣分 |
| --- | ---: | ---: | --- | --- |
| 架构与可维护性 | 6.5 | 10% | 角色目录、Go 根 module、GUI/SDK 复用 CLI、平台 build tags | app.go 同时编排多种副作用，生命周期信息不够强；reverse 大单文件增加导航成本 |
| 核心连接与恢复 | 5.5 | 20% | 已有代理可用，双 reverse 会话、限额/回收/抢占和禁止 Hub 出口回退 | 裸 PID 管理、端口即运行、macOS 操作不串行、SDK 超时预算失配 |
| 安全与授权隔离 | 3.5 | 20% | HTTPS 新入口、本地生成 WG 私钥、Argon2id、CSRF、会话哈希、审计掩码 | 公网裸 reverse TCP、HTTP legacy 私钥路径、到期不撤销数据面、私网 XFF 信任过宽 |
| 桌面用户行为 | 4.5 | 10% | 薄 GUI、版本/协议检查、状态轮询 | 退出会误关既有代理，恢复错误被忽略并删除备份，升级全杀同名进程 |
| 自动测试与验证 | 6.0 | 10% | 当前 Windows Go 112 用例通过，vet 和 GUI 类型检查通过 | GUI/Rust 关键生命周期无回归测试，发布不执行检查，race/跨平台/安装流程未完整验收 |
| 依赖维护 | 4.0 | 5% | 有 Go/npm/Rust 锁定信息，现代工具链可构建 | 当前扫描存在 Go 受影响符号与 GUI 开发依赖 advisory，缺少持续扫描闭环 |
| 发布、升级与追踪 | 3.5 | 10% | GUI 三份版本一致，sidecar product/version/protocol 门禁、SHA256 产物 | 无仓库 CI、签名/更新渠道待补，本机 local 构建难映射发行版本 |
| 可观测性与运维 | 6.0 | 10% | session-health、峰值、延迟分位数、迁移观测、SQLite 保留策略、部署备份 | 日志初始化会删现用日志，CONNECT success 不能证明网页成功，单 Hub/手机依赖 |
| 文档与线上一致性 | 4.0 | 5% | 有架构、runbook、worklog，部署已补文档 | GitHub main/本地/生产三个状态分离，已部署 observer 未推、Motorola 与 Pixel 记录混杂 |

仅按桌面 GUI 看，体验与交付保障低于后端/CLI；不能用 Go 测试整体通过替代桌面生命周期验收。

## 最高优先级发现

### F1 / P1：公网反向 TCP 明文认证

- 源码：`egress/reverse/main.go:366` 裸 TCP listener；`:2176` 裸 TCP dial，`:2184` 明文传递长期共享 token，`:395` 直接启动 yamux。已部署 idle-preempt 基点保留同样协议。
- 运行：本日启动日志 `transport=tcp`；Hub `*:39093`，UFW 对公网 IPv4/IPv6 放行该 TCP 端口。手机侧 Motorola 的兼容客户端必须遵循 Hub 当前可接受的 hello 格式。
- 影响：链路观察者可以取得认证材料；持有 token 的攻击者可尝试作为冒牌出口接入。网站自己的 HTTPS 仍受目标 TLS 保护。
- 证据：源码 + 当前生产监听/配置，高置信度；未抓取真实 token，未攻击或证明发生泄露。
- 整改方向：为现有 TCP 数据面添加 TLS 与 Hub 身份校验，或在经过性能验证后选择受认证加密的隧道承载；Hub/手机协议配套灰度，并在迁移后轮换共享认证材料。不要直接切回历史 QUIC 假定其性能已验收。

### F2 / P1：公网 HTTP legacy 入口仍可返回 WireGuard 私钥

- 源码：`hub/main.go:19,53` 明文兼容 listener；`hub/internal/auth/server.go:263` 仅请求含客户端公钥时去掉私钥，`:304` 兼容请求可以返回记录内的原配置。
- 运行：`*:18080` 与 UFW 公网 IPv4/IPv6 放行，Windows 无代理直连 `/healthz` 返回 `ok`。仅统计生产 `tokens.yaml` 的字段名，仍有 **21 个 `private_key` 字段**，没有读取或输出值；这不是 21 个当前活跃用户的统计。
- 影响：持有效 token 的 legacy bootstrap 请求仍可能经明文链路取得并传递私钥，HTTPS 新入口并未关闭旧路径。
- 证据：源码 + 当前公网健康入口 + 非秘密字段计数；未请求真实 bootstrap，不能宣称实际私钥泄露。
- 整改方向：结合现有迁移 observer 核实 legacy 消费者，受控收口公网 18080、强制本地公钥协议、再清理 legacy 私钥。该变更需要保留兼容窗口与明确恢复步骤。

### F3 / P1：token 过期或禁用不撤销已建立的 WireGuard 权限

- 源码：`hub/internal/auth/store.go:71` 在请求时查 enabled/expiry；`server.go:398` 添加/更新 peer，没有动态公钥与 token 的持久绑定及到期删除 peer 流程；`hub/main.go:26` 只在启动时加载配置。
- 影响：拒绝后续 bootstrap 不会主动切断已建隧道；用户是否仍能使用受具体 peer 与运行状态影响，需要显式撤销数据面才能保证到期生效。
- 证据：源码确定，未真实过期用户复现。
- 整改方向：记录 token/客户端/公钥关系，定义可重入的撤销流程，验证过期、禁用、替换公钥和服务重启后的边界。

### F4 / P1：GUI 退出会关闭用户原来的系统代理

- 源码：`clients/desktop-gui/src-tauri/src/sysproxy.rs:141` 无备份时执行 `disable()`；`lib.rs:196,319` 的断开和托盘退出无条件调用 restore。
- 触发：用户已有另一套系统代理，zhvpn 从未启用全局代理，随后断开/退出。
- 影响：zhvpn 会关闭不属于自己的系统代理设置。
- 证据：源码确定，未修改用户注册表做实际复现。
- 整改方向：只恢复本次会话确实拥有的修改；无备份且未接管时保持原状态。

### F5 / P1：代理恢复失败仍删除备份并返回成功

- 源码：`sysproxy.rs:118–139` 忽略 JSON 解析、注册表打开、写入错误，最后删除备份并返回 `Ok`。
- 触发：备份损坏、访问或写入失败。
- 影响：旧代理可能未恢复，恢复材料也丢失。
- 证据：源码确定；未对真实注册表注入失败。
- 整改方向：逐项传播恢复错误；成功确认后才删除备份，失败保留材料并向 GUI 返回可操作状态。

### F6 / P1：PID 复用会误杀无关进程

- 源码：`shared/proxy/engine.go:38–78` 写 PID 但退出不清理；Windows `singbox.go:69,99` 仅检查 PID 存活后 `taskkill /PID ... /T /F`；macOS `platform_darwin.go:64,125` 同样缺少身份校验。
- 触发：引擎退出后 PID 遗留，操作系统把这个 PID 分配给另一个进程，再调用 stop 或需要重启的 start。
- 影响：可能强杀无关进程或进程树。
- 证据：源码确定，未执行真实误杀。
- 整改方向：保存并核对 PID、启动时间、可执行文件/工作目录及会话身份；正常退出清理自己的 PID，未能验证身份时不强杀。

## 次优先级发现

| ID / 严重度 | 问题与触发 | 证据 | 整改方向 |
| --- | --- | --- | --- |
| F7 / P2 | 兼容授权入口把所有私网来源当可信代理；WG 用户持有效 token 可伪造同一 XFF 绕过来源租约 | `hub/internal/auth/server.go:894,913,542`；纯单元走 ClientIngressCompat，不带 XFF 第二源 409、伪造相同 XFF 两源均 200 | 只信任明确代理地址/专用入口；租约使用可信设备或公钥身份 |
| F8 / P2 | 任意普通 TCP listener 占据代理端口时，status 报 running、proxy_reachable=true 并显示缓存出口 | `clients/cli/internal/app/app.go:668–675`；随机端口与合成配置已隔离复现 | 检查引擎/会话身份，并做代理协议健康检查；区分端口可达与出口健康 |
| F9 / P2 | macOS 并发 start/login 无互斥，可争写配置、密钥、PID | `operation_lock_nonwindows.go:7` 空实现，`app.go:439,550`；未在 Mac 实跑竞争 | 实现跨进程锁与原子写入；测试两个入口并发，不用 Windows 结果替代 Mac |
| F10 / P2 | Python 默认超时 30 秒，小于 CLI rotate 等待 75 秒；远端已触发但 SDK 提前杀 CLI | `sdk/python/src/zongheng_vpn/client.py:32,81,150`；`app.go:767,971,1039` | 为长操作设置独立预算和可查询操作结果，区分触发成功/恢复完成 |
| F11 / P2 | EnsureDirs 把现用 zhvpn.log/err 当遗留文件删除；Mac 引擎继续写被 unlink 的文件 | `shared/paths/paths.go:65,75–80`；临时目录调用复现删除；`platform_darwin.go:42` 使用相同日志 | 移除每次初始化的现用日志删除；迁移与轮转采用显式受控流程 |
| F12 / P2 | GUI 安装升级按进程名全杀 zhvpn.exe，会终止其他安装目录或 ZHVPN_HOME 的 CLI/SDK 会话 | `installer-hooks.nsh:9`；未执行安装升级 | 只停当前安装拥有的实例，并校验可执行文件路径/实例身份 |
| F13 / P2 | 公网授权 HTTP server 无超时/请求体限额，认证前解码 JSON | `hub/main.go:53,57`；`server.go:191,641`；未做线上压力测试 | 设置 header/body/time 边界并验证异常输入；所有入口一致生效 |
| F14 / P2 | GUI 构建不执行已有类型检查/自动回归，无仓库 CI 与 GUI/Rust 测试 | `clients/desktop-gui/build.ps1:91–106`；`package.json:6–12` | 建立可重复门禁，再对退出恢复、升级、CLI 协议加入行为用例 |

## 当前测试与依赖扫描

- 主工作区 `go test -json ./...`：**112 个测试通过，11 个含测试的包通过，没有 cached 包结果，没有失败**。包括未提交的 wgboot 包，因此不能当作 GitHub main 的测试总数。
- `go vet ./...`：通过。以上为当前 Windows 默认 build tags，不能等同于 macOS、Linux、Android、`with_gvisor` 全组合回归。
- `npm run check`：**0 errors / 1 warning**；警告为 tsconfig 引用 `node` 类型但缺少类型定义。没有完成 Tauri 安装包构建或真实 GUI 行为验收。
- 本日部署分支 `e5be358`：首次 Windows reverse 测试出现既有超短超时用例失败；之后完整包 `-count=3`、新增抢占相关 `-count=10` 通过。本轮未完成完整 race 检查，不把先前失败隐藏成通过。
- 当前 GUI `npm audit --json`：5 个受影响依赖节点（high 3、moderate 1、low 1），涉及 SvelteKit/cookie/devalue/nanoid/postcss；**全部位于开发依赖树，`--omit=dev` 为 0**。这些数字不证明静态 Tauri 应用可以远程利用 SvelteKit 服务端漏洞。[SvelteKit 维护者公告](https://github.com/sveltejs/kit/security/advisories/GHSA-29g2-3rmr-qm68)明确描述其服务端请求场景，需按实际打包路径评估。
- `govulncheck v1.8.0 -json ./...`，官方数据库更新时间 2026-10-01：**39 个独立受影响 advisory，其中 16 个有符号级调用报告、6 个仅到 package、17 个仅到 module**。不能把 39 当作 39 个已证实可利用漏洞，也不能把 JSON 模式的进程 exit=0 当作无漏洞。
- 示例：本地 `golang.org/x/net v0.50.0` 命中 [GO-2026-4559](https://pkg.go.dev/vuln/GO-2026-4559)，官方受影响范围为 `v0.50.0` 到 `v0.51.0` 之前。应优先完成工具链与 x/net/x/crypto 等安全更新的兼容验证，逐项审查实际入口与功能使用条件；本轮没有升级依赖。
- 本机已安装 CLI 另做 `govulncheck -mode=binary` 扫描；二进制保留符号比源调用路径更宽，不拿它直接证明线上可利用。客户 Android 控制目标代码与本机 tools 的扫描命中不能等同于生产 Motorola `dxandroid-control` 的实际版本漏洞。
- 正向保障：GUI/sidecar 有 product/version/protocol 检查、三份 GUI 版本一致、锁文件存在；Admin 已实现密码哈希、CSRF、会话 cookie、审计与保留策略。不能描述成完全没有安全或测试措施。

## 推荐实施顺序与验收

1. **先修桌面生命周期与权限风险**：F4/F5 系统代理归属及恢复、F6 进程身份；同时设计 F1/F2/F3 的线上迁移，避免仅关闭端口导致旧客户断连。验收包括“未接管时退出不改代理”“恢复失败保留材料”“PID 复用不误杀”“过期 peer 确实被撤销”。
2. **修可隔离的源码缺陷**：F7 明确可信代理边界、F8 状态语义、F9 Mac 并发、F10 SDK 长操作预算、F11 日志、F12 安装作用域、F13 API 资源边界。合成测试先验证拒绝和失败路径，再受控实机测试。
3. **建立持续交付最小闭环**：Go test/vet、GUI check、Rust 编译/行为测试、govulncheck/npm audit；记录 product/version/commit/toolchain/hash，测试安装、升级、退出和异常恢复。再评估签名与自动升级。
4. **收拢运行版本与文档**：明确 GitHub main、待合并修复、已经上线但未推送的 observer、Android 目标实现与当前 Motorola 布局；保留当前 dirty 工作区，不直接混推。只在这些高价值缺陷被控制后考虑 app/reverse 的模块拆分。

这是审计交付与优先级建议，不代表授权执行新的生产安全切换或修改用户代理。本轮未实施这些修复。

2026-10-06 计划复核补充：现有客户端安全迁移计划明确“不增加 hosted CI；所有门禁按项目政策在本地运行”。因此 F14 及发布维度中的“无 CI”应理解为发行缺少自动执行的本地质量门禁，不能把没有 hosted CI 单独认定为缺陷。后续采用本地自动门禁，评分须在实际修复与重新审计后更新。[Steelman checkbox 计划](../../30-implementation/zhvpn-steelman-refactor-plan.md)已建立，尚未实施。

## 证据位置

- 原始 Go/npm/漏洞结果：`C:/Users/xuotq/ZeroCoreUnit00v2/.local/zongheng-vpn/audits/20261005/`，`go-test.jsonl`、`npm-audit-gui.json`、`npm-audit-gui-prod.json`、`govulncheck.jsonl`、`govulncheck-summary.json`、`govulncheck-installed-cli.jsonl`。
- 授权隔离 synthetic 用例：`C:/Users/xuotq/ZeroCoreUnit00v2/.local/zongheng-vpn/audit-auth-20261005/hub/internal/auth/audit_test.go`；命令 `go test ./hub/internal/auth -run TestAudit -v`，2 个用例通过，未加入主仓库。
- CLI 状态及日志复现使用临时目录/合成配置，结果为普通 TcpListener 下 `running=true proxy_reachable=true egress=synthetic-egress`、`current_zhvpn_log_removed=true`。没有真实 token/私钥输出。
- 本日授权部署与 Windows 公钥授权：[idle-preempt 工作日志](https://github.com/flymrc/daxiang-vpn/blob/fix/hub-idle-preempt/docs/90-history/worklogs/2026-10-05-zhreverse-idle-preempt.md)位于独立部署分支，主工作区尚未包含该 feature 文件；可通过已推送分支查看。
