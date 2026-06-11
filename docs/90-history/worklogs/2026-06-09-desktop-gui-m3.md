# 2026-06-09 工作记录：桌面 GUI M3（系统代理 + 托盘 + 换 IP）

接 [M2](2026-06-09-desktop-gui-m2.md)。方案见 [desktop-gui.md](../../30-implementation/desktop-gui.md)。

## 做了什么

- **系统代理自动化**（`src-tauri/src/sysproxy.rs`，用 `sysproxy` crate）：
  - `connect` 在默认用户态模式下，连接成功后把 Windows 系统代理（WinINET）指向本地代理，浏览器等无需手动设置；`--fast`（系统 TUN 全局路由）不设系统代理。
  - `disconnect` 还原。首次打开把原系统代理状态写 `proxy-backup.json`（在 app config dir），还原后删除。
  - **崩溃恢复**：启动时若备份文件存在且 `status` 显示代理实际未运行，则自动还原，避免系统代理悬挂指向已死的本地端口导致断网。
- **托盘 + 生命周期**（`tauri` 加 `tray-icon` feature，`lib.rs`）：
  - 托盘左键开主界面，右键菜单 打开/退出。
  - 关窗 = `prevent_close` + `hide`（收进托盘，保持连接）；退出只走托盘「退出」，退出前 `sysproxy::restore`。
- **换 IP**：`rotate_ip` 命令（`zhvpn rotate-ip --json`）+ 主界面「换 IP」按钮（连接态可见），结果显示 `before → after`。
- **全局模式**：前端复选框 → `connect(fast=true)`。
- 前端 `+page.svelte`/`api.ts`：加 rotate 按钮、info/warning 提示。

## 验证

- `cargo build` 通过（1m00s，新增 `sysproxy 0.3.0`、`tray-icon`、`winreg`/`winapi`）。
- `npm run build` 通过。
- 运行 debug 程序：窗口「纵横 VPN」正常；进程存活说明 `.setup()`（含 `TrayIconBuilder::build`）未 panic，托盘创建成功。

## 待人工点验（runtime，需真实登录 + 出口）

GUI 无法在此环境点击验证，留给人工：

1. 登录授权码。
2. 连接（用户态）→ 浏览器**免手动设置**应能直连日本；状态显示出口 IP。
3. 断开 → 系统代理被还原（设置里代理关闭/恢复原状）。
4. 关窗 → 收进托盘，连接保持；托盘「退出」→ 系统代理还原。
5. 「换 IP」→ 触发并显示新旧 IP。
6. 全局模式勾选 → 连接弹一次 UAC，全局走 VPN。

## 备注

- 机器上早前遗留的 `dist\windows-amd64\zhvpn.exe __engine` 旧代理仍在（非本次产生）。
- dev 运行需把 sidecar 放到 `src-tauri/target/debug/zhvpn.exe`（`tauri dev` 会自动放；裸 `cargo build` 不会，已手动拷一份用于本次冒烟）。
