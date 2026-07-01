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
