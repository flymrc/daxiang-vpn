# zongheng-vpn Python SDK

3 分钟版：装好 SDK，拿 token 登录，`connect()` 以后 Python 程序就可以走本机代理访问网络。

## 你需要准备

- Windows 机器
- Python 3.9+
- SDK wheel 文件，例如 `zongheng_vpn-0.1.0-py3-none-win_amd64.whl`
- 一个纵横 VPN token，例如 `ZH-XXXX`

## 1. 安装

在 wheel 文件所在目录执行：

```powershell
python -m pip install .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```

如果要使用 `vpn.get()` / `vpn.request()` 这类 HTTP 辅助方法，再安装：

```powershell
python -m pip install requests
```

## 2. 最小示例

```python
from zongheng_vpn import Client

vpn = Client()

vpn.login("ZH-XXXX")
vpn.connect()

print(vpn.status())

vpn.disconnect()
```

`login()` 会把 token 保存到本机。第一次使用先登录一次，之后通常直接 `connect()` 即可；换 token 或清理登录信息时再调用 `logout()`。

## 3. 发请求

方式一：使用 SDK 自带的请求方法，需要先安装 `requests`。

```python
from zongheng_vpn import Client

vpn = Client()
vpn.connect()

response = vpn.get("https://api64.ipify.org", timeout=10)
print(response.text)
```

方式二：给你自己的 HTTP client 使用代理地址。

```python
from zongheng_vpn import Client

vpn = Client()
vpn.connect()

proxies = vpn.proxies()
print(proxies)
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## 常用操作

```python
vpn.login("ZH-XXXX")     # 登录
vpn.connect()            # 连接
vpn.status()             # 查看状态
vpn.status_ip()          # 查看状态，并检查出口 IP
vpn.rotate_ip()          # 切换出口 IP
vpn.disconnect()         # 断开
vpn.logout()             # 清除本机登录信息
```

## 升级

如果 VPN 是通过 Python SDK 连接的，升级前先断开：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```

首次安装不会覆盖已经安装的桌面客户端或命令行客户端。

## 指定 CLI 路径

一般不需要设置。只有在你明确要使用某个指定的 `zhvpn.exe` 时才需要：

```powershell
$env:ZHVPN_EXE="C:\Path\To\zhvpn.exe"
```

也可以在代码里传：

```python
vpn = Client(exe_path=r"C:\Path\To\zhvpn.exe")
```
