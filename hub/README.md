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
0.0.0.0:18080       迁移兼容入口
127.0.0.1:18079     Caddy 专用可信代理入口
```

## 环境变量

```text
ZHHUB_TOKENS=/opt/zongheng-vpn/tokens.yaml
ZHHUB_LISTEN=0.0.0.0:18080
ZHHUB_TRUSTED_PROXY_LISTEN=127.0.0.1:18079
```

两个客户端入口必须使用不同端口，可信代理入口必须是 loopback IP 字面量；配置不满足时 Hub 启动失败。Caddy 完成迁移后只把客户端 HTTPS 路由反代到 `127.0.0.1:18079`，公网 `18080` 在观察期继续兼容旧客户端。
