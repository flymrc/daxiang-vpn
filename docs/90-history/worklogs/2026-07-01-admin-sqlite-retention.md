# 2026-07-01 Hub admin SQLite 容量防护

本次为 `hub/admin` 的 SQLite 管理库增加容量防护,重点防 `audit_events` 和 `admin_login_attempts` 无上限增长。

## 已完成

- 新增 DB maintenance policy:
  - `audit_events` 默认保留 90 天,最多 50000 行。
  - `admin_login_attempts` 默认保留 7 天,最多 10000 行。
  - 过期 `admin_sessions`、`token_leases`、`rotate_locks` 会被清理。
  - 每轮维护后执行 `PRAGMA wal_checkpoint(TRUNCATE)`。
- `zhhub` admin server 启动时先跑一次维护,之后默认每 60 分钟跑一次。
- 新增环境变量:
  - `ZHHUB_ADMIN_AUDIT_RETENTION_DAYS`
  - `ZHHUB_ADMIN_AUDIT_MAX_ROWS`
  - `ZHHUB_ADMIN_LOGIN_ATTEMPT_RETENTION_DAYS`
  - `ZHHUB_ADMIN_LOGIN_ATTEMPT_MAX_ROWS`
  - `ZHHUB_ADMIN_DB_MAINTENANCE_MINUTES`
- audit 写入时限制 actor、source IP、event type、target、detail JSON、result、error code 的长度。
- admin 登录写入时限制 username、source IP、User-Agent、error code 的长度。
- 新增 DB 层测试覆盖 retention、最大行数裁剪、过期 session/lease/lock 清理和超大 detail JSON 截断。

## 风险判断

- 本次不改客户端协议,也不触碰生产拓扑或端口。
- 最可能失控的表是 `audit_events`;第二是 `admin_login_attempts`。
- `token_leases`、`egress_nodes`、`rotate_locks`、`tokens_cache` 都是按主键 upsert 或受出口/token 数限制,容量风险较低。

## 生产部署

- 本地验证:
  - `go test ./...`
  - `npm run build:embed` in `hub/admin/web`
  - `GOOS=linux GOARCH=amd64 go build -o dist/linux-amd64/zhhub ./hub`
- 生产部署:
  - 上传 `/tmp/zhhub-059ef71`。
  - 远端 SHA256 校验通过:`aff3855a6ae5bfd53fc62dc2342ba397d84c7444dde47d0da550735d92b6baf2`。
  - 备份旧二进制与 systemd 配置到 `/root/zongheng-backups/20260701125316-admin-sqlite-retention`。
  - 替换 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。
- 部署后验证:
  - `zhhub.service` active,`caddy.service` active。
  - `127.0.0.1:18080/healthz` 返回 ok。
  - `127.0.0.1:18100/admin/api/health` 返回 ok。
  - `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
  - `https://jp-proxy.ruichao.dev/admin/` 返回 200。
  - `https://jp-proxy.ruichao.dev/api/client/bootstrap` 的 HEAD 返回预期 405。
  - `admin.db-wal` 从约 4.0M 收缩到约 20K,说明启动维护和 WAL checkpoint 生效。
  - 近 5 分钟日志无 `admin db maintenance failed`、panic 或 fatal。
