# 2026-10-07 Desktop/NSIS 独立复核

范围：Steelman 隔离 worktree 的 CLI runtime lease 接线、GUI/SDK、NSIS 与发行门禁；未提交、推送、部署，未操作真实用户代理或旧卸载器。

## 已复现并修复

- GUI 在缺配置 CLI 非零退出时丢弃 typed status，导致首次安装/登出无法进入登录页。保留完整且明确未就绪的 missing-config DTO；永久测试同时证明它不能成为连接/lease 权限，已有恢复错误仍被保留。
- 原 NSIS 的短暂 exclusive probe 无法覆盖整段替换。实际 owned synthetic NSIS 复现 GUI 已变、CLI 仍旧、安装器 exit 0。改成拒绝现有注册和目标目录，完整 payload 先进私有 stage，核验 GUI/CLI SHA 后原子发布。
- 官方 Tauri 2.11.2 模板在 PREINSTALL 之前会运行旧卸载器；仅改 hook 不能约束旧 EXE。自定义模板移除该维护路径，在 `.onInit` 拒绝已有安装；自动卸载也明确拒绝。
- GUI 登录授权码移到有界 stdin；删除旧 localStorage 明文缓存写入，成功/失败输出递归红化，不显示无效 stdout/stderr 原文。
- Chrome mock IPC 进一步复现 ready 后 status 拒绝仍保留旧“已连接/换 IP”的 P1。状态失败现在清快照/IP、显示待确认并禁用连接；迟到 IP 不复活旧 ready；独立保存 status、action、代理恢复诊断，新状态成功只清临时 status 错误。
- inner builder 改用统一 Steelman 行为+安全门禁，模板与 CLI 版本漂移拒绝，源 manifest 捕获与最终复核覆盖 dirty development。

## 当前验证

- `cargo test --locked --manifest-path clients/desktop-gui/src-tauri/Cargo.toml --lib`（TAURI_CONFIG externalBin=[]）：41 passed。含新 missing-config、stdin owned subprocess、红化用例。
- `cargo clippy --locked --manifest-path clients/desktop-gui/src-tauri/Cargo.toml --lib -- -D warnings`：通过。
- `npm --prefix clients/desktop-gui run check`：0 errors / 0 warnings。
- `pwsh -NoProfile -File clients/desktop-gui/scripts/check-upgrade-guard.ps1 -EvidenceDirectory C:/Users/xuotq/.codex/zhvpn-private-fresh-final`：13 个 Windows 行为场景 + 1 个实际 Tauri 完整模板渲染/NSIS 编译场景通过。完整模板产物为 synthetic child，不执行。
- `pwsh -NoProfile -File clients/desktop-gui/scripts/check-build-failure.ps1`：使用复制的真实 inner builder/统一 gate 和受控扫描生产者，安全扫描拒绝后无 sidecar、无成功 manifest；已接入日常 gate。
- Playwright CLI 驱动新的 owned Chrome session、实际当前 Svelte 与 localhost Vite、mock IPC：缺配置进入登录页；播种旧 `zhvpn.lastToken` 后页面初始化删除，新登录 1 次且授权缓存写入 0 次；ready→IPC拒绝→待确认/连接禁用→完整ready恢复；迟到旧 IP 不恢复就绪；action/代理恢复错误保留，仅临时 status 错误清除；没有自动 connect/disconnect/rotate。Console 0 errors / 0 warnings。
- 浏览器证据在私有 `cli-gui-independent-review/output/playwright/`：`final-missing-config-login.png`、`final-ready-failure-diagnostics.png`、`final-ready-recovered-errors-retained.png`，以及 `final-ready-failure-evidence.txt` / `final-ready-recovery-evidence.txt`。复现旧行为截图为 `repro-stale-ready-after-ipc-error.png`。此证据不是实际 Tauri/WebView/Hub 会话。
- 本机原 workspace 父 namespace 的其他 SID Modify 权限被只读识别并拒绝；仅对新建测试目录设置 ACL，未放宽已有 profile 权限。
- 独立复核 CLI 私有三路径：读旧 inherited/unsafe 文件拒绝；写前保护新文件、单链接/no-reparse 检查与 canonical home operation lock 保持。SDK stdin 的成功/失败输出红化与大小写私有字段复核由 root 补充永久测试。

## 未验事项

WinINET 和注册表 Rust 测试使用合成 key；CLI subprocess 数据面使用隔离 fixture。全新 NSIS 宏测试和完整模板编译不能替代真实签名发行、UAC/原 SID、现有版本升级、卸载、macOS 或生产验收。未重跑 Jetstar，按用户取消保持取消。

可执行范围与运维限制见 [Desktop 登录与全新安装边界](../../30-implementation/desktop-fresh-install-boundary.md)。整体 Steelman 的发布/平台/生产验收条件仍需分别完成，不因本地门禁通过而打勾。
