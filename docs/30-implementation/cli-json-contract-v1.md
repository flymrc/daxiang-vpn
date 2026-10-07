# CLI JSON v1 维护源与消费者

> 2026-10-06 JST；本地实现。该版本不表示生产客户端或 Hub 已升级。

`shared/contracts/source.go` 是公开 CLI DTO、字段约束、状态规则和证据语义的唯一声明源。`app.go` 的结果/status 类型与 `shared/proxy` 的引擎身份使用生成的 Go 类型；JSON 输出边界统一写入 `contract_version=1`，包括缺配置和命令失败的结果。引擎身份的字段次序保持不变，现有控制 HMAC 协议没有升级或改变签名输入。

## 生成与漂移检查

在仓库根目录执行：

```powershell
go generate ./shared/contracts
go run ./shared/contracts/cmd/contractgen -check
```

生成器确定性维护以下投影；禁止直接修改生成文件：

- `shared/contracts/cli_v1_generated.go`：CLI 与引擎身份 DTO。
- `shared/contracts/cli-v1.schema.json`：JSON Schema 2020-12，包含 Status、Result、EngineIdentity 定义。
- `shared/contracts/cli_v1_generated.ts`：共享 TypeScript 投影。
- `clients/desktop-gui/src/lib/contracts.generated.ts`：GUI 项目内的同源投影。
- `sdk/python/src/zongheng_vpn/_contract_generated.py`：Python TypedDict、约束与证据说明。

`-check` 检查全部五份产物；任一缺失或漂移返回失败。`scripts/check-client-safety.ps1` 在测试之前调用它。

## 三个版本边界

`contract_version` 描述公开 JSON；`control_protocol_version` 描述本地 HMAC 控制协议；`version` 与原有 `protocol_version` 描述二进制/sidecar。三个版本不能互相推定。新字段不能默默升级引擎停止权限。

没有 `contract_version` 的旧结果仍可被 SDK 读取；旧字段保持原义。已出现但不支持的版本、错误的已知字段类型、相互矛盾的状态和公开 JSON 中的私密存储字段返回 `ZHVpnContractError`。兼容新增字段允许保留，但不增强身份或健康证据。

## 状态与证据

完整的 `engine_state=ready`、instance/generation/control 身份表示本地实例就绪。`running=true` 与 `proxy_reachable=false` 可以并存，表示本地实例已经就绪、TCP 探测没有成功。普通端口监听和 legacy `running` 不能提供新版认证身份。

`proxy_reachable` 只是一时的 TCP 观察，不是代理转发、WG 隧道或出口业务成功。出口名称来自配置；IP 字段是可选探测的观察。缺失的 IP、IPv4/IPv6、隧道健康和验证时间均保持 unknown。v1 没有验证时间，SDK 不以解析时刻伪造 `verified_at`，也不把此前 IP 当作本次观察。

Python `Status.evidence` 提供只读证据视图；legacy 输出的该视图为 unknown，旧 `Status.running` 等属性继续保留。违反合同的异常保留字段路径、错误码和退出码；命令/token、解析后的私密字段及其诊断被脱敏。无效 JSON 不回显无法可靠脱敏的原始输出；超时异常不保留包含真实命令的底层异常链。

第八波新增可缺省 `logging_state=healthy/degraded/unknown` 与 `logging_error_code`。healthy/degraded需要完整可信starting/ready/stopping身份；degraded必须有固定错误码，其他状态不带code。运行期sink错误不改变running/engine_state、隧道或出口证据。CLI通过独立purpose-HMAC日志RPC绑定同一完整EngineIdentity，旧child或缺失/坏响应保持unknown；文件操作在途也为unknown。日志healthy不承诺整个历史、crash durability或业务成功，详见[日志合同](client-engine-log.md)。

## 验证边界

共同 fixture 覆盖 legacy/current、部分健康、完整/缺失身份、不兼容版本、类型与私密字段的允许/拒绝场景。Go、Python 消费者和生成漂移检查覆盖同一维护源；另以当前 CLI 的真实 `version --json` 与无配置 `status --json --no-ip-check` 验证 SDK 互操作，不进行登录、启动、修改系统代理或探测真实出口。

本切片没有新增 durable operation ID、rotate 幂等/查询、客户端 HTTP/OpenAPI 合同、Admin 消费者变更或实际隧道健康检查。GUI TypeScript 类型检查不等同于 Tauri Rust 对所有 JSON 输入做运行时 schema 校验；完整 P1.6 仍未完成。
