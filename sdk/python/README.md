# zongheng-vpn Python SDK

用 Python 控制纵横 VPN。连接后，本机代理地址是 `127.0.0.1:7890`。

## 安装

```powershell
python -m pip install .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```

如果要用 `vpn.get()` / `vpn.request()`：

```powershell
python -m pip install requests
```

## 使用

```python
from zongheng_vpn import Client

vpn = Client()

vpn.login("ZH-XXXX")  # 第一次使用需要
vpn.connect()

print(vpn.status())
print(vpn.get("https://api64.ipify.org", timeout=10).text)

vpn.disconnect()
```

如果不用 SDK 发请求，只取代理配置：

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## 常用

```python
vpn.connect()
vpn.disconnect()
vpn.status()
vpn.status_ip()
vpn.rotate_ip()
vpn.logout()
```

## 升级

如果当前 VPN 是 Python SDK 连上的，升级前先断开：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade .\zongheng_vpn-0.1.0-py3-none-win_amd64.whl
```
