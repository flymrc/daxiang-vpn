# desktop-gui — 纵横 VPN 桌面客户端

面向终端用户的跨平台 GUI 客户端（**Tauri v2 + SvelteKit**）。

当前阶段：**Windows**（macOS 后置，详见实现方案）。

## 它是什么

一层薄外壳：界面用 Web（Svelte），后端 Rust 通过 **sidecar 子进程**调用现成的 [`zhvpn.exe`](../cli/)（内嵌 sing-box），不重写任何核心逻辑。所有提权 / 引擎 / PID 管理都在 `zhvpn.exe` 内部。

当前 Windows GUI 默认只启动本地代理 `127.0.0.1:7890`，需要用户在目标浏览器或软件里手动配置。勾选「全局代理」后，连接成功时 Rust 后端会把 Windows 系统代理指向本地代理地址，断开、登出、托盘退出时还原。勾选「高性能模式」后，GUI 会调用 `zhvpn start --fast`，这条路径可能触发 UAC；它和「全局代理」互相独立，可同时开启。

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

仓库根目录也提供一个更适合日常使用的包装脚本，会自动打印安装包路径和 SHA256：

```powershell
.\scripts\build-desktop-gui.ps1 -Target x64
.\scripts\build-desktop-gui.ps1 -Target arm64
.\scripts\build-desktop-gui.ps1 -Target both -OpenFolder
```

其中 `x86`/`x64`/`amd64` 都会构建 Windows Intel/AMD 64 位包；当前不支持真正 32 位 Windows GUI 包。

在 macOS/Linux 上交叉构建 Windows 包时需先装 Rust target、`cargo-xwin`、LLVM/NSIS，脚本会在非 Windows host 默认给 Tauri 加 `--runner cargo-xwin`。amd64 sidecar 构建会强制 `GOAMD64=v1`，保证老 Intel i5 / Win10 兼容。
WebView2 用 downloadBootstrapper（Win11 自带，旧系统自动拉起安装）。

## Rust 命令 ↔ CLI 映射

| 命令 | 调用 | 返回 |
| --- | --- | --- |
| `login(token)` | `zhvpn login <token> --json` | `{ok, egress, proxy, error}` |
| `connect(globalProxy, fast)` | `zhvpn start [--fast]`；`globalProxy=true` 时连接成功后设置 Windows 系统代理 | `{ok, message}` |
| `disconnect()` | `zhvpn stop` | `{ok, message}` |
| `status()` | `zhvpn status --json --no-ip-check` | `{running, proxy, proxy_reachable, egress, error}` |
| `statusIp()` | `zhvpn status --json` | `{running, proxy, proxy_reachable, egress, egress_ip, egress_ipv4, egress_ipv6, error}` |
| `appVersion()` | Rust 包版本 | `0.4.x` |

`status()` 在 Rust 后端有全局异步锁，前端也会跳过仍在进行中的刷新；主窗口和托盘同时轮询时不会叠出多个长期停留的 `zhvpn.exe status --json` 子进程。CLI `status` 使用登录/start 写入的本地状态缓存，不会把 Hub bootstrap 当成心跳；`start` 仍会强刷新授权配置，缓存不持久化 WireGuard 私钥。公网出口 IP 由 `statusIp()` 按需/低频刷新，GUI 会同时展示 IPv6 与 IPv4，并保留上一轮有效值，避免未换 IP 时界面反复跳「获取中」。

GUI 主程序使用 Windows 命名 Mutex 保持单例；`zhvpn.exe start/stop/login/import/logout` 也在同一 `ZHVPN_HOME` 下使用操作级命名 Mutex，避免并发操作启动多套同一本地实例。
