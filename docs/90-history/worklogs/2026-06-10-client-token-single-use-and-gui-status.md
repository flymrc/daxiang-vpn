# 2026-06-10 工作记录：客户端重复启动与 token 单来源保护

## 背景

GUI 连接后经常出现卡顿和断线感，排查时提出两个风险：

- 本机是否同时启动了多个 `zhvpn` 客户端或状态查询进程。
- 同一个客户端 token 是否可能在不同地方登录，导致同一个 WireGuard peer 被不同 endpoint 来回抢占。

## 排查结论

- 本机长期运行形态是一个 `zhvpn-desktop.exe` 加一个 `zhvpn.exe __engine`，监听 `127.0.0.1:7890`。
- 曾看到多个短暂或滞留的 `zhvpn.exe status --json` 子进程，来源是 GUI 主窗口和托盘状态轮询可能重叠。
- Hub 上 `10.66.0.30` 的 endpoint 公网 IP 等于本机直连公网 IP，因此这次没有证据证明 token 正在异地使用。
- Hub token 配置中未发现重复 WireGuard 地址。

## 修复

- 桌面 GUI：
  - 前端 `refresh()` 增加进行中保护，跳过重叠刷新。
  - Rust 后端 `status_impl` 增加全局 async mutex，串行化所有 `zhvpn status --json` sidecar 调用。
- Hub 授权 API：
  - `/api/client/bootstrap` 增加 token 来源租约，默认 `ZHHUB_TOKEN_LEASE_SECONDS=30`。
  - 同 token 同来源会刷新租约；同 token 不同公网来源在租约内返回 `409 {"error":"token_in_use"}`。
  - 来源识别只信任本机或内网反代传入的 `X-Forwarded-For`，公网直连客户端无法伪造 XFF 绕过租约。
- CLI：
  - bootstrap 收到 409 时提示“授权码正在其他网络使用，请先断开另一台设备或等待约 30 秒后重试”。

## 验证

- `go test ./...` 通过。
- `npm run build` 通过。
- `cargo fmt --manifest-path .\clients\desktop-gui\src-tauri\Cargo.toml` 通过。
- `cargo check --manifest-path .\clients\desktop-gui\src-tauri\Cargo.toml` 通过。
- Hub 新版 `zhhub` 已部署并重启，`zhhub.service` active。
- 线上 token 租约验证通过：
  - 本机先正常 bootstrap 后，从 Hub 本机以可信 `X-Forwarded-For: 203.0.113.200` 模拟异地来源，返回 `409 {"error":"token_in_use"}`。
  - 从公网直连 Hub 时伪造同一个 XFF，Hub 忽略该头并按真实 TCP 来源处理，返回 `200`。
- GUI 安装包已用临时 `CARGO_TARGET_DIR` 打出，避免覆盖当前正在运行的 release 程序：`clients/desktop-gui/src-tauri/target-build-tmp/release/bundle/nsis/纵横 VPN_0.1.0_x64-setup.exe`。
- 当前本机 `zhvpn status --json` 可返回 Android 手机卡出口 IP。

## 后续

- 需要用户安装或重启新版 GUI 后，GUI 状态轮询串行化才会在桌面端生效。
- token 租约是当前静态 token 模型下的保守保护；长期产品化仍建议升级到设备绑定、公私钥本地生成、token 换短期会话。

## 同日追加：GUI 0.3.0 amd64 客户包

用户要求给客户发 Windows amd64 GUI 包，并把版本升到 `0.3.0`。第一次按旧脚本打出的安装包文件名虽然是 `x64`，但开发机 Rust host 是 `aarch64-pc-windows-msvc`，Tauri 主程序实际编成了 ARM64，导致普通 x64 客户机打不开。

已修复：

- 版本号同步到 `0.3.0`：`package.json`、`package-lock.json`、`src-tauri/Cargo.toml`、`src-tauri/Cargo.lock`、`tauri.conf.json`。
- `clients/desktop-gui/build.ps1` 增加 `-Target amd64|arm64|host` 参数，默认 `amd64`，并显式执行 `tauri build --target x86_64-pc-windows-msvc`。
- `clients/desktop-gui/README.md` 同步打包命令为 `./build.ps1 -Target amd64`。
- 已重新生成客户包：`dist/windows-amd64/纵横 VPN_0.3.0_x64-setup.exe`。
- PE header 校验通过：
  - `zhvpn-desktop.exe` machine `0x8664`，`x64/amd64`。
  - `zhvpn.exe` machine `0x8664`，`x64/amd64`。
