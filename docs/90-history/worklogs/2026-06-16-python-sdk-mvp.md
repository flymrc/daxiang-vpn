# 2026-06-16 Python SDK MVP

## 背景

确认客户端统一架构：GUI 和 Python SDK 都通过 `zhvpn.exe` CLI 控制本机运行态，避免 SDK 调 GUI 或另写一套 WireGuard / sing-box / Hub bootstrap 逻辑。

## 实现

- 新增 `clients/python-sdk/`，与 `clients/cli/`、`clients/desktop-gui/` 平级。
- Python 包发布名 `zongheng-vpn`，导入名 `zongheng_vpn`。
- 实现 `Client`：
  - `login`
  - `connect`
  - `disconnect`
  - `status`
  - `status_ip`
  - `rotate_ip`
  - `logout`
  - `version`
  - `proxy_url` / `proxies`
  - 可选 `requests` convenience helper
- 实现 typed dataclass 结果和 SDK 异常。
- 异常里的命令、stdout/stderr、payload 会脱敏 `ZH-*` token。
- CLI 补齐 `start --json`、`stop --json`、`version --json`，让 SDK 不解析人读文本。

## 验证

```powershell
go test ./clients/cli/...
python -m unittest discover clients/python-sdk/tests
```

两项均通过。
