# 2026-06-11 Windows GUI 全局代理止血包

## 背景

Windows GUI 旧界面把 `--fast` 标成“全局模式 / 系统 TUN”，但 2026-06-10 调查已确认当前 sing-box/WireGuard 配置没有完整默认路由接管。用户需要先拿到一个可试的“全局代理”GUI。

## 改动

- `clients/desktop-gui/src-tauri/src/lib.rs`
  - `connect_impl` 在 `zhvpn start` 成功后统一读取 `status --json` 的本地代理地址，并调用 `sysproxy::enable` 写 Windows 系统代理。
  - 即使未来调用方传 `fast=true`，也仍设置系统代理，避免旧 `--fast` 路径再次表现为“连接了但浏览器不走代理”。
- `clients/desktop-gui/src/routes/+page.svelte`
  - 移除旧“全局模式（系统 TUN）”复选框。
  - 主连接按钮固定调用 `connect(false)`，走免 UAC 的本地代理 + Windows 系统代理路径。
- `clients/desktop-gui` 版本号提升到 `0.3.2`。
- 文档同步：
  - `clients/desktop-gui/README.md`
  - `docs/30-implementation/desktop-gui.md`

## 构建与验证

- `npm run check`：通过；保留既有 `Cannot find type definition file for 'node'` 警告。
- `cargo fmt --manifest-path clients\desktop-gui\src-tauri\Cargo.toml --check`：通过。
- `.\clients\desktop-gui\build.ps1 -Target amd64`：通过。
- 产物：
  - `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.3.2_x64-setup.exe`
  - 大小约 7.9 MB。

## 注意

这个包实现的是 Windows 系统代理意义上的全局代理：浏览器和遵循系统代理的 App 会走本地代理。它还不是完整 TUN 全局接管；真正 TUN 需要后续补 `tun inbound`、`auto_route`、DNS/IPv6/断开恢复等验证。
