# 2026-06-11 生产 Hub token 前缀修正

## 背景

Windows GUI 0.3.2 安装包登录 `ZH-JP-TEST-100` 时返回“授权码无效或已过期”。本地直接请求 Hub API 复现为 `401 {"error":"invalid_token"}`。

## 排查

- 客户端默认授权 API 为 `http://36.50.84.68:18080`。
- 生产 18080 实际仍由旧 `dxhub.service` 提供：
  - unit：`/etc/systemd/system/dxhub.service`
  - binary：`/opt/daxiang-vpn/dxhub/dxhub`
  - tokens：`/opt/daxiang-vpn/dxhub/tokens.yaml`
- 实际 token 文件里仍是 `DX-JP-TEST-*` 段名，没有 `ZH-JP-TEST-*`，因此 ZH 授权码被拒绝。

## 处理

- 备份线上 token 文件：
  - `/opt/daxiang-vpn/dxhub/tokens.yaml.bak.20260611-030246-zh-token-rename`
- 仅将 token 段名从 `DX-JP-TEST-*` 改为 `ZH-JP-TEST-*`。
- 重启 `dxhub.service`。

## 验证

- `dxhub.service` 重启后为 `active`。
- `ZH-JP-TEST-100` bootstrap 返回 HTTP 200。
- `ZH-JP-TEST-100` 当前绑定：
  - client：`my-test`
  - expires_at：`2026-12-31`
  - egress：`jp-android-01`
  - proxy_addr：`10.66.0.1:18081`
  - WireGuard address：`10.66.0.30/24`

## 后续

当前仅修正生产 token 前缀，未迁移 service 名称和目录。后续如要彻底完成品牌迁移，应把 `dxhub.service`、`DXHUB_*` 环境变量、部署目录和二进制一起迁到 `zhhub`/`ZHHUB_*`。
