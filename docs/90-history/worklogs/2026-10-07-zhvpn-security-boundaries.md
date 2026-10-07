# 2026-10-07 Steelman 第二波安全边界

状态：LOCAL_VERIFIED。工作在原隔离分支 `codex/zhvpn-steelman-runtime`，基于本地提交 `072bbc48d606cb08a58fa5eb7d144bb90ab9e282`。没有 push、生产命令、真实用户代理写入、安装升级或 Jetstar。

## 已完成源码切片

- legacy/trusted-client/Admin 三个实际 HTTP server 共用 body/header/read/write/idle 预算，修复兼容入口私网 XFF 来源伪造；四个写 JSON 入口严格完整读取，拒绝超限和尾随内容。旧外部执行总预算、并发/速率仍未闭合。
- Admin 同源 Go/TypeScript/sqlc 漂移门禁与配置逃逸 guard；安全扫描加入此前未覆盖的 Admin npm tree，显式包含所有依赖类别，真实 npm omission 负例防止环境变量隐藏 dev 漏洞。Admin 基线 16 项（13 high、3 moderate）经有界升级及 Tailwind 4 迁移处理；冻结v3实际扫描为0。
- 管理台不用 demo 冒充真实空/失败；完整运行 shape 校验、未知状态、secret generation、mutation 独立锁、权限失效和迟到刷新处理。未知 rotate 回执仍只有页内锁，未获得跨页 durable operation 合同。
- `shared/updateverify` 实现严格 signed metadata 与实际 artifact 纯验证；scope、时间、approved key ring、版本、序列、安全 floor、Previous receipt、大小/hash 全部绑定。输出仅 `verified_for_staging`，没有 Policy/Previous 持久提交、签名发行或安装链。
- 公共 Ed25519 身份输入增加长度、canonical Y 和 small-order 拒绝，Hub device identity 与更新 ring 同用 `shared/signingkey`。WireGuard X25519 规则保持各自算法边界；不是替换标准库密码验证。
- 新离线恢复规划器比较 backup/latest/checkpoint，保护读取身份并只查询精确字节的私有副本；阻止旧快照丢后续撤销/换钥/取消历史。`ready_to_restore=false`，外部最新事实与实际恢复没有完成。

## 独立反例与验证

私有证据根为机器本地 `.local/zongheng-vpn/steelman/2026-10-07/`，不提交原始日志、快照、二进制或浏览器报告。正式 Windows fixture 使用新建私有目录；没有修真实 profile ACL。Linux 为 WSL 受控 fixture，未访问生产 WG。

| 边界 | 有效证据 | 限制 |
| --- | --- | --- |
| HTTP 来源/body/socket | Windows race 与 vet；真实 loopback 短预算慢连接组 `-race -count=5` 通过 | 生产 Caddy/legacy wg/SSH 预算未验 |
| 更新 verifier | Windows race/vet、schema drift、独立 6 个 adversarial overlay 通过 | 纯函数，没有持久 watermark 或安装 |
| 公钥输入 | signingkey/updateverify/deviceauth focused Windows race 与独立复核通过 | 配置输入边界，不是生产迁移证明 |
| 恢复规划 | Windows Restore 15 + CLI 2 顶层测试 race；WSL 普通实际 SQLite/Unix 文件权限测试；独立原 4 反例与 OS 文件边界组通过 | WSL 无 C 编译器，Linux race 未执行；不是实际恢复 |
| 恢复独立发现 | 9999 年 UnixNano 溢出、硬链接/路径复开风险、不同非法 UTF-8 TEXT 的 JSON 比较碰撞、2 MiB cell 均修复并复验 | 最新 checkpoint 仍 caller asserted，不自证最新 |
| Admin consumer | Node runtime 19 个负例（含 canonical 可选字段兼容）；Svelte check 零错误/警告；锁定依赖 build | 尚无生产 API/会话证据 |
| Admin Chrome | 编译页面 10 项原回归 + 3 项迟到权限/刷新 owner/新 snapshot 恢复；显式 DEV preview 1 项 | 合成 API；预期 503/403 HTTP console error 保留，JavaScript exception 为 0 |
| CSS 升级 | Chrome login form/input/button 的几何、颜色、字体、边框基线对比一致，截图人工检查 | 本机 Chrome，未运行所有浏览器 |
| npm omission | 真实 registry fixture：继承 omit=dev 的 audit 隐藏 dev 漏洞，显式 includes 策略返回拒绝 | 合成依赖树，不执行 lifecycle |

浏览器原始结果分别 `admin-state-cases-settled.log`、`admin-authority-refresh-generation.log`、`admin-dev-preview-final.log`；独立发现的权限并发窗口已按 generation/refresh owner 修复，再运行完整原组防回归。早期 DEV server 持有升级前模块缓存导致 CSS import 失败，重启本任务自己的 Vite 后正向 DEV 验收通过，不把该失败归于产品已验证行为。

## 统一门禁与交付

冻结v3 `pwsh -NoProfile -File scripts/check-steelman.ps1 -EvidenceDirectory <新私有目录>` exit0，原始日志 `unified-final-frozen-v3.log`，扫描目录 `security-final-frozen-v3/`。完整 Go test/vet/race、实际 CLI↔Hub TLS、SDK33/Rust41、CLI builder9/SDK builder16、NSIS13与完整模板编译、Admin消费者19/两端Svelte check 全通过。6个Go OS/arch 均 symbol=0/package=0/module=1，仅现有OpenPGP module-tier例外；两棵npm均0；4个Rust target tree已审计，0 vulnerability、无未处置 warning，有效例外仍到2026-11-06。该 gate 不提供缺失的Linux race或实机/生产证据。

第二波本地提交为 `490e9fd5998ec49b3a107ad3fea86f9910e0fd9e`（70 files）。该确切提交和上一提交 `072bbc4` 的九目标干净开发构建均已完成：CLI Windows/Darwin amd64/arm64、Hub/helper Linux amd64、reverse Linux amd64/arm64、android-control Linux arm64。第二波构建日志 `build-490e9fd.log`、产物/清单 `development-490e9fd/`，exit0。清单有 full SHA、Go 1.26.7、clean source、artifact hash；重新核对9个产物hash，Windows CLI 实际 `version --json` 的协议2/合同1/SHA/clean身份匹配。所有产物 unsigned、`release_ready=false`、`compile_only`；不能外推 Mac/Android/Linux 运行验收。后续文档提交不改变这些产物对应的源码提交。

原工作区保护清册79个既有dirty文件按上一份最终hash重新核对，无本轮变化；私有原始证据和已启动的本任务Chrome/Vite已保留/按归属关闭。

## 下一批依赖

可本地继续的源码缺口包括 legacy 子进程预算、Mac OS adapter、v2 credential 到实际代理 bootstrap、campaign/installation lineage 和阻断投影、受控旧配置导入、最新撤销 head 与实际恢复、更新 watermark/安装维护协议、日志容量与性能指标。真实 WinINET/双会话/UAC/已有安装升级、Mac 签名公证/代理、真实 WG 撤权和手机 TLS canary 需要各自实机证据。当前生产授权未接管，30 天窗口未开始，不给总体完成或 ≥85 分的结论。

对应合同：[HTTP/Admin](../../30-implementation/steelman-http-admin-boundaries.md)、[离线恢复](../../30-implementation/device-authority-offline-restore-plan.md)、[可信更新](../../30-implementation/trusted-update-metadata-verifier.md)。
