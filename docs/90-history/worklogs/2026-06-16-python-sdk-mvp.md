# 2026-06-16 Python SDK MVP

## 背景

确认客户端统一架构：GUI 和 Python SDK 都通过 `zhvpn.exe` CLI 控制本机运行态，避免 SDK 调 GUI 或另写一套 WireGuard / sing-box / Hub bootstrap 逻辑。

## 实现

- 新增 `sdk/python/`。SDK 归到顶层 `sdk/`，方便后续扩展 `sdk/js/` 等其它语言 SDK。
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
- SDK 优先发现包内 `zongheng_vpn/bin/zhvpn.exe`。
- 新增 `sdk/python/build.ps1`，用于把 Go CLI 编入 Python 包并本地安装 SDK。
- 若机器已安装 GUI/CLI，SDK 默认用包内 CLI；可通过 `ZHVPN_EXE` 或 `Client(exe_path=...)` 指向已有 CLI。默认运行态仍共享同一 `ZHVPN_HOME`。
- 明确安装 / 升级边界：SDK 安装不会覆盖 GUI 或独立 CLI；升级时只有旧 SDK 包内 `zhvpn.exe` 正在运行才可能被 Windows 锁文件，需要先断开再 `pip install --upgrade`。

## 命名决策

- pip / PyPI 发布名：`zongheng-vpn`。
- Python import 包名：`zongheng_vpn`。
- CLI / sidecar 二进制：`zhvpn` / `zhvpn.exe`。
- 不采用 `zhvpn` 作为 pip 包名，避免把“CLI 短名”和“品牌发布名”混在一起。
- 不采用 `zh-vpn`，仓库内没有这个命名体系。

## 目录修正

最初草稿放在 `clients/python-sdk/`，但 SDK 不是终端 GUI / CLI 客户端本体。最终迁到 `sdk/python/`，与未来 `sdk/js/` 等语言 SDK 形成统一布局。

## 验证

```powershell
go test ./clients/cli/...
python -m unittest discover sdk/python/tests
.\sdk\python\build.ps1
```

两项测试和 SDK 打包脚本均通过。
