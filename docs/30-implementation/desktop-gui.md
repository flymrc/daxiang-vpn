# 桌面 GUI 客户端实现方案

> 角色：`clients/desktop-gui/`——面向不想用命令行的终端用户的跨平台 GUI。
> 本文记录实现方案与当前实现状态；改动 GUI 行为时同步回填。

## 目标

给普通客户一个「点一下就连」的图形客户端，把 `zhvpn.exe` 已有的能力（授权、连接、状态、换 IP）包成一个窗口 + 托盘的应用，**不让客户接触命令行、WireGuard、sing-box、端口、出口地址等细节**。

客户能做的事：

- 输入授权码登录。
- 一个开关：连接 / 断开。
- 看到状态：未连接 / 连接中 / 已连接 / 错误，以及出口名和出口 IP。
- 出口 IP 分开展示 IPv6 / IPv4；Hub 不作为出口兜底,IPv4 烂就如实显示异常。
- 一键换 IP（手机卡出口）。
- 默认只启动本地代理，用户可在浏览器或目标软件里手动配置。
- 可勾选「全局代理」自动设置 Windows 系统代理，浏览器和遵循系统代理的 App 无需手动配置。
- 可勾选「高性能模式」调用 `zhvpn start --fast`，使用系统网络栈路径；它不是完整 TUN 全局路由。

## 选型决策

### 框架：Tauri v2 + CLI 子进程（已定）

| 维度 | Tauri（选用） | Wails | Electron |
| --- | --- | --- | --- |
| 后端语言 | Rust 外壳 | Go | Node |
| 复用现有 Go 逻辑 | 经 CLI 子进程边界，全复用 | 同进程 import | 经 CLI 子进程边界，全复用 |
| 体积 / 内存 | 轻（~10MB，系统 WebView） | 轻 | 重（~150MB+，吃内存） |
| 长期维护风险 | 低（公司背书 CrabNebula） | 较高（小项目） | 低（OpenJS 基金会） |
| 适配本项目 | ✅ 不弃坑 + 轻量 + 集成干净 | 项目长期性存疑 | 能力过剩、对托盘 VPN 偏重 |

结论：**Tauri v2**。公司背书不易弃坑、体积轻适合客户分发；本仓库的 `zhvpn.exe` 又恰好把提权 / 引擎 / PID 这些硬骨头都自包含了，子进程集成很干净。

## 范围

### 第一阶段（本方案）：仅 Windows

`zhvpn.exe` 的 `start/stop/引擎` 逻辑目前**只在 Windows 实现**（`shared/proxy/platform_nonwindows.go` 中 mac/Linux 全是「当前平台未实现」）。因此 GUI 复用 CLI ⇒ 第一阶段只能出 **Windows** 包。

### 后置：macOS

macOS 包的前置依赖是先在 `shared/proxy` 补出 macOS 引擎后端（系统 TUN / 路由 / 提权），这是另一块独立的大活，单列里程碑 M5，不阻塞 Windows 上线。

## 架构

```text
┌─────────────────────────────────────────────┐
│  Tauri 应用 (纵横 VPN)                          │
│                                               │
│  ┌─────────────┐   invoke    ┌─────────────┐  │
│  │  WebView 前端 │ ─────────► │  Rust 后端    │  │
│  │  (Svelte)   │ ◄───────── │  commands    │  │
│  └─────────────┘   事件/返回  └──────┬──────┘  │
│   连接开关/状态/出口IP/换IP          │ 子进程     │
│                              ┌──────▼──────┐  │
│                              │ sysproxy.rs │  │ 设/还原系统代理
│                              └──────┬──────┘  │
└─────────────────────────────────────┼─────────┘
                                       │ 执行(sidecar)
                              ┌────────▼─────────┐
                              │  zhvpn.exe        │
                              │ login/start/stop/ │
                              │ status/rotate-ip  │
                              └────────┬──────────┘
                                       │ 内嵌 sing-box 引擎
                                       ▼
                          WireGuard→Hub→出口 HTTP 代理→外网
```

要点：

- 所有「难」的部分（提权拉 `--fast` 引擎、后台引擎进程、PID 管理、运行指纹重启）都封在 `zhvpn.exe` 内部，Rust 侧只负责拼参数、跑子进程、解析输出。
- 系统代理的设置 / 还原放 **Rust 侧**（不污染 CLI），断电 / 崩溃的兜底还原也在 Rust 侧处理。

## CLI 机器接口改造（前置，M1）

GUI 解析中文文本不稳，先给 `zhvpn.exe` 加机器可读输出。**只加 `--json`，不改默认人读行为**，改动落在 `clients/cli/internal/app/app.go`：

1. `status --json`：
   ```json
   {"running":true,"proxy":"127.0.0.1:7890","proxy_reachable":true,
    "egress":"日本住宅","egress_ip":"240b:...","egress_ipv6":"240b:...","egress_ipv4":"133.106.x.x"}
   ```
   （未连接 / 无配置时为 `{"running":false,"proxy_reachable":false,"error":"…"}` 并退出非 0。
   当前不带 `fast` 字段——运行模式没有持久化存储，GUI 自己知道是以哪种模式拉起的。）
   `status --json --no-ip-check` 只返回本地运行状态、代理地址和出口名，不访问外部公网 IP 服务，也不重复调用 Hub bootstrap，供 GUI 高频轮询使用。CLI 登录/首次迁移旧 token-only 配置时会落一份本地状态缓存；`start` 仍会强制重新 bootstrap 以拿到最新 WireGuard/出口配置，状态缓存不持久化 WireGuard 私钥。
2. `login --json` / `rotate-ip --json`：
   ```json
   {"ok":true,"egress":"日本住宅","proxy":"127.0.0.1:7890"}
   {"ok":false,"error":"授权码无效或已过期"}
   ```
   失败时仍以非 0 退出码退出，GUI 双保险（退出码 + JSON）。
3. `start` / `stop`：维持现状（退出码 0/非 0 + stdout 文本即可）。`start` 内部已 `waitForTCP` 阻塞到代理可用或超时，GUI 期间显示「连接中」。

## 连接行为与系统代理（关键 UX）

`zhvpn.exe` 两种模式语义不同，GUI 必须替客户兜住「点一下就生效」：

| 模式 | 机制 | UAC | 流量覆盖 | 客户需手动设置吗 |
| --- | --- | --- | --- | --- |
| 默认（用户态） | 本地代理 `127.0.0.1:7890` | 否 | 仅指向该代理的 App | 要：得有人把系统/浏览器代理指过去 |
| `--fast`（实验加速） | 系统网络栈 + 内网段路由 | 是（每次启动弹一次） | 非完整全局 TUN | 仍应设置系统代理兜底 |

**采用方案**：

- **默认 = 用户态本地代理**：连接时只启动 `127.0.0.1:7890`，不改系统代理。用户可把浏览器或目标软件手动指向该代理。
- **全局代理选项 = 用户态代理 + GUI 自动设 Windows 系统代理**：用户勾选后，连接时由 Rust 写 WinINET（注册表 `Internet Settings` 的 `ProxyEnable`/`ProxyServer` + `InternetSetOption` 刷新），`ProxyServer` 使用显式协议映射 `http=127.0.0.1:7890;https=127.0.0.1:7890;socks=127.0.0.1:7890`，断开 / 退出 / 崩溃时还原。**免管理员、无 UAC，对浏览器和大多数 App 即开即用。**
- **高性能模式 = `--fast`**：与「全局代理」独立。现有 sing-box/WireGuard 配置只覆盖 Hub/内网段，并不是完整 `0.0.0.0/0` TUN 接管。后续真正全局 TUN 需要单独补 tun inbound、auto_route、DNS/IPv6/断开恢复验证。

## 工程结构

```text
clients/desktop-gui/
├─ README.md                 # 删占位，写真实工程说明
├─ src-tauri/
│  ├─ tauri.conf.json        # externalBin 指向打包进来的 zhvpn.exe
│  ├─ Cargo.toml
│  ├─ binaries/              # 构建前把 zhvpn-<target-triple>.exe 拷进来
│  └─ src/
│     ├─ main.rs             # 应用入口 + 托盘
│     ├─ cli.rs              # sidecar 调用 + JSON 解析
│     └─ sysproxy.rs         # Windows 系统代理 开/关/还原
├─ src/                      # 前端（Svelte）
│  ├─ App.svelte             # 登录 / 连接开关 / 状态 / 出口IP / 换IP / 模式
│  └─ lib/api.ts             # invoke 封装
├─ package.json
└─ build.ps1                 # 一键：go build zhvpn.exe → 拷 sidecar → tauri build
```

## Rust 命令面（Tauri commands）

| 命令 | 内部行为 |
| --- | --- |
| `login(token)` | 跑 `zhvpn.exe login <token> --json`，解析 ok/egress |
| `connect(globalProxy, fast)` | `start [--fast]`；`globalProxy=true` 时连接成功后由 `sysproxy.rs` 设 Windows 系统代理 |
| `disconnect()` | 先还原系统代理，再 `zhvpn.exe stop` |
| `status()` | `status --json --no-ip-check` → 回前端本地运行状态 |
| `status_ip()` | `status --json` → 按需刷新公网出口 IPv4/IPv6 |
| `app_version()` | 返回 GUI 版本号，前端显示为 `v0.4.x` |
| `rotate_ip()` | `rotate-ip --json`，期间前端禁用按钮 + 进度 |
| `get_config()` | 读已保存配置（出口名、端口等）供 UI 展示 |

每个命令 = 拼参数 → 跑 sidecar → 解析 JSON / 退出码 → 回前端。

## 前端 UI

单窗口 + 托盘，界面极简：

- **未登录页**：授权码输入框 → 登录。
- **主页**：
  - 大开关「连接 / 断开」。
  - 状态机：`未连接 → 连接中 → 已连接`，失败进 `错误` 并显示原因。
  - 状态行：出口名、**出口 IPv6**、**出口 IPv4**、本地端口。
  - 「换 IP」按钮（仅手机卡出口可用），点击后禁用 + 进度。
  - 「全局代理」复选框：默认关闭；关闭时只启动本地代理，开启时自动配置 Windows 系统代理。
  - 「高性能模式」复选框：默认关闭；开启时调用 `zhvpn start --fast`。
- **托盘**：连接状态图标；右键菜单 连接 / 断开 / 打开主界面 / 退出；关窗最小化到托盘。
- 主界面打开时约 5s 轮询一次 `status --json --no-ip-check`，只刷新本地连接状态。
- CLI `status` 读取登录/start 写入的本地状态缓存；主窗口和托盘同时轮询不会把 Hub `/api/client/bootstrap` 打成心跳。
- 公网出口 IP 是外部观察结果，本机无法直接从网卡/代理内部可靠知道 NAT 后地址；因此只在连接成功、首次打开、换 IP 后、以及低频定时（约 60s）调用 `status_ip()` 刷新。前端保留上一轮有效 IPv4/IPv6，避免探测空窗把 UI 刷成「获取中」。

## Windows 打包（方案 A：不装服务）

| 方案 | 后台服务 | UAC 体验 | 复杂度 |
| --- | --- | --- | --- |
| **A. 仅打包 zhvpn.exe（采用）** | 否，作为 sidecar 与 app 同包 | 默认零 UAC；实验 `--fast` 才弹一次 | 低 |
| B. 装 Windows 服务 | 是，开机自启、提前提权 | 全程零 UAC | 高，要写服务 + 权限 + 自更新 |

采用 **A**：`zhvpn.exe --fast` 自己会弹 UAC 拉提权引擎，**不需要常驻服务**。B 仅当未来要「完整 TUN 全局也永不弹 UAC」时才值得，后置。

打包细节：

- `tauri build` 出 **NSIS** 安装包（`bundle.targets: ["nsis"]`，按用户安装 `installMode: currentUser`，免管理员；NSIS 默认创建开始菜单/桌面快捷方式）。
  - **不出 MSI**：WiX `light.exe` 在 unicode 产品名「纵横 VPN」上会失败；NSIS 处理 UTF-8 正常，且是面向消费者的首选安装器（MSI 偏企业 GPO）。
- WebView2 走 `downloadBootstrapper`（Win11 自带，旧系统自动拉起安装）。
- `zhvpn.exe` 作为 `externalBin` sidecar 随包，构建前由 `build.ps1` 用 `go build -tags with_gvisor` 产出并按 target-triple 命名拷入 `src-tauri/binaries/`；Tauri 打包时去 triple 后缀复制为主程序旁的 `zhvpn.exe`。
- 日常构建可从仓库根目录运行 `scripts/build-desktop-gui.ps1 -Target x64|arm64|both`。该脚本只是包装 `clients/desktop-gui/build.ps1`,并额外打印安装包路径、大小和 SHA256。`x86` 作为 x64/amd64 别名处理,当前不提供 32 位 Windows GUI 包。
- 产品显示名「纵横 VPN」（`productName`），二进制名 `zhvpn-desktop`（`mainBinaryName`，ASCII 避坑）；`publisher`/`copyright`/描述齐全；图标为自绘品牌图（蓝绿底白色 `ZH` 字标，`tauri icon` 生成全套）。代码签名后续再补。

## 文档同步（按 AGENTS.md）

开工 / 上线时同步：

- 本文（`docs/30-implementation/desktop-gui.md`）随实现回填状态。
- `docs/10-architecture/system-architecture.md`：客户端侧新增 GUI 角色（**上线时**改）。
- `clients/desktop-gui/README.md`：删占位，补真实工程说明。
- `docs/90-history/worklogs/YYYY-MM-DD-desktop-gui-*.md`：记录每天实质工作。

## 里程碑

| 里程碑 | 内容 | 预估 |
| --- | --- | --- |
| **M1** ✅ | CLI 机器接口：`status/login/rotate-ip --json` + 测试 | 已完成 |
| **M2** ✅ | Tauri 工程骨架 + sidecar 打包 + `connect/disconnect/status` 走通 | 已完成 |
| **M3** ✅ | Rust 系统代理开关 + 换 IP + 托盘 + 状态轮询 + UI（代码完成，待人工点验） | 已完成 |
| **M4** ✅ | NSIS 安装包（按用户安装 + 快捷方式）+ 品牌图标 + 文档/架构同步 | 已完成 |
| **M5（后置）** | 先补 `shared/proxy` macOS 引擎，再开 macOS 包 | 单列 |

## 验收标准（Windows / 第一阶段）

- [ ] 输入授权码可登录，登录态持久化。
- [ ] 点「连接」后状态依次走 连接中 → 已连接，并显示出口名与出口 IP。
- [ ] 已连接后不因普通状态轮询反复刷新公网 IP；IPv4/IPv6 可同时展示。
- [ ] 默认模式下连接后只开放本地代理，需要手动把目标软件指向 `127.0.0.1:7890`。
- [ ] 勾选「全局代理」后，浏览器**无需手动配置**即可访问日本网站（GUI 已自动设系统代理）。
- [ ] 点「断开」后系统代理被还原，状态回到未连接。
- [ ] 应用崩溃 / 强退后重开，系统代理不残留（兜底还原生效）。
- [ ] 「换 IP」能触发并在出口恢复后刷新出口 IP。
- [ ] 全局代理模式下 Windows 系统代理指向本地代理，断开后还原。
- [ ] 关窗最小化到托盘，托盘可连接 / 断开 / 退出。
- [ ] NSIS/MSI 安装后有桌面快捷方式，卸载干净。

## 当前实现状态

- **M1 已完成**（2026-06-09）：`zhvpn.exe` 的 `status / login / rotate-ip` 支持 `--json`，默认人读输出不变。
  - 实现：`clients/cli/internal/app/app.go`、`clients/cli/main.go`（新增 `app.ErrSilent`：JSON 命令失败时退出非 0 但不再向 stderr 重复打印）。
  - `printJSON` 关闭 HTML 转义，`<授权码>` 等保持可读；JSON 命令失败输出 `{"ok":false,"error":…}`（status 为 `{"running":false,…,"error":…}`）并退出非 0。
  - 测试：`go test ./clients/cli/...` 通过；新增 `wantJSON`/`hasFlag`/`--json` 解析用例。
- **M2 已完成**（2026-06-09）：Tauri v2 + SvelteKit 工程在 `clients/desktop-gui/`，sidecar 调用现成 `zhvpn.exe` 跑通。
  - 工程：`create-tauri-app`（svelte-ts，Tauri v2）；前端 `src/routes/+page.svelte` + `src/lib/api.ts`（登录 / 连接开关 / 状态 / 出口 IP）。
  - 后端：`src-tauri/src/lib.rs` 用 `tauri-plugin-shell` 调 sidecar，四个命令 `login/connect/disconnect/status`（见 README 映射表）；`tauri.conf.json` `externalBin: binaries/zhvpn`，窗口标题「纵横 VPN」。
  - sidecar：`build.ps1` 用 `go build -tags with_gvisor` 产出 `zhvpn-<triple>.exe`（不入库，`.gitignore`）。
  - 工具链：本机新装 Rust 1.96（MSVC）+ VS Build Tools 2022（C++ 工作负载）。
  - 验证：`cargo build` 通过；`npm run build` 通过；`tauri build` 产出 MSI/NSIS（sidecar 以 17.2MB `zhvpn.exe` 随包，置于主程序旁）；运行 release 程序，窗口正常，`status` 命令实际拉起 `target\release\zhvpn.exe status --json`（runtime sidecar 走通）。
  - 尚未做（属 M3）：系统代理自动设/还原、换 IP、托盘、登录→连接→真实代理的端到端人工点验。
- **M3 已完成**（2026-06-09，代码层，待人工点验）：
  - 系统代理：`src-tauri/src/sysproxy.rs`（直接写 WinINET 注册表并刷新系统代理）。勾选「全局代理」后，`connect` 成功会自动把 Windows 系统代理指向本地代理；默认局部代理模式不改系统代理。`disconnect` 还原。首次打开把原状态写 `proxy-backup.json`，还原后删除——**崩溃也能恢复**：启动时若备份存在且代理实际未运行则自动还原。
  - 托盘：`tauri` 加 `tray-icon` feature；左键开主界面、右键菜单 打开/退出；**关窗收进托盘**（保持连接），退出走托盘「退出」并还原系统代理。
  - 换 IP：`rotate_ip` 命令（`zhvpn rotate-ip --json`）+ 主界面「换 IP」按钮。
  - 2026-06-11 起界面不再暴露旧「全局模式 / 系统 TUN」复选框；改为「全局代理」和「高性能模式」两个独立复选框。默认局部代理；全局代理负责设置 Windows 系统代理；高性能模式负责传递 `--fast`。
  - 验证：`cargo build` 通过（1m，含 `sysproxy`/`tray-icon`）；`npm run build` 通过；运行 debug 程序窗口「纵横 VPN」正常、托盘 setup 未 panic。**待人工点验**：登录→连接（看浏览器是否免设置直连日本）→断开（系统代理还原）→换 IP→关窗到托盘→托盘退出。
- **M4 已完成**（2026-06-09）：打包细化 + 品牌图标 + 文档/架构同步。
  - 品牌图标：自绘蓝绿底白色 `ZH` 字标源图（System.Drawing 生成），`tauri icon` 出全套，替换脚手架默认 logo；删掉用不到的 android/ios 图标。
  - `tauri.conf.json`：`productName` 改「纵横 VPN」+ `mainBinaryName: zhvpn-desktop`（ASCII 二进制名）；`bundle.targets: ["nsis"]`、`installMode: currentUser`、`webviewInstallMode: downloadBootstrapper`、`publisher/copyright/category(Utility)/描述`。
  - 验证：`tauri build` 产出 `bundle/nsis/纵横 VPN_0.1.0_x64-setup.exe`（7.5MB，sidecar `zhvpn.exe` 17.2MB 随包置于主程序旁）。踩坑：`category` 只接受 Apple 枚举（`Network` 非法→`Utility`）；unicode `productName` 让 WiX MSI 失败→去掉 MSI 只留 NSIS。
  - 架构文档：`system-architecture.md` 角色表新增「桌面 GUI」。
- **桌面 GUI 四个里程碑（M1–M4）代码与打包完成**；剩真实出口下的端到端人工点验，及后置项：代码签名、macOS（M5，需先补 `shared/proxy` mac 引擎）。
- **状态轮询加固已完成**（2026-06-10）：主界面 `refresh()` 会跳过正在进行中的刷新，Rust 后端 `status_impl` 用全局 async mutex 串行化 `zhvpn status --json`。主窗口和托盘同时存在时，不再堆积多个 status sidecar 子进程，降低本机代理卡顿和误判断线的概率。
- **0.4.4 状态展示修正已完成**（2026-06-11）：
  - GUI 高频轮询改为 `zhvpn status --json --no-ip-check`，只看本地运行状态，不再每 5 秒请求公网 IP endpoint。
  - 新增 Tauri `status_ip()`，只在连接成功、首次打开、换 IP 后和低频刷新时探测公网出口。
  - CLI `status --json` 新增 `egress_ipv4` / `egress_ipv6` 字段，旧 `egress_ip` 保持兼容并优先返回 IPv6。
  - 前端分开展示「出口 IPv6 / 出口 IPv4」，并保留上一轮有效值。
  - GUI 版本号显示在主界面底部，当前为 `v0.4.4`。
  - 单例排他：GUI 主进程用 Windows 命名 Mutex 防止重复打开；CLI `start/stop/login/import/logout` 在同一 `ZHVPN_HOME` 下共享操作级命名 Mutex，避免并发操作启动多套同一本地实例。
- **0.4.5 出口语义修正已完成**（2026-06-11）：
  - Hub 只做 Hub,不作为 IPv4 兜底出口；`egress/reverse` 已废弃并忽略 `v4_only_direct`。
  - CLI status 若观测到 IPv4 等于 Hub endpoint IP,不再把它作为 `egress_ipv4` 返回。
  - GUI 若 IPv4 探测完成但无手机 IPv4,如实显示 `不可用`;IPv6 仍单独显示 Rakuten IPv6。
  - 第二次打开 GUI 时不再静默退出,而是激活已有主窗口。
  - 主界面新增“复制诊断”基础版,便于排查时一次性拿到版本、连接状态和最近一次 IPv4/IPv6 探测结果。
  - GUI 版本号显示为 `v0.4.5`。
- **0.4.6 安装与布局修正已完成**（2026-06-11）：
  - 主界面布局压缩,避免默认 420x600 窗口出现右侧滚动条。
  - Windows NSIS 安装前先尝试 `zhvpn.exe stop` 并清理残留 sidecar,避免升级时覆盖 `zhvpn.exe` 被文件锁拒绝。
  - 若公网 IP 探测暂时返回空,GUI 不再等待完整 60 秒刷新周期;未拿到任何 IP 时 5 秒重试,复制诊断前也会强制刷新一次。
  - GUI 版本号显示为 `v0.4.6`。
- **0.4.7 打包已完成**（2026-06-11）：
  - 将 0.4.6 的安装器释放 sidecar 文件锁、紧凑布局、IP 探测快速重试修正打成独立版本,避免继续复用同名 0.4.6 安装包。
  - GUI 版本号显示为 `v0.4.7`。
- **0.4.8 打包已完成**（2026-06-11）：
  - 继续提升 GUI 包版本,用于区分包含最新 sidecar 与 GUI IP 快速重试逻辑的安装包。
  - GUI 版本号显示为 `v0.4.8`。
- **0.4.9 bootstrap 节流已完成**（2026-06-14）：
  - `zhvpn login` / `zhvpn start` 写入本地状态缓存,但不持久化 WireGuard 私钥。
  - `zhvpn status --json --no-ip-check` 和 GUI 高频轮询只读本地缓存,不再每 5 秒触发 Hub bootstrap。
  - 旧 token-only 配置会在第一次需要状态缓存时自动迁移一次。
  - GUI 版本号显示为 `v0.4.9`。
