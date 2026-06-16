# Python SDK 实现方案

> 角色：`sdk/python/`——面向需要在 Python 程序里控制纵横 VPN 的客户或自动化脚本。

## 结论

Python SDK 值得做，但它不应该直接调用 GUI，也不应该重新实现 WireGuard、sing-box、Hub bootstrap、PID 管理或 Android 换 IP 逻辑。

第一版 SDK 应该是 **CLI wrapper**：

```text
Python SDK
    |
    | subprocess + JSON
    v
zhvpn.exe
    |
    v
本地代理 127.0.0.1:7890 -> Hub -> 日本出口
```

`zhvpn.exe` 是本机唯一控制面。GUI 和 SDK 都走 CLI，保持统一架构。

## 当前实现状态

MVP 已落地在 `sdk/python/`。SDK 放在顶层 `sdk/` 下，便于后续扩展 `sdk/js/`、`sdk/go/` 等其它语言实现；它不属于终端 GUI，也不和 `clients/` 下的用户客户端混在一起。

- Python 包名：`zongheng_vpn`，发布名：`zongheng-vpn`。
- 零运行依赖；HTTP convenience helper 可选安装 `requests` extra。
- `Client` 支持 `login`、`connect`、`disconnect`、`status`、`status_ip`、`rotate_ip`、`logout`、`version`。
- `proxies()` / `proxy_url()` 可直接给 Python HTTP 库复用本地代理。
- SDK 异常会脱敏 `ZH-*` token，不记录私钥或完整 WireGuard 配置。

## 范围

SDK 负责：

- 定位 `zhvpn.exe`。
- 调用 CLI 机器接口。
- 把 JSON 输出转成 Python 对象。
- 给 `requests` / `httpx` 提供代理配置。
- 提供常用控制方法：登录、连接、断开、状态、出口 IP、换 IP、登出。

SDK 不负责：

- 直接读写 WireGuard 私钥。
- 直接调用 Hub bootstrap API。
- 启动或嵌入 sing-box。
- 直接操作 GUI、Tauri 或 Windows 系统代理。
- 自己维护第二套运行状态机。

## 建议 API

```python
from zongheng_vpn import Client

vpn = Client()
vpn.login("ZH-XXXX")
vpn.connect()

status = vpn.status()
print(status.running, status.proxy, status.egress)

response = vpn.get("https://api64.ipify.org", timeout=10)
print(response.text)

vpn.rotate_ip()
vpn.disconnect()
```

## 安装

开发/本地安装：

```powershell
python -m pip install -e sdk/python
```

如果需要 SDK 自带的 `get()` / `request()` convenience helper（依赖 `requests`）：

```powershell
python -m pip install -e "sdk/python[requests]"
```

发布到 PyPI 后，目标安装方式是：

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

无论本地安装还是 PyPI 安装，SDK 仍需要能找到本机 `zhvpn.exe`。推荐通过 `ZHVPN_EXE` 指定路径，或把 `zhvpn.exe` 放到 `PATH`。

代理辅助：

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## CLI 依赖

已使用的 CLI 机器接口：

| SDK 方法 | CLI |
| --- | --- |
| `login(token)` | `zhvpn.exe login <token> --json` |
| `connect(fast=False, port=None)` | `zhvpn.exe start [--fast] [--port N] --json` |
| `disconnect()` | `zhvpn.exe stop --json` |
| `status()` | `zhvpn.exe status --json --no-ip-check` |
| `status_ip()` | `zhvpn.exe status --json` |
| `rotate_ip()` | `zhvpn.exe rotate-ip --json` |
| `logout()` | `zhvpn.exe logout --json` |
| `version()` | `zhvpn.exe version --json` |

`start` / `stop` 已补 JSON，避免 Python 解析人读输出。

## 可执行文件定位

建议顺序：

1. 显式参数：`Client(exe_path=".../zhvpn.exe")`。
2. 环境变量：`ZHVPN_EXE`。
3. `PATH` 中的 `zhvpn.exe` / `zhvpn`。
4. Windows 常见安装位置（后续按实际 NSIS 安装路径补）。

找不到时给出明确错误：提示安装桌面客户端或单独下载 CLI。

## 错误模型

SDK 统一抛出 Python 异常，但保留 CLI 原始信息：

- `ZHVpnExecutableNotFound`
- `ZHVpnCommandError`
- `ZHVpnJSONError`
- `ZHVpnTimeout`

异常里保留：

- 命令名。
- 退出码。
- stdout / stderr 的安全摘要。
- JSON error 字段。

不得记录 token、私钥、完整 WireGuard 配置。

## 版本策略

Python SDK 版本不等于 GUI 版本。SDK 应声明最低 CLI 版本或能力探测结果：

- 没有 `start --json` 时，可退回退出码模式，但给 warning。
- 没有 `version --json` 时，通过命令失败判断为旧 CLI。
- 关键能力缺失时直接报错，不做猜测。

## 已完成的 MVP 顺序

1. 给 `zhvpn.exe start/stop/version` 补 `--json`。
2. 在 `sdk/python/` 增加 Python package 骨架。
3. 实现 `Client`、结果 dataclass、异常类型。
4. 增加单元测试：用 fake `zhvpn.exe` 脚本模拟 JSON / 非 0 / 非法 JSON。
5. 增加最小 README：安装、定位 CLI、`requests` 示例。

## 后续增强

- 根据实际 NSIS 安装目录继续增强 `zhvpn.exe` 自动定位。
- 增加真实 CLI 的集成测试（使用临时 `ZHVPN_HOME`，避免污染用户配置）。
- 如果客户大量使用 `httpx`，补 `httpx` 专用 helper 或文档示例。
