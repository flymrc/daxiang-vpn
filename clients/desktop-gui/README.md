# desktop-gui（占位）

mac / windows PC 的**单一跨平台 GUI 客户端**(预留,尚未实现)。

- 目标:一套跨平台 GUI 代码库(Tauri / Flutter / Electron 等待定)同时出 macOS 和 Windows 包。
- 与现有 [clients/cli](../cli/) 并列,面向不想用命令行的终端用户。
- 复用方式:通过调用 CLI / 直接复用 [shared/](../../shared/) 里的 Go 包(`config`、`proxy`、`paths`),或经 Hub API 交互——具体选型实现时再定。

> 这是按 [AGENTS.md](../../AGENTS.md) 约定预留的角色坑位。开始实现时删掉本说明、补真实工程。
