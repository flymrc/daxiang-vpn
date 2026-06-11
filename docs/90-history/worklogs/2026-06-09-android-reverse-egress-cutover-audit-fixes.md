# 2026-06-09 Android reverse egress cutover audit fixes

## 背景

对 Android 出口从旧 `zhandroid-egress` / `10.66.0.101:1080` 切到新 `zhreverse` / `10.66.0.1:18081` 的代码和配置做审计后,发现几类切换风险:

- 本地 Hub token 配置仍可能把 Android 客户端指向旧代理地址。
- QUIC reverse client 跳过服务端证书校验,共享 token 可能被主动 MITM 获取。
- Hub 端 tunnel listener 启动失败时,proxy 仍可能启动并让 systemd 显示 active。
- `/fetch` 诊断入口默认暴露。
- Hub 侧 proxy 缺少来源 ACL。
- 旧 `egress/proxy` 文档仍写 Android 路径在用。

## 修改

- `egress/reverse`:
  - Hub server 支持 `tls_cert_file` / `tls_key_file`,用于固定 QUIC 服务端证书。
  - Android client 新增 `server_cert_sha256`,QUIC 模式默认必须配置证书 SHA-256 pin。
  - 保留 `--insecure-skip-verify` 作为临时实验开关,不用于生产。
  - tunnel listener 启动成功后才启动 Hub 侧 HTTP proxy;监听失败会直接返回错误。
  - `/fetch` 默认关闭,需 `server.enable_fetch: true` 显式启用。
  - 新增 `allowed_proxy_cidrs`,Hub 侧 proxy 会拒绝不在 ACL 内的来源。
- `hub/config/tokens.yaml`:
  - Android token 的 `egress.proxy_addr` 改为 `10.66.0.1:18081`。
- 配置示例:
  - `hub-reverse-server.yaml.example` 增加证书文件、proxy ACL 和 `enable_fetch: false`。
  - `android-reverse-client.yaml.example` 增加 `server_cert_sha256` 占位字段。
- 文档:
  - `egress/reverse/README.md` 补证书生成、指纹计算、pinning 和 `/fetch` 启用说明。
  - `docs/20-operations/runbooks/server-access.md` 补 Hub QUIC cert/key 与 Android pin 要求。
  - `egress/proxy/README.md` 把 Android `zhandroid-egress` 标为回滚路径。

## 验证

```powershell
go test ./...
```

结果:通过。

## 后续部署注意

线上 Hub 需要安装持久化 `/etc/zongheng/zhreverse/server.crt` 和 `/etc/zongheng/zhreverse/server.key`,计算证书 DER SHA-256 后写入 Android `/data/adb/zhreverse/client.yaml` 的 `client.server_cert_sha256`,再重启 `zhreverse-hub.service` 和 Android `99-zhreverse-egress.sh`。
