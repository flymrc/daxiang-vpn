# 大象 VPN

大象 VPN 是一个 Hub + 日本住宅出口 + Windows 客户端的代理网络项目。

## 目录

```text
backend/
  dxhub/              Hub 授权 API

frontend/
  dxvpn/              Windows 客户端 CLI

docs/
  architecture/       架构文档
  implementation/     实施文档
  ops/                运维和部署文档

dist/
  windows-amd64/      Windows x64 客户端发布包
  windows-arm64/      Windows ARM64 客户端发布包
```

## 当前 MVP

```text
dxvpn.exe login <授权码>
-> Hub 校验 token
-> Hub 返回运行配置
-> dxvpn.exe start
-> 本地代理 127.0.0.1:7890
-> 日本住宅出口
```

## 客户端命令

```powershell
dxvpn.exe login <授权码>
dxvpn.exe start            # 本地代理端口默认 7890
dxvpn.exe start --port 7891  # 端口被占用时换端口（也可用环境变量 DXVPN_LOCAL_PORT）
dxvpn.exe status
dxvpn.exe stop
```

## 关键文档

- [Hub 授权 API MVP](docs/implementation/AUTH_API_MVP.md)
- [Hub 授权 API 部署](docs/ops/HUB_API_DEPLOY.md)
- [运维诊断命令手册](docs/ops/DIAGNOSTICS.md)
- [总体架构](docs/architecture/ARCHITECTURE.md)
