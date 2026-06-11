# 2026-06-09 工作记录：桌面 GUI M2（Tauri 骨架 + sidecar 走通）

## 背景

接 [M1](2026-06-09-desktop-gui-plan-m1.md)（CLI `--json`）后实现 M2：Tauri v2 工程骨架 + 把现成 `zhvpn.exe` 作为 sidecar 调通 `login/connect/disconnect/status`。方案见 [desktop-gui.md](../../30-implementation/desktop-gui.md)。

## 工具链（本机新装）

机器原本无 Rust、无 C/C++ 链接器。经确认后 winget 安装：

- `Rustlang.Rustup` → Rust 1.96.0（stable-x86_64-pc-windows-msvc，按用户安装在 `~\.cargo`）。
- `Microsoft.VisualStudio.2022.BuildTools` + `Microsoft.VisualStudio.Workload.VCTools`（C++ 链接器，UAC 已批准）。

注意：新装后已有的 PowerShell 进程 PATH 不刷新，命令里需 `$env:Path = "$env:USERPROFILE\.cargo\bin;$env:Path"`。

## 工程

- `create-tauri-app`（`-t svelte-ts --tauri-version 2`）在临时目录生成后并入 `clients/desktop-gui/`，保留角色目录。栈：Tauri v2 + SvelteKit（adapter-static, ssr=false）+ Svelte 5 + Vite 6 + TS。
- 前端 `src/routes/+page.svelte` + `src/lib/api.ts`：登录页 / 连接开关 / 状态机（未连接·连接中·已连接·错误）/ 出口名·出口 IP·本地端口 / 全局模式开关。
- 后端 `src-tauri/src/lib.rs`：加 `tauri-plugin-shell`，四个 `#[tauri::command]`：
  - `status` → `zhvpn status --json`（透传 JSON）
  - `login(token)` → `zhvpn login <token> --json`
  - `connect(fast)` → `zhvpn start [--fast]`（`{ok,message}`）
  - `disconnect` → `zhvpn stop`
- `tauri.conf.json`：`externalBin: ["binaries/zhvpn"]`、窗口标题「纵横 VPN」420x600、productName `Zongheng VPN`（ASCII，避免二进制名 unicode 问题；客户显示名 M4 再本地化）。
- sidecar 不入库：`src-tauri/.gitignore` 加 `/binaries/`；`build.ps1` 一键 `go build -tags with_gvisor` 产出 `zhvpn-<rustc host triple>.exe` → `tauri build`。

## 验证

- `npm install` / `npm run build`（vite → `build/`）通过。
- `cargo build`（debug）通过，3m28s；`npm run tauri build`（实际跑成 release，`--debug` 被 npm 吞了）通过，3m34s，自动下载 WiX/NSIS，产出：
  - `src-tauri/target/release/bundle/msi/Zongheng VPN_0.1.0_x64_en-US.msi`（10.3MB）
  - `src-tauri/target/release/bundle/nsis/Zongheng VPN_0.1.0_x64-setup.exe`（7.4MB）
- sidecar 打包确认：Tauri 把 `zhvpn-x86_64-pc-windows-msvc.exe` 去掉 triple 后缀复制为 `target/release/zhvpn.exe`（17.2MB），随安装包。
- runtime 走通：运行 `target/release/zhvpn-desktop.exe`，窗口标题「纵横 VPN」正常；采样子进程抓到 GUI 实际调用
  `"...\target\release\zhvpn.exe" status --json`，证明 sidecar 解析与调用链通。

## 备注 / 遗留

- 本机有一个早前 CLI 测试遗留的 `dist\windows-amd64\zhvpn.exe __engine` 后台代理进程（非本次产生），未动它。
- M3 再做：系统代理自动设/还原、换 IP、全局模式、托盘、登录→连接→真实代理的人工点验。
