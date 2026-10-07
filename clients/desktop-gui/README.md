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
./build.ps1 -Target amd64 -Development
```

产出 Windows x64 / amd64 NSIS 安装包（按用户安装，免管理员）于 `src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/`，sidecar `zhvpn.exe` 随包。若只想构建当前开发机架构，可用 `./build.ps1 -Target host -Development`。

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
| `login(token)` | `zhvpn login --token-stdin --json` | `{ok, egress, proxy, error}` |
| `connect(globalProxy, fast)` | `zhvpn start [--fast]`；`globalProxy=true` 时连接成功后调用 CLI system-proxy acquire | `{ok, message}` |
| `disconnect()` | `zhvpn stop` | `{ok, message}` |
| `status()` | `zhvpn status --json --no-ip-check` | `{running, proxy, proxy_reachable, egress, error}` |
| `statusIp()` | `zhvpn status --json` | `{running, proxy, proxy_reachable, egress, egress_ip, egress_ipv4, egress_ipv6, error}` |
| `appVersion()` | Rust 包版本 | `0.4.x` |

`status()` 在 Rust 后端有全局异步锁，前端也会跳过仍在进行中的刷新；主窗口和托盘同时轮询时不会叠出多个长期停留的 `zhvpn.exe status --json` 子进程。CLI `status` 使用登录/start 写入的本地状态缓存，不会把 Hub bootstrap 当成心跳；`start` 仍会强刷新授权配置，缓存不持久化 WireGuard 私钥。公网出口 IP 由 `statusIp()` 按需/低频刷新，GUI 会同时展示 IPv6 与 IPv4，并保留上一轮有效值，避免未换 IP 时界面反复跳「获取中」。

GUI 主程序使用 Windows 命名 Mutex 保持单例；`zhvpn.exe start/stop/login/import/logout` 也在同一 `ZHVPN_HOME` 下使用跨进程、跨会话的文件操作锁，避免并发操作启动多套同一本地实例。

## 2026-10-07 开发与发行限制

默认 release 需要干净源码、受控 Windows 签名证书/SignTool、统一合同/行为/安全门禁，侧载 CLI 和安装器的 Authenticode 均须有效；缺条件即停止。`-Development` 显式生成 unsigned 开发包，记录完整源提交、dirty/clean、源文件 hash、工具链和产物 SHA，不能作为正式发布。包装脚本同样透传开发与签名参数，只从本次 manifest 选择产物，不取目录里“最新”的旧包。设置全新绝对 `CARGO_TARGET_DIR` 或明确归档旧输出，构建不会自动清空目录。

安装器当前仅允许全新目标目录，早于旧卸载器执行检查注册与目标；完整 GUI/CLI 在受保护 staging 中核验后一次无覆盖目录发布。已有安装的升级和自动卸载均明确拒绝，暂不具备完整升级协议；用户配置/恢复 WAL 保留，不能通过按进程名称杀引擎绕过拒绝。具体源码、合成并发检查和平台限制见 [运行时集成](../../docs/30-implementation/steelman-runtime-integration.md)。

登录授权码仅进入 sidecar stdin，不保存浏览器 localStorage；秘密输出脱敏。无配置的 typed degraded 状态可进入登录页，不能授予系统代理 ready。GUI 新租约只调用 CLI，退出释放本 GUI 实际新建的 lease，保留共享引擎；旧 v1 journal 只保留受控恢复。正常关窗仍进入托盘；恢复失败须保留 GUI、引擎和恢复记录。macOS 系统代理和真实 WinINET/双登录会话尚待实机验收。
