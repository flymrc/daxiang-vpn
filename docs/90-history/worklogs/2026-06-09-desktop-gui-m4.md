# 2026-06-09 工作记录：桌面 GUI M4（打包 + 品牌图标 + 文档）

接 [M3](2026-06-09-desktop-gui-m3.md)。方案见 [desktop-gui.md](../../30-implementation/desktop-gui.md)。

## 做了什么

- **品牌图标**：用 `System.Drawing` 自绘 1024² 源图（蓝色渐变圆角方块 + 居中白「象」），`npm run tauri icon` 生成全套，替换脚手架默认 Tauri/Svelte logo；删掉桌面端用不到的 `icons/android`、`icons/ios`。
- **打包配置**（`tauri.conf.json`）：
  - `productName` 改为「大象 VPN」（显示名），新增 `mainBinaryName: dxvpn-desktop`（ASCII 二进制名，避免 unicode 坑）。
  - `bundle.targets: ["nsis"]`、`windows.nsis.installMode: currentUser`（按用户安装，免管理员）、`windows.webviewInstallMode: downloadBootstrapper`。
  - `publisher`/`copyright`/`category: Utility`/`shortDescription`/`longDescription`。
- **架构文档**：`docs/10-architecture/system-architecture.md` 角色表新增「桌面 GUI」一行。
- README 打包说明同步为 NSIS（按用户安装 + WebView2 bootstrapper）。

## 踩坑

1. `category: "Network"` → `failed to build bundler settings: invalid category`。Tauri 的 `category` 只接受 Apple App Store 枚举，无 Network；改 `Utility`。
2. unicode `productName`「大象 VPN」让 WiX `light.exe`（MSI）失败。NSIS 处理 UTF-8 正常。→ 去掉 MSI，只出 NSIS（消费者首选安装器，MSI 偏企业 GPO）。

## 验证

- `tauri build` 通过，产出 `src-tauri/target/release/bundle/nsis/大象 VPN_0.1.0_x64-setup.exe`（7.5MB）。
- sidecar 确认随包：`target/release/dxvpn.exe`（17.2MB，由 `dxvpn-x86_64-pc-windows-msvc.exe` 去 triple 后缀复制而来）。
- 主程序 `dxvpn-desktop.exe` 11.2MB，嵌入品牌图标。

## 点验中发现并修复（M4 后）

真机点验时发现并修掉两个 runtime bug + 加了富托盘：

1. **`failed to parse string`**：`sysproxy` crate 读取当前系统代理时，遇到空值 / `:0` 这类会解析失败。改：`enable` 读不出当前代理就备份一个「关闭」状态继续设，不中断。
2. **app 窗口里出现浏览器错误页**：连接后系统代理被设上，WebView2 把自己界面（`http://tauri.localhost`）的请求也丢去走代理 → 加载失败显示错误页；WinINET 的 `*.localhost` bypass 对 WebView2 不可靠。改：启动时设 `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--no-proxy-server`，WebView 永不走代理（界面只加载本地资源、功能走 Rust 命令，无需代理）。界面在任何网络/代理状态下都稳定。
3. **富托盘菜单（仿 Tailscale）**：状态行（实时「● 已连接 · IP / ○ 未连接」，5s 刷新）+ 连接/断开/换 IP + 打开主界面/退出，可直接在托盘操作。`connect/disconnect/rotate/status` 抽成共享 impl，命令与托盘复用。

附：构建产物只应有单一 `大象 VPN_…setup.exe`（含内置 CLI sidecar，NSIS LZMA 压缩约 7.5MB）；调试期改名/改格式残留的旧包（`Daxiang VPN…msi/exe`）已清理。沿用 `cargo build`（非 `tauri build`）跑出的二进制会指向 dev server，只能 `npm run tauri dev` 或装/跑 `tauri build` 产物。

## 状态

桌面 GUI **M1–M4 代码与打包完成**。剩：

- 真实出口下的端到端人工点验（登录→连接→断开→换 IP→托盘）。
- 后置：代码签名（避免 SmartScreen 警告）、macOS（M5，前置补 `shared/proxy` mac 引擎）。
