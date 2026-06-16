# zongheng-vpn Python SDK

用于在 Python 程序里控制纵横 VPN：登录、连接、断开、查看状态、切换出口 IP，并获取本地代理地址。

- 安装包名：`zongheng-vpn`
- Python 导入名：`zongheng_vpn`

## 安装

给同事分发时，推荐直接安装构建好的 wheel：

```powershell
python -m pip install .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```

如果需要 SDK 自带的 `get()` / `request()` 辅助方法：

```powershell
python -m pip install ".\zongheng_vpn-0.1.0-py3-none-win_amd64.whl[requests]"
```

发布到 PyPI 后，可以改用：

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

开发本仓库时，可以从源码安装：

```powershell
.\sdk\python\build.ps1 -Install
```

带 `requests` 辅助方法：

```powershell
.\sdk\python\build.ps1 -Install -WithRequests
```

## 快速开始

```python
from zongheng_vpn import Client

vpn = Client()
vpn.login("ZH-XXXX")
vpn.connect()

status = vpn.status()
print(status.running, status.proxy, status.egress)

vpn.rotate_ip()
vpn.disconnect()
```

## 访问网络

如果安装了 `requests` extra，可以直接用 SDK 发请求：

```python
response = vpn.get("https://api64.ipify.org", timeout=10)
print(response.text)
```

如果业务代码已经自己管理 HTTP client，可以只取代理配置：

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## 常用方法

- `login(token)`：保存登录 token。
- `connect(fast=False, port=None)`：启动本地 VPN。
- `disconnect()`：停止本地 VPN。
- `status()`：查看本地运行状态。
- `status_ip()`：查看状态并检查出口 IP。
- `rotate_ip()`：切换出口 IP。
- `logout()`：清除本地登录信息。
- `proxy_url()` / `proxies()`：获取本地代理地址。

## 升级注意

首次安装 SDK 不会覆盖已经安装的桌面 GUI 或独立 CLI。

升级或重装 SDK 前，如果当前 VPN 是通过 Python SDK 连上的，建议先断开：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade zongheng-vpn
```

如果还没有发布到 PyPI，把第二行换成新的 wheel 文件：

```powershell
python -m pip install --upgrade .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```

如果明确要让 SDK 使用指定的 `zhvpn.exe`，可以设置：

```powershell
$env:ZHVPN_EXE="C:\Path\To\zhvpn.exe"
```

或者在代码里传：

```python
vpn = Client(exe_path=r"C:\Path\To\zhvpn.exe")
```

## 构建 wheel

```powershell
.\sdk\python\build.ps1 -Wheel
```

生成的 wheel 位于 `sdk/python/dist/`。
