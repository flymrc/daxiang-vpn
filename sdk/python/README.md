# zongheng-vpn Python SDK

用于在 Python 脚本或应用里控制纵横 VPN 的 SDK。

这个 SDK 是 `zhvpn.exe` 的轻量封装。它不调用 GUI，不直接访问 Hub API，也不重新实现 WireGuard、sing-box 或本地运行态逻辑。

命名约定：

- pip 安装包：`zongheng-vpn`
- Python import：`zongheng_vpn`
- CLI 可执行文件：`zhvpn` / `zhvpn.exe`

```text
Python SDK -> zhvpn.exe --json -> 本地代理 127.0.0.1:7890
```

## 本地安装

```powershell
.\sdk\python\build.ps1 -Install
```

这个脚本会先把 Go CLI 编译进 Python 包，路径是 `zongheng_vpn/bin/zhvpn.exe`，然后用 editable mode 安装 SDK。安装后直接使用 `Client()` 即可，不需要手动设置 `ZHVPN_EXE`。

如果需要 `get()` / `request()` 这类 convenience helper：

```powershell
.\sdk\python\build.ps1 -Install -WithRequests
```

发布 Windows wheel 到 PyPI 后，目标安装方式是：

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

PyPI wheel 应该内置匹配版本的 `zhvpn.exe`，用户不需要额外安装 CLI。

## 安装和升级行为

安装 SDK 不会覆盖已经安装的桌面 GUI 或独立 `zhvpn.exe`。内置可执行文件会安装到 Python 自己的 `site-packages/zongheng_vpn/bin/` 目录里，SDK 也不会注册全局 `zhvpn` 命令。

正常场景：

- 机器上已经有 GUI，再首次安装 SDK：不冲突。
- 机器上已经有 `PATH` 里的独立 CLI，再首次安装 SDK：不冲突。
- SDK、GUI、独立 CLI 可以通过默认 `ZHVPN_HOME` 共享同一份运行态。

唯一需要注意的是：升级或重装 SDK 时，如果旧 SDK 包内的 `zhvpn.exe` 正在运行，Windows 可能锁住旧文件，导致 `pip install --upgrade` 替换失败。先断开，再升级：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade zongheng-vpn
```

如果 VPN 引擎是从 GUI 或独立 CLI 启动的，它不会锁住 SDK 包内的 exe。但它们仍共享运行态，所以任意一边断开，另一边也会看到状态变化。

## 构建发布 wheel

```powershell
.\sdk\python\build.ps1 -Wheel
```

wheel 会写入 `sdk/python/dist/`，并包含 `zongheng_vpn/bin/zhvpn.exe`。Windows wheel 是平台相关包，例如 `zongheng_vpn-0.1.0-py3-none-win_amd64.whl`。

## 使用示例

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

如果业务代码已经自己管理 HTTP client，可以直接使用代理辅助方法：

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## CLI 查找顺序

SDK 按以下顺序查找 `zhvpn.exe`：

1. `Client(exe_path="...")`
2. `Client(command=[...])`，用于高级用法或测试
3. `ZHVPN_EXE`
4. Python 包内置二进制：`zongheng_vpn/bin/zhvpn.exe`
5. `PATH` 中的 `zhvpn.exe` / `zhvpn`
6. 常见 Windows 安装位置

只有在明确想覆盖包内 CLI 时，才需要设置 `ZHVPN_EXE`。

## 已安装 GUI 或 CLI 时

如果机器上已经安装过桌面 GUI 或独立 `zhvpn.exe`，SDK 默认仍使用自己包内的 CLI。这样可以保证 SDK 和 CLI 的 JSON 契约版本匹配。

所有 CLI 默认共享同一个运行目录（`%LOCALAPPDATA%\ZonghengVPN`），所以 GUI 和 SDK 会看到同一份登录、状态、代理和 PID 信息。在 Python 里调用 `vpn.disconnect()`，会停止 GUI 当前显示为已连接的同一个本地 VPN 引擎。

如果明确要让 SDK 使用已有 CLI，可以设置：

```powershell
$env:ZHVPN_EXE="C:\Path\To\zhvpn.exe"
```

或者在代码里传：

```python
vpn = Client(exe_path=r"C:\Path\To\zhvpn.exe")
```
