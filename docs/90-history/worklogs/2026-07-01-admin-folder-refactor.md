# 2026-07-01 Hub admin 目录重构

本次按“顶层 facade + internal 实现 + generated 目录显式隔离”的结构整理 `hub/admin`。

## 已完成

- `hub/admin` 顶层只保留对外入口:
  - `admin.go`:公开 `NewServer` 与 `Server`。
  - `config.go`:公开 `Config` 与环境变量读取。
  - `password.go`:公开管理员密码 hash/verify helper。
  - `generate.go`:保留统一 `go generate ./hub/admin` 入口。
- 后端 HTTP 实现迁到 `hub/admin/internal/api/`,并按职责拆为 server、auth、handlers、summaries、response、audit。
- SQLite schema、queries、store 迁到 `hub/admin/internal/db/`。
- sqlc 生成代码迁到 `hub/admin/internal/db/generated/`,包名保持 `generated`。
- OpenAPI 合同和 oapi-codegen 配置迁到 `hub/admin/internal/spec/`。
- OpenAPI Go 生成代码迁到 `hub/admin/internal/spec/generated/openapi_types.go`,包名保持 `generated`。
- Argon2id 密码实现迁到 `hub/admin/internal/security/`。
- 前端 Go embed shim 放在 `hub/admin/web/ui.go`;前端源码仍统一在 `hub/admin/web/`。
- 前端 `npm run generate:api` 改为读取 `../internal/spec/openapi.yml`。

## 说明

- `internal` 用于表达 Go 包可见性边界:admin 的实现细节不再被仓库其他角色随意 import。
- OpenAPI 与 sqlc 分别使用自己的 `generated` 目录,避免不同生成器产出的同名类型冲突。
- 本次是本地代码结构重构,不改生产拓扑、端口或线上服务状态。
