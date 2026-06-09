# 桌面 GUI 客户端实现方案

> 角色：`clients/desktop-gui/`——面向不想用命令行的终端用户的跨平台 GUI。
> 本文是实现方案（计划阶段），尚未落代码。开工后随实现进度回填「当前实现状态」。

## 目标

给普通客户一个「点一下就连」的图形客户端，把 `dxvpn.exe` 已有的能力（授权、连接、状态、换 IP）包成一个窗口 + 托盘的应用，**不让客户接触命令行、WireGuard、sing-box、端口、出口地址等细节**。

客户能做的事：

- 输入授权码登录。
- 一个开关：连接 / 断开。
- 看到状态：未连接 / 连接中 / 已连接 / 错误，以及出口名和出口 IP。
- 一键换 IP（手机卡出口）。
- 高级里可选「全局模式」（系统 TUN，弹一次 UAC）和本地端口。

## 选型决策

### 框架：Tauri v2 + CLI 子进程（已定）

| 维度 | Tauri（选用） | Wails | Electron |
| --- | --- | --- | --- |
| 后端语言 | Rust 外壳 | Go | Node |
| 复用现有 Go 逻辑 | 经 CLI 子进程边界，全复用 | 同进程 import | 经 CLI 子进程边界，全复用 |
| 体积 / 内存 | 轻（~10MB，系统 WebView） | 轻 | 重（~150MB+，吃内存） |
| 长期维护风险 | 低（公司背书 CrabNebula） | 较高（小项目） | 低（OpenJS 基金会） |
| 适配本项目 | ✅ 不弃坑 + 轻量 + 集成干净 | 项目长期性存疑 | 能力过剩、对托盘 VPN 偏重 |

结论：**Tauri v2**。公司背书不易弃坑、体积轻适合客户分发；本仓库的 `dxvpn.exe` 又恰好把提权 / 引擎 / PID 这些硬骨头都自包含了，子进程集成很干净。

## 范围

### 第一阶段（本方案）：仅 Windows

`dxvpn.exe` 的 `start/stop/引擎` 逻辑目前**只在 Windows 实现**（`shared/proxy/platform_nonwindows.go` 中 mac/Linux 全是「当前平台未实现」）。因此 GUI 复用 CLI ⇒ 第一阶段只能出 **Windows** 包。

### 后置：macOS

macOS 包的前置依赖是先在 `shared/proxy` 补出 macOS 引擎后端（系统 TUN / 路由 / 提权），这是另一块独立的大活，单列里程碑 M5，不阻塞 Windows 上线。

## 架构

```text
┌─────────────────────────────────────────────┐
│  Tauri 应用 (大象 VPN)                          │
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
                              │  dxvpn.exe        │
                              │ login/start/stop/ │
                              │ status/rotate-ip  │
                              └────────┬──────────┘
                                       │ 内嵌 sing-box 引擎
                                       ▼
                          WireGuard→Hub→出口 HTTP 代理→外网
```

要点：

- 所有「难」的部分（提权拉 `--fast` 引擎、后台引擎进程、PID 管理、运行指纹重启）都封在 `dxvpn.exe` 内部，Rust 侧只负责拼参数、跑子进程、解析输出。
- 系统代理的设置 / 还原放 **Rust 侧**（不污染 CLI），断电 / 崩溃的兜底还原也在 Rust 侧处理。

## CLI 机器接口改造（前置，M1）

GUI 解析中文文本不稳，先给 `dxvpn.exe` 加机器可读输出。**只加 `--json`，不改默认人读行为**，改动落在 `clients/cli/internal/app/app.go`：

1. `status --json`：
   ```json
   {"running":true,"proxy":"127.0.0.1:7890","proxy_reachable":true,
    "egress":"日本住宅","egress_ip":"126.x.x.x"}
   ```
   （未连接 / 无配置时为 `{"running":false,"proxy_reachable":false,"error":"…"}` 并退出非 0。
   当前不带 `fast` 字段——运行模式没有持久化存储，GUI 自己知道是以哪种模式拉起的。）
2. `login --json` / `rotate-ip --json`：
   ```json
   {"ok":true,"egress":"日本住宅","proxy":"127.0.0.1:7890"}
   {"ok":false,"error":"授权码无效或已过期"}
   ```
   失败时仍以非 0 退出码退出，GUI 双保险（退出码 + JSON）。
3. `start` / `stop`：维持现状（退出码 0/非 0 + stdout 文本即可）。`start` 内部已 `waitForTCP` 阻塞到代理可用或超时，GUI 期间显示「连接中」。

## 连接行为与系统代理（关键 UX）

`dxvpn.exe` 两种模式语义不同，GUI 必须替客户兜住「点一下就生效」：

| 模式 | 机制 | UAC | 流量覆盖 | 客户需手动设置吗 |
| --- | --- | --- | --- | --- |
| 默认（用户态） | 本地代理 `127.0.0.1:7890` | 否 | 仅指向该代理的 App | 要：得有人把系统/浏览器代理指过去 |
| `--fast`（系统 TUN） | wintun 全局路由 | 是（每次启动弹一次） | 全局 | 否 |

**采用方案**：

- **默认 = 用户态代理 + GUI 自动设 Windows 系统代理**：连接时由 Rust 写 WinINET（注册表 `Internet Settings` 的 `ProxyEnable`/`ProxyServer` + `InternetSetOption` 刷新），断开 / 退出 / 崩溃时还原。**免管理员、无 UAC，对浏览器和大多数 App 即开即用。**
- **高级开关「全局模式」= `--fast`**：系统 TUN 全局路由，启动弹一次 UAC，连不认代理的程序也走 VPN。

## 工程结构

```text
clients/desktop-gui/
├─ README.md                 # 删占位，写真实工程说明
├─ src-tauri/
│  ├─ tauri.conf.json        # externalBin 指向打包进来的 dxvpn.exe
│  ├─ Cargo.toml
│  ├─ binaries/              # 构建前把 dxvpn-<target-triple>.exe 拷进来
│  └─ src/
│     ├─ main.rs             # 应用入口 + 托盘
│     ├─ cli.rs              # sidecar 调用 + JSON 解析
│     └─ sysproxy.rs         # Windows 系统代理 开/关/还原
├─ src/                      # 前端（Svelte）
│  ├─ App.svelte             # 登录 / 连接开关 / 状态 / 出口IP / 换IP / 模式
│  └─ lib/api.ts             # invoke 封装
├─ package.json
└─ build.ps1                 # 一键：go build dxvpn.exe → 拷 sidecar → tauri build
```

## Rust 命令面（Tauri commands）

| 命令 | 内部行为 |
| --- | --- |
| `login(token)` | 跑 `dxvpn.exe login <token> --json`，解析 ok/egress |
| `connect(fast)` | `start [--fast]`；非 fast 时连接成功后由 `sysproxy.rs` 设系统代理 |
| `disconnect()` | 先还原系统代理，再 `dxvpn.exe stop` |
| `status()` | `status --json` → 回前端结构化状态 |
| `rotate_ip()` | `rotate-ip --json`，期间前端禁用按钮 + 进度 |
| `get_config()` | 读已保存配置（出口名、端口等）供 UI 展示 |

每个命令 = 拼参数 → 跑 sidecar → 解析 JSON / 退出码 → 回前端。

## 前端 UI

单窗口 + 托盘，界面极简：

- **未登录页**：授权码输入框 → 登录。
- **主页**：
  - 大开关「连接 / 断开」。
  - 状态机：`未连接 → 连接中 → 已连接`，失败进 `错误` 并显示原因。
  - 状态行：出口名、**出口 IP**、本地端口。
  - 「换 IP」按钮（仅手机卡出口可用），点击后禁用 + 进度。
  - 「高级」折叠：全局模式开关（=`--fast`）、本地端口。
- **托盘**：连接状态图标；右键菜单 连接 / 断开 / 打开主界面 / 退出；关窗最小化到托盘。
- 主界面打开时轮询 `status --json`（约 5s）刷新状态与出口 IP。

## Windows 打包（方案 A：不装服务）

| 方案 | 后台服务 | UAC 体验 | 复杂度 |
| --- | --- | --- | --- |
| **A. 仅打包 dxvpn.exe（采用）** | 否，作为 sidecar 与 app 同包 | 默认零 UAC；仅「全局模式」启动弹一次 | 低 |
| B. 装 Windows 服务 | 是，开机自启、提前提权 | 全程零 UAC | 高，要写服务 + 权限 + 自更新 |

采用 **A**：`dxvpn.exe --fast` 自己会弹 UAC 拉提权引擎，**不需要常驻服务**。B 仅当未来要「全局模式也永不弹 UAC」时才值得，后置。

打包细节：

- `tauri build` 出 NSIS / MSI 安装包，含桌面快捷方式。
- WebView2 走 `downloadBootstrapper`（Win11 自带，旧系统自动拉起安装）。
- `dxvpn.exe` 作为 `externalBin` sidecar 随包，构建前由 `build.ps1` 用 `go build -tags with_gvisor` 产出并按 target-triple 命名拷入 `src-tauri/binaries/`。
- 产品名「大象 VPN」、版本号、图标统一。代码签名后续再补。

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
| **M2** | Tauri 工程骨架 + sidecar 打包 + `connect/disconnect/status` 走通（先默认用户态，手动验证代理） | 1–2 天 |
| **M3** | Rust 系统代理开关 + 换 IP + 全局模式 + 托盘 + 状态轮询 + UI | 2–3 天 |
| **M4** | NSIS/MSI 安装包 + 桌面快捷方式 + 文档四件套 + worklog | 1 天 |
| **M5（后置）** | 先补 `shared/proxy` macOS 引擎，再开 macOS 包 | 单列 |

## 验收标准（Windows / 第一阶段）

- [ ] 输入授权码可登录，登录态持久化。
- [ ] 点「连接」后状态依次走 连接中 → 已连接，并显示出口名与出口 IP。
- [ ] 默认模式下连接后浏览器**无需手动配置**即可访问日本网站（GUI 已自动设系统代理）。
- [ ] 点「断开」后系统代理被还原，状态回到未连接。
- [ ] 应用崩溃 / 强退后重开，系统代理不残留（兜底还原生效）。
- [ ] 「换 IP」能触发并在出口恢复后刷新出口 IP。
- [ ] 「全局模式」启动弹一次 UAC，全局流量走 VPN。
- [ ] 关窗最小化到托盘，托盘可连接 / 断开 / 退出。
- [ ] NSIS/MSI 安装后有桌面快捷方式，卸载干净。

## 当前实现状态

- **M1 已完成**（2026-06-09）：`dxvpn.exe` 的 `status / login / rotate-ip` 支持 `--json`，默认人读输出不变。
  - 实现：`clients/cli/internal/app/app.go`、`clients/cli/main.go`（新增 `app.ErrSilent`：JSON 命令失败时退出非 0 但不再向 stderr 重复打印）。
  - `printJSON` 关闭 HTML 转义，`<授权码>` 等保持可读；JSON 命令失败输出 `{"ok":false,"error":…}`（status 为 `{"running":false,…,"error":…}`）并退出非 0。
  - 测试：`go test ./clients/cli/...` 通过；新增 `wantJSON`/`hasFlag`/`--json` 解析用例。
- 下一步：M2（Tauri 工程骨架 + sidecar 打包 + `connect/disconnect/status` 走通）。
