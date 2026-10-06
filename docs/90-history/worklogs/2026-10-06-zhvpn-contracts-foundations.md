# 2026-10-06 CLI 合同、授权与代理租约基础

## 范围与基点

用户指出“整体 Steelman 尚未完成”并追问原因。前一轮停在客户端安全首切片，未执行完全部重构；这次继续本地可完成的合同、持久授权与租约基础，并保持全阶段门禁未完成。源码在隔离 `codex/zhvpn-steelman-runtime`，基于首切片 `9113a3a`；没有推送、部署、变更真实 peer/系统代理或执行已取消的 Jetstar。

原工作区未被本切片写入。相对首轮 79 文件 hash 清册，复核时 74 项仍相同，5 份文档包含另一路新增的个人 RDP/SSH 内容；已保留这些变化，不用旧清册覆盖它们。当前 79 文件另存 phase2 hash 清册，初始清册保持原样。工作树基点含早先本地未推 observer 提交，不能未经处理直接推整个分支。

## 实现与文档

- `shared/contracts/source.go` 同源生成 Go、JSON Schema、两份 TypeScript 投影和 Python 类型/约束；CLI 与引擎身份使用生成类型，公开成功/失败/status 输出加 `contract_version=1`，原 HMAC 消息字段次序和控制协议不变。
- GUI API 消费生成 DTO，保留 GUI 的恢复错误/warning 及 wrapper 必填 message；没有切换真实系统代理命令。SDK 严格校验已知类型/版本/状态关系，legacy 字段兼容但不提供认证证据，缺失隧道与验证时间保持 unknown。
- SDK 异常不泄露登录参数、私密字段或无法可靠脱敏的无效 JSON，超时不保留真实 subprocess command 的异常链。
- `hub/internal/deviceauth` 实现隔离 SQLite 的 ownership/generation/binding 历史/tombstone/幂等 operation/outbox/intent，与单机跨进程文件 fence。新 generation 使旧任务 superseded，但保留撤销义务及原预算；过期、超时和不可信运行态不伪报生效。
- `clients/cli/internal/runtime/systemproxy` + `shared/systemproxy` 实现 v2 租约状态机与固定 SID/KnownFolder Windows adapter；WAL 先于 OS 写入，锁/目录固定句柄和锁后身份复核，通知后再次读回。真实 phase/lifetime Authority、CLI/RPC/stop hook 仍未接入。
- 本地门禁增加生成漂移和离线 deviceauth race。README、架构、诊断、实现合同、安全迁移/TODO、docs 入口与 checkbox 进度同步更新。

精确合同见 [CLI JSON v1](../../30-implementation/cli-json-contract-v1.md)、[授权基础](../../30-implementation/device-auth-foundation.md)、[代理租约基础](../../30-implementation/system-proxy-lease-foundation.md)。

## 独立复核及修复

| 问题 | 原反例与最终结果 |
| --- | --- |
| DB 身份只在排队前检查 | 等 fence 时恢复旧数据库可使旧 apply 复活；拿锁后二次身份核验、Windows 持续 pin DB。独立原反例变为 ErrPolicy，无复活 |
| Snapshot 后已知过期仍 apply | 原模型先新增 peer 再 ErrExpired；现动作前重核并持久 expiry generation/tombstone/outbox，独立原反例不调用 Apply |
| Policy slice 不是真正私有快照 | 并发 New 有 race，调用者 reload 可松动已持久保护策略；现 normalize 前显式复制，两个独立 race/保护反例通过 |
| 旧 pending 阻断已核验新 generation | superseded 任务不再永久占用新 grant 门禁，旧 tombstone 预算仍保留；持久回归通过 |
| Notify 后发生外部设置变化 | 原模型会直接成功/删除恢复材料；现再读回、冲突保留 WAL，Acquire/Recover 负例通过 |
| `fields`/`Fields` 大小写别名 | 原 journal 可触发 raw/typed 数组长度不一致并 panic；已先复现，再以精确名称、折叠判重与长度检查拒绝 |

这些反例在隔离 SQLite、文件模拟数据面、fake adapter 或合成 HKCU 上执行；真实子进程证明锁和崩溃行为，不能冒称实际 WG/真实 WinINET 验收。

## 验证结果

最终整入口 `pwsh -NoProfile -File scripts/check-client-safety.ps1` exit 0；脱敏原始日志位于私有证据目录 `.local/zongheng-vpn/steelman/2026-10-06/contracts-foundations-gates.txt`。

| 验证 | 结果与限制 |
| --- | --- |
| Go 全仓 | `with_gvisor` test/vet 通过；CLI/shared/reverse race 与新增 deviceauth race 通过 |
| 同源合同 | 5 个投影 drift 检查、4 个合同测试、25 项共同 fixture；当前 CLI version 与缺配置 status 的 SDK 实际互操作通过 |
| SDK | 17/17 通过；没有实际登录/启动/公网探测 |
| 代理基础 | 17 个 core + 7 个 Windows 顶层测试，含表驱动子例；真实内核句柄/child、合成 HKCU/no notify；race/vet 通过 |
| 授权基础 | 31 个顶层入口，30 个实质套件与 1 个 child helper；六处进程崩溃、双进程旧任务重放、独立四项修复重放通过；8.3 子例因卷禁用而跳过 |
| GUI | npm check 零错误/警告；现有 Rust library 30/30 通过，未构建正式 sidecar/安装包 |
| 跨平台编译 | 授权 foundation Linux/amd64、Darwin/arm64；代理 foundation Darwin/amd64 编译通过，该平台 NewPlatform 仍返回 unsupported；交叉编译不是实机验证 |

## 明确未完成

CLI 租约接线/认证扩展/正常 stop 恢复阻断、真实用户全局代理与升级验收未完成。本机实际 Roaming ACL 不能满足新 scope 的权限规则，Resolver 拒绝；未修改目录权限来通过测试。Mac system proxy 未实现，macOS CLI 生命周期与 GUI 仍缺实机验收。

设备激活/credential/nonce/API、权限安装与真实 WG adapter、全局到期/重启调度、导入/campaign 与备份撤销合并未完成；Pending 仍全量读取，服务级设备/operation 配额和分页待实现。fake executor 的 done 不代表真实隧道失权，`tokens.yaml` 仍是生产授权事实源。

reverse mTLS/手机迁移、依赖扫描处置、签名/发行/升级渠道、生产观察与重新评分仍待后续阶段。没有缩短 campaign/观察窗口、没有勾 P1/P3/P4 全阶段或 Steelman 终验。
