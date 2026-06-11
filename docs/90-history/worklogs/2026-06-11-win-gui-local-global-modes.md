# 2026-06-11 Windows GUI 局部/全局代理模式修正

## 背景

0.3.2 止血包把连接按钮默认做成“启动本地代理 + 写 Windows 系统代理”。用户反馈这不符合产品预期：

- 默认应是局部代理：只启动 `127.0.0.1:7890`，需要用户自行配置浏览器或目标软件。
- 勾选“全局代理”后，才自动设置 Windows 系统代理。

## 改动

- `clients/desktop-gui/src/routes/+page.svelte`
  - 恢复「全局代理」复选框，默认关闭。
  - 连接时把复选框状态传给后端。
- `clients/desktop-gui/src/lib/api.ts`
  - 前端 API 语义改为 `connect(globalProxy)`；Tauri 参数名仍沿用 `fast` 做兼容。
- `clients/desktop-gui/src-tauri/src/lib.rs`
  - GUI 不再从该路径调用 `zhvpn start --fast`。
  - `global_proxy=false`：只执行 `zhvpn start`。
  - `global_proxy=true`：执行 `zhvpn start` 后读取本地代理地址并设置 Windows 系统代理。
- 登录成功后把上次令牌保存到 WebView localStorage；之后进入登录页会自动填回。
- 图标换为蓝绿底白色 `ZH` 字标，并同步前端 favicon。
- 版本号提升到 `0.4.0`。

## 语义

- 局部代理：本地代理可用，但系统代理保持用户原状。
- 全局代理：自动写 Windows 系统代理；断开、登出、托盘退出时沿用既有还原逻辑。
- 这仍不是完整 TUN 全局接管；`--fast` 保留在 CLI 命令层，后续另做完整 TUN。

## 构建与验证

- `cargo fmt --manifest-path clients\desktop-gui\src-tauri\Cargo.toml --check`：通过。
- `npm run check`：通过；保留既有 `Cannot find type definition file for 'node'` 警告。
- `.\clients\desktop-gui\build.ps1 -Target amd64`：通过，无 Rust warning。
- 随包 `zhvpn.exe status --json` 可执行；当前本机未登录，返回“未找到配置”属预期。
- 产物：
  - `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.0_x64-setup.exe`
