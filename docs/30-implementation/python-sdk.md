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
- SDK 优先使用随 Python 包打进去的 `zongheng_vpn/bin/zhvpn.exe`，降低用户手动配置成本。

## 命名约定

最终命名：

| 场景 | 名称 | 说明 |
| --- | --- | --- |
| pip / PyPI 发布名 | `zongheng-vpn` | 对外品牌名，和仓库 Go module `zongheng-vpn` 保持一致 |
| Python import 包名 | `zongheng_vpn` | Python 标识符不能包含 `-`，因此用下划线 |
| CLI / sidecar 二进制 | `zhvpn` / `zhvpn.exe` | 内部短名，继续用于命令行、GUI sidecar 和 SDK 调用目标 |

不采用 `zhvpn` 作为 pip 包名：它更像 CLI 二进制名，品牌感弱，也容易让用户误以为安装的是一个 Python CLI。  
不采用 `zh-vpn`：仓库内没有这个命名体系，会额外制造第三套名字。

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
.\sdk\python\build.ps1 -Install
```

该脚本会先把 Go CLI 编译为 `sdk/python/src/zongheng_vpn/bin/zhvpn.exe`，再执行 editable install。安装后 `Client()` 默认使用包内 CLI，不需要手动设置 `ZHVPN_EXE`。

如果需要 SDK 自带的 `get()` / `request()` convenience helper（依赖 `requests`）：

```powershell
.\sdk\python\build.ps1 -Install -WithRequests
```

发布 Windows wheel 到 PyPI 后，目标安装方式是：

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

PyPI wheel 应内置匹配架构的 `zhvpn.exe`，用户不需要额外安装 CLI。`ZHVPN_EXE` 只作为高级覆盖项保留。

安装 / 升级行为：

- 首次安装 SDK 不会覆盖已经安装的桌面 GUI 或独立 `zhvpn.exe`。
- wheel 内置的可执行文件位于 Python 自己的 `site-packages/zongheng_vpn/bin/zhvpn.exe`，不会写入 GUI 安装目录，也不会注册全局 `zhvpn` 命令。
- 如果机器上已有 GUI 或 `PATH` 里的独立 CLI，SDK 默认仍用包内 CLI，避免版本契约不匹配；运行态通过默认 `ZHVPN_HOME` 共享。
- 唯一需要注意的是升级 / 重装 SDK 时，如果旧 SDK 包内的 `zhvpn.exe` 正在运行，Windows 可能锁住文件，导致 `pip install --upgrade` 替换失败。升级前先执行：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade zongheng-vpn
```

如果 VPN 是由 GUI 或独立 CLI 启动的，它不会锁住 SDK 包内的 exe，但仍会共享同一运行态。

构建发布 wheel：

```powershell
.\sdk\python\build.ps1 -Wheel
```

wheel 会写到 `sdk/python/dist/`，并包含 `zongheng_vpn/bin/zhvpn.exe`。因为包含 Windows 可执行文件，发布包应是平台 wheel，例如 `py3-none-win_amd64`，不能是 `any` wheel。

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
2. 显式命令：`Client(command=[...])`（测试或高级用法）。
3. 环境变量：`ZHVPN_EXE`。
4. Python 包内置：`zongheng_vpn/bin/zhvpn.exe`。
5. `PATH` 中的 `zhvpn.exe` / `zhvpn`。
6. Windows 常见安装位置（后续按实际 NSIS 安装路径补）。

找不到时给出明确错误：提示安装桌面客户端或单独下载 CLI。

### 已安装 GUI / CLI 时的行为

如果机器上已经装过桌面 GUI 或独立 `zhvpn.exe`，SDK 默认仍使用自己 wheel / editable install 内置的 CLI，保证 SDK 与 CLI JSON 契约版本匹配。

运行态默认共享同一个 `ZHVPN_HOME`（未设置时是 `%LOCALAPPDATA%\ZonghengVPN`），因此 GUI 和 SDK 会看到同一份登录、状态、本地代理、PID 信息。Python 里执行 `disconnect()` 会停止 GUI 当前看到的同一个本地 VPN 引擎；反过来，GUI 断开后 SDK 的 `status()` 也会看到未连接。

如果明确想让 SDK 使用已有 GUI/CLI 的 `zhvpn.exe`，可以设置：

```powershell
$env:ZHVPN_EXE="C:\Path\To\zhvpn.exe"
```

或在代码里传：

```python
vpn = Client(exe_path=r"C:\Path\To\zhvpn.exe")
```

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
5. 增加本地打包脚本：把 Go CLI 编进 Python 包，再安装 SDK。
6. 增加最小 README：安装、定位 CLI、`requests` 示例。

## 后续增强

- 发布 PyPI 前生成 Windows x64 / arm64 平台 wheel，并确认 wheel tag 不是 `any`。
- 根据实际 NSIS 安装目录继续增强备用 `zhvpn.exe` 自动定位。
- 增加真实 CLI 的集成测试（使用临时 `ZHVPN_HOME`，避免污染用户配置）。
- 如果客户大量使用 `httpx`，补 `httpx` 专用 helper 或文档示例。
