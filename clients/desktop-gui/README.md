# desktop-gui — 纵横 VPN 桌面客户端

面向终端用户的跨平台 GUI 客户端（**Tauri v2 + SvelteKit**）。

当前阶段：**Windows**（macOS 后置，详见实现方案）。

## 它是什么

一层薄外壳：界面用 Web（Svelte），后端 Rust 通过 **sidecar 子进程**调用现成的 [`zhvpn.exe`](../cli/)（内嵌 sing-box），不重写任何核心逻辑。所有提权 / 引擎 / PID 管理都在 `zhvpn.exe` 内部。

当前 Windows GUI 的连接按钮走“本地代理 + Windows 系统代理”路径：`zhvpn start` 成功后，Rust 后端自动把系统代理指向 `status --json` 返回的本地代理地址，断开、登出、托盘退出时还原。旧的 `--fast` 参数仍保留在命令层，但界面不再把它标成“全局模式”，避免把尚未完整实现的 TUN 路由误认为全局接管。

完整设计见 [docs/30-implementation/desktop-gui.md](../../docs/30-implementation/desktop-gui.md)。

## 结构

```text
src/                     SvelteKit 前端（src/routes/+page.svelte、src/lib/api.ts）
src-tauri/
  src/lib.rs             Rust 命令：login / connect / disconnect / status（调 sidecar + 解析 --json）
  tauri.conf.json        externalBin 指向 binaries/zhvpn
  binaries/              zhvpn-<target-triple>.exe（由 build.ps1 产出，不入库）
build.ps1                一键：go build sidecar -> tauri build
```

## 开发

前置：Node、Rust（MSVC toolchain）、Go、VS Build Tools（C++ 工作负载）。

```powershell
# 1. 先产出 sidecar（dev 也需要，按 rustc host triple 命名）
$triple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
go build -tags with_gvisor -trimpath -ldflags "-s -w" `
  -o "src-tauri/binaries/zhvpn-$triple.exe" ../cli

# 2. 跑起来
npm install
npm run tauri dev
```

## 打包

```powershell
./build.ps1 -Target amd64
```

产出 Windows x64 / amd64 NSIS 安装包（按用户安装，免管理员）于 `src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/`，sidecar `zhvpn.exe` 随包。若只想构建当前开发机架构，可用 `./build.ps1 -Target host`。

在 macOS/Linux 上交叉构建 Windows 包时需先装 Rust target、`cargo-xwin`、LLVM/NSIS，脚本会在非 Windows host 默认给 Tauri 加 `--runner cargo-xwin`。amd64 sidecar 构建会强制 `GOAMD64=v1`，保证老 Intel i5 / Win10 兼容。
WebView2 用 downloadBootstrapper（Win11 自带，旧系统自动拉起安装）。

## Rust 命令 ↔ CLI 映射

| 命令 | 调用 | 返回 |
| --- | --- | --- |
| `login(token)` | `zhvpn login <token> --json` | `{ok, egress, proxy, error}` |
| `connect(fast)` | `zhvpn start [--fast]` + 连接成功后设置 Windows 系统代理 | `{ok, message}` |
| `disconnect()` | `zhvpn stop` | `{ok, message}` |
| `status()` | `zhvpn status --json` | `{running, proxy, proxy_reachable, egress, egress_ip, error}` |

`status()` 在 Rust 后端有全局异步锁，前端也会跳过仍在进行中的刷新；主窗口和托盘同时轮询时不会叠出多个长期停留的 `zhvpn.exe status --json` 子进程。
