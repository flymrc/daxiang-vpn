# 2026-06-09 工作记录：桌面 GUI 客户端方案 + CLI 机器接口（M1）

## 背景

用户要实现桌面版客户端（`clients/desktop-gui/`，此前仅占位）。经讨论确定选型并写实现方案，随后开始第一阶段（M1）。

## 选型结论

- 框架：**Tauri v2 + CLI 子进程**。理由：公司背书（CrabNebula）不易弃坑、体积轻（系统 WebView，~10MB）适合客户分发；本仓库 `dxvpn.exe` 已把提权 / 引擎 / PID 自包含，子进程集成干净。
- helper = **现成的 `dxvpn.exe`**（内嵌 sing-box），**不引入 wg-quick / wireguard.exe**（裸 WG 到不了出口、没有换 IP）。
- 范围：**第一阶段仅 Windows**。`shared/proxy/platform_nonwindows.go` 的 `Start/Stop/WriteSingBoxConfig` 在非 Windows 仍是「未实现」，macOS 包前置依赖是先补 mac 引擎，单列 M5。
- 连接 UX：默认用户态代理 + GUI 自动设/还原 Windows 系统代理（零 UAC）；全局模式 = `--fast`（系统 TUN，弹一次 UAC）。
- 打包：方案 A（NSIS/MSI + 桌面快捷方式，dxvpn.exe 当 sidecar，不装服务）。

方案落档：`docs/30-implementation/desktop-gui.md`，并挂入 `docs/README.md` 索引。

## M1：CLI 机器接口（已完成）

给 `dxvpn.exe` 的 `status / login / rotate-ip` 加 `--json`，默认人读输出不变。

- `clients/cli/main.go`：新增 `app.ErrSilent` 处理——JSON 命令失败时已把 `{"ok":false,…}` 打到 stdout，main 仅退出非 0，不再向 stderr 重复打印。
- `clients/cli/internal/app/app.go`：
  - `printJSON` 用 `json.Encoder` 且 `SetEscapeHTML(false)`，`<授权码>` 等保持可读。
  - `status --json` → `{"running","proxy","proxy_reachable","egress","egress_ip"}`；无配置/出错 → `{"running":false,…,"error":…}` + 退出非 0。
  - `login --json` / `rotate-ip --json` → 成功 `{"ok":true,…}`，失败 `{"ok":false,"error":…}` + 退出非 0。
  - `rotate-ip` 抽出 `performRotate(cfg, opts, quiet)`，`--json` 时静默所有进度输出，stdout 只剩最终 JSON；`waitForPublicIP` 加 `quiet` 参数。
  - 新增 `wantJSON` / `hasFlag` 解析辅助，`parseRotateIPOptions` 接受 `--json`（不改换 IP 行为）。

## 验证

- `go build ./clients/cli/...` 通过。
- `go test ./clients/cli/...` 通过；新增 `wantJSON` / `hasFlag` / `--json` 解析用例。
- `gofmt -l` 干净，`go vet ./clients/cli/...` 通过。
- 冒烟（`DXVPN_HOME` 指向空临时目录）：
  - `status --json` 无配置 → `{"running":false,"proxy_reachable":false,"error":"未找到配置…"}`，退出 1，stderr 无「错误：」前缀。
  - `login BADTOKEN --json` → `{"ok":false,"error":"授权码无效或已过期"}`，退出 1。
  - `status`（人读）输出不变，仍走 stderr「错误：」。
  - `status --bogus` → `未知参数：--bogus`。

## 下一步

M2：Tauri 工程骨架 + sidecar 打包 + `connect/disconnect/status` 走通。
