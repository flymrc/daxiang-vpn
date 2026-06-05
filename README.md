# 大象 VPN

大象 VPN 是一个 Hub + 日本住宅出口 + Windows 客户端的代理网络项目。

## 目录

```text
backend/
  dxhub/              Hub 授权 API

frontend/
  dxvpn/              Windows 客户端 CLI

docs/
  README.md           文档总入口
  00-overview/        现状、MVP、客户端指南
  10-architecture/    架构设计、出口选型
  20-operations/      运维、部署、示例配置
  30-implementation/  具体实现方案
  40-security/        安全审查和安全 TODO
  90-history/         工作记录、阶段复盘

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

- [文档总入口](docs/README.md)
- [当前 MVP 计划](docs/00-overview/mvp-plan.md)
- [总体架构](docs/10-architecture/system-architecture.md)
- [出口方案选型](docs/10-architecture/egress-strategy.md)
- [运维诊断命令手册](docs/20-operations/runbooks/diagnostics.md)
- [服务器访问与当前状态](docs/20-operations/runbooks/server-access.md)
- [安全与抗封改进 TODO](docs/40-security/security-todo.md)
- [Hub 安全审查报告 2026-06-04](docs/40-security/security-audit-2026-06-04.md)
