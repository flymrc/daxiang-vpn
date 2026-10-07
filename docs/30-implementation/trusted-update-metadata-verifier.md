# 可信更新元数据：纯 staging 验证边界

> 2026-10-07 JST，LOCAL_IMPLEMENTED。对应 Steelman P7.6 的离线验证切片；尚未接入安装、SDK、GUI、发布签名或生产更新渠道。

`shared/updateverify.Verify` 接收候选 envelope、调用者持有的 artifact reader、可信 Policy/Previous Receipt 和时钟，验证后只返回 `verified_for_staging`。它不打开文件路径、不联网、不安装、不修改当前版本、授权 DB 或 WireGuard，也不自行保存防重放状态。

## 合同与唯一维护源

`Metadata` 的 Go 字段、次序和 JSON tag 是签名正文的维护源。`Schema()` 确定性导出 [metadata-v1.schema.json](../../shared/updateverify/metadata-v1.schema.json)，生成器不参与运行授权：

```powershell
go run ./shared/updateverify/cmd/schemagen -check
go test -race ./shared/updateverify/...
go vet ./shared/updateverify/...
```

envelope 必须恰好包含 `schema_version`、`key_id`、`metadata`、`signature`。所有字段必填、大小写固定、不能为 null；重复字段（含转义后重复）、未知字段、尾随数据、数组替代对象均拒绝。正文必须完全等于 `CanonicalMetadata` 的紧凑 UTF-8 编码，字段顺序固定；额外空白、数字指数/小数、字段重排、转义别名不能被重新解释成另一份签名正文。

每份正文只描述一个产物，绑定：

| 绑定项 | 规则 |
| --- | --- |
| product / platform / architecture / channel | 必须等于可信 Policy 的四项 Scope；不能跨产品、OS、CPU 或渠道使用 |
| version / protocol | 有界严格 SemVer 与 Policy 协议范围；stable 拒绝 prerelease |
| source_commit | 完整 40 位小写 SHA；这是受信任签名者的来源声明，验证器不证明二进制由该源码构建 |
| artifact_name / size / SHA-256 | 单一 ASCII 文件名、实际字节数和 hash；名字不授予路径/执行权限 |
| issued_at / expires_at | 秒级 UTC Unix 时间；已生效、未到期、有效期受 Policy 约束 |
| release_sequence / security_version / security_floor | 发布顺序与安全下限分别校验，不能以高发布序列掩盖安全退化 |

正文最多 16 KiB，envelope 最多 24 KiB；产物最多 8 GiB且仍受更小的 Policy 限制。数字计数不超过 JSON 安全整数 `9007199254740991`，协议为 `1..1024`，元数据有效期最多 30 天。SemVer 总长最多 128 字节，major/minor/patch 不超过 uint32；prerelease/build 每项最多 64 字节、最多 8 项。numeric prerelease 按长度和 ASCII 比较，不解析成可能溢出的机器整数；build metadata 不参与优先级。JSON Schema 不表达签名、字段间比较、canonical bytes 或防重放语义，单独通过 schema 不产生 staging 资格。

## 签名与密钥批准

签名消息唯一为以下字节串，长度为字节数、网络字节序：

```text
UTF8("zhvpn/update-metadata/v1") || NUL
|| uint32BE(len(key_id)) || UTF8(key_id)
|| uint32BE(len(canonical_metadata)) || canonical_metadata
```

`SigningMessage` 只生成公有待签字节，不持有私钥或发布产物。签名为 Ed25519 的 64 字节结果，envelope 以 128 位小写 hex 编码。

Policy 的 pinned ring 必须由可信调用者提供，不能从包内公钥或 candidate 配置建立信任。ring 最多 16 个 key；ID、public key 均不可重复，每个 key 有批准的有效窗和发布序列段。候选签发时间及当前验签时间都须落在 `[ValidFrom,ValidUntil)`，序列须落在含端点的批准范围；key 窗口最多 5 年。删除/过期 key 会使旧包再次验证失败。换 key 需要独立批准 Policy，不允许 metadata 自授新 key；使用新 key 必须发布新的序列。

ring 还拒绝 small-order 公钥和非 canonical Y 编码。Go 1.26.7 的原始验证器会接受 identity public key 与 `R=identity,S=0` 的 trivial signature，负例已锁定此配置错误。检查只使用公开点编码，不另写曲线算法；坐标依据 [libsodium 官方 ref10 原始实现](https://github.com/jedisct1/libsodium/blob/1.0.18/src/libsodium/crypto_core/ed25519/ref10/ed25519_ref10.c#L966)。其它签名/曲线有效性仍由 Go 标准库验证。

## 发布、幂等与安全下限

首次验证可传 nil Previous，但 Policy 的初始最低序列、版本和安全 floor 必须来自可信配置。已登记后不得丢弃 Previous；纯函数无法发现调用者删除了历史状态。

Previous 必须是同一 Scope 下受保护保存的最近 staging receipt。验证器检查其 schema、状态、正文 digest、时间与字段一致性，拒绝其它包的 receipt，不能把网络输入或自行改写的记录当作可信 Previous。Previous 的原 key 即使已退役，其 watermark 仍须保留；当前候选再按最新 ring 校验。

- 新候选序列必须大于 Previous；版本优先级、security_version 和 security_floor 都不下降。
- 候选不得低于 Policy 最低版本/序列；security_floor 不得低于 Policy floor，也不得超过自身 security_version。
- 相同序列只允许完全相同 canonical metadata digest 和 key ID，返回 `Idempotent=true`；包大小/hash已绑定在正文。任何重新签发 key、来源或正文差异都视为 equivocation。
- 幂等仍重新检查当前 key、有效期、Policy、floor，并重新读取实际产物；缓存 receipt 不能绕过 key 撤销、到期或提升的安全下限。
- 当前边界不提供旧版本回退例外。未来受控恢复必须有单独的合同和批准，不能重置序列/Previous 或降低 floor。

调用者要在协调锁下原子保存 receipt 与新的 watermark/floor；并发审批、安装成功/失败、机器重启后的持久防重放仍属后续集成。`VerifiedAt` 表示本次校验时间，不表示安装、恢复或业务健康。

## 产物读取与证据边界

仅在所有元数据、签名、Previous 与策略检查通过后读 artifact，最多读取声明 size + 1 字节，流式比对实际长度和 SHA-256。失败不返回 staging receipt，不输出文件内容或 reader 原始错误文本。验证器在读取前后检查 Context；已经阻塞的 `Read` 必须由调用者提供超时/关闭语义，不创建潜在泄漏的后台 goroutine。

本片覆盖 canonical/schema 漂移、跨包/平台/渠道/协议、过期/未来/超 TTL、key 窗口/撤销/换发、scope 不同的旧 receipt、重放/equivocation、版本及安全 floor 回退、strict JSON、SemVer 溢出/prerelease 排序、实际 size/hash/读失败/取消、弱公钥配置及并发验证。

- [x] 纯验签、策略、严格正文和实际产物检查；schema 漂移测试；Windows race/vet。
- [ ] 可信 Policy 与 Previous 的实际受保护持久化、跨进程 watermark 提交。
- [ ] 发布签名身份、Windows Authenticode、macOS 签名/公证与批准密钥管理。
- [ ] staging 到安装的原子接线、已有安装升级、分批、故障恢复及真实健康检查。
- [ ] 实际更新服务、平台实机、生产上线与观察验收。

因此本切片不等于完整可信更新链，不改变当前安装器只支持全新目标的限制，也不使未签名开发构建成为正式发行物。
