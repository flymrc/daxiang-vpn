# desktop-gui — 大象 VPN 桌面客户端

面向终端用户的跨平台 GUI 客户端（**Tauri v2 + SvelteKit**）。

当前阶段：**Windows**（macOS 后置，详见实现方案）。

## 它是什么

一层薄外壳：界面用 Web（Svelte），后端 Rust 通过 **sidecar 子进程**调用现成的 [`dxvpn.exe`](../cli/)（内嵌 sing-box），不重写任何核心逻辑。所有提权 / 引擎 / PID 管理都在 `dxvpn.exe` 内部。

完整设计见 [docs/30-implementation/desktop-gui.md](../../docs/30-implementation/desktop-gui.md)。

## 结构

```text
src/                     SvelteKit 前端（src/routes/+page.svelte、src/lib/api.ts）
src-tauri/
  src/lib.rs             Rust 命令：login / connect / disconnect / status（调 sidecar + 解析 --json）
  tauri.conf.json        externalBin 指向 binaries/dxvpn
  binaries/              dxvpn-<target-triple>.exe（由 build.ps1 产出，不入库）
build.ps1                一键：go build sidecar -> tauri build
```

## 开发

前置：Node、Rust（MSVC toolchain）、Go、VS Build Tools（C++ 工作负载）。

```powershell
# 1. 先产出 sidecar（dev 也需要，按 rustc host triple 命名）
$triple = (rustc -Vv | Select-String '^host:\s*(.+)$').Matches.Groups[1].Value.Trim()
go build -tags with_gvisor -trimpath -ldflags "-s -w" `
  -o "src-tauri/binaries/dxvpn-$triple.exe" ../cli

# 2. 跑起来
npm install
npm run tauri dev
```

## 打包

```powershell
./build.ps1
```

产出 NSIS/MSI 安装包于 `src-tauri/target/release/bundle/`。

## Rust 命令 ↔ CLI 映射

| 命令 | 调用 | 返回 |
| --- | --- | --- |
| `login(token)` | `dxvpn login <token> --json` | `{ok, egress, proxy, error}` |
| `connect(fast)` | `dxvpn start [--fast]` | `{ok, message}` |
| `disconnect()` | `dxvpn stop` | `{ok, message}` |
| `status()` | `dxvpn status --json` | `{running, proxy, proxy_reachable, egress, egress_ip, error}` |
