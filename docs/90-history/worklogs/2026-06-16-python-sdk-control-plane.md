# 2026-06-16 Python SDK control plane decision

## 背景

讨论 GUI 客户端被 Python 程序调用时会很别扭。现有 GUI 是 Tauri 外壳，核心能力已经由随包 `zhvpn.exe` sidecar 提供；CLI 也已有 `login/status/rotate-ip/logout --json` 等机器接口。

## 结论

- `zhvpn.exe` 作为客户端本机唯一控制面。
- 桌面 GUI 继续调用 CLI sidecar。
- 计划中的 Python SDK 也直接调用 CLI，不调用 GUI。
- SDK 不重新实现 WireGuard、sing-box、Hub bootstrap、PID 管理、Android 换 IP 等核心逻辑。

## 文档更新

- README 增加 SDK 目录说明和“GUI/SDK 都走 CLI”的 MVP 原则。
- 架构文档增加 Python SDK 角色和客户端控制面原则。
- 桌面 GUI 方案补充“新增能力优先补 CLI，再由 GUI/SDK 调用”。
- 新增 Python SDK 实现方案文档。
