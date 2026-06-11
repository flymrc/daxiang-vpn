# zhhub

Hub 授权服务 MVP。

## 功能

- 校验客户授权码。
- 返回客户端运行配置。
- 支持禁用授权码和设置过期日期。

## 启动

```powershell
go run .
```

默认读取：

```text
./config/tokens.yaml
```

默认监听：

```text
0.0.0.0:18080
```

## 环境变量

```text
ZHHUB_TOKENS=/opt/zongheng-vpn/tokens.yaml
ZHHUB_LISTEN=0.0.0.0:18080
```
