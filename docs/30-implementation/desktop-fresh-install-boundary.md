# Desktop 登录与全新安装边界

更新：2026-10-07。本页描述本地重构代码与合成验证，不描述生产迁移完成。

## 登录

- GUI 接受 CLI 非零退出的完整 `client_config_unavailable` v1 DTO，使首次启动和登出进入登录页；该 DTO 必须为 degraded、running=false、proxy_reachable=false，不能用于获取代理 lease。
- GUI 与 SDK 使用 `login --token-stdin --json`。GUI 写入有界 stdin 并关闭管道；不把授权码放进 argv。空、非单行或超过 4096 UTF-8 字节的值拒绝提交。登录结果观察超时表示结果未知，不自动重复提交。
- GUI 对成功和失败 JSON 的已知授权码回显及私有字段递归红化；未解析 stderr 和无效输出不向 UI 回显。旧 `zhvpn.lastToken` localStorage 项仅删除，不再读取或保存授权码。存储不可用不阻止登录。
- 本地 Rust 的真实 owned 子进程证明 stdin、EOF、argv 和输出红化；它不是打包 Tauri WebView→shell→生产 Hub 的端到端验收。
- Svelte 状态读取失败立即废弃旧 ready/出口 IP 快照，显示待确认并禁用未知状态连接；迟到的 IP 观察不能重新授予 ready。下一次完整成功状态只清临时 status 错误，保留未解决的代理恢复和 action 错误。Chrome+mock IPC 已覆盖首次登录、缓存退休、ready→拒绝→待确认→新 ready，以及迟到 IP 响应，未访问真实授权或生产。

## 安装器支持范围

当前仅支持**此前不存在的全新目录**，并要求父 namespace 可验证。已有产品卸载注册、已有目标目录（包括空目录）、无法检查的目标全部拒绝，退出码 12。自定义模板没有旧卸载器调用页，在 `.onInit` 就执行拒绝；不能依赖 PREINSTALL 才阻止旧卸载器。

自动升级、自动卸载、已有安装目录迁移目前不支持。新卸载器在 `un.onInit` 拒绝操作，保留安装文件与恢复记录。不得把全新安装合成测试写成签名发行、旧版本升级或卸载验收通过。既有旧卸载器不受新代码约束，不应主动运行。

全新目录的发布过程：

1. 固定本地卷、绝对路径；父目录及所有祖先不得有 reparse point。父目录 owner 必须是当前 token SID，父目录允许写入的 SID 限当前用户、SYSTEM、Administrators；祖先禁止其他 SID 对已有 namespace、DACL、owner 的变动。无法证明即拒绝，不修改已有目录 ACL。
2. 原生 no-delete parent handle 覆盖 staging 到发布。用 `CreateDirectoryW` 原子创建此前不存在的 GUID sibling，传入当前 SID+SYSTEM+Administrators 的显式受保护 DACL，创建后持有 no-delete stage handle。随机目录名不承担权限边界。
3. GUI、CLI、资源先全部解压进 stage，每次解压失败立即拒绝发布。编译时从实际 GUI/CLI 文件生成 SHA256；发布前重新检查私有 stage owner/DACL、普通非 reparse 文件及两份 SHA256。
4. 固定验证程序嵌入安装器，通过固定环境分段及短 bootstrap 执行，使用绝对系统 PowerShell 和系统模块路径；不执行可写临时目录中的验证脚本。环境参数只传路径和公开 hash。
5. 关闭 stage handle/cwd 后，仍持有可信 parent handle，执行一次不覆盖目标的目录 `Rename`。最终检查之后有人创建空目标，Rename 也必须拒绝；不分批替换、不安排重启替换、不按进程名结束 GUI/CLI。

信任边界为当前用户、SYSTEM、Administrators；不宣称防御同用户恶意代码或管理员。拒绝/异常会留下私有未发布 stage，用于只读调查；不会自动递归删除外部目录。真实用户 profile ACL 如果不满足要求，必须拒绝，不能为了通过安装测试放宽 ACL。本机历史 workspace 祖先带有其他 SID 的 Modify 权限，已观察到该路径被拒绝。

## 门禁与证据

`clients/desktop-gui/scripts/check-upgrade-guard.ps1` 已接入 `scripts/check-client-safety.ps1` Windows 门禁：

- 13 个 owned synthetic Windows 行为场景：全新完整发布、已有静态/运行/空目录、合成注册、缺 CLI、解压失败、CLI hash 改变、发布前目标竞争、最终 Rename 前空目标竞争、私有 DACL 与 parent/stage pin、外部 SID 可写 parent、junction parent。
- 实际 Tauri CLI 2.11.2 渲染完整模板并由 NSIS 编译，使用 synthetic owned child 作 payload；该完整模板产物不执行。
- package-lock 和已安装 CLI 必须为 2.11.2，嵌入验证程序必须与源脚本一致。CLI/template 漂移先拒绝，重新审查后才能改 pin。
- inner builder 调用统一 `check-steelman.ps1`，行为与安全扫描失败均停止；新 EvidenceDirectory 每次独立生成。构建开始记录完整 source manifest，成功清单前重新核对，dirty development 也拒绝构建中源文件改变；输出保存 manifest 文件及 SHA256 关系。

测试仅在新建 owned fixture 上设私有 ACL，所有进程均为测试创建并通过关闭 stdin 结束；不访问真实 HKCU/InternetSettings，不运行产品安装、旧卸载器、SSH 或部署。

仍未验收：真实签名证书/SmartScreen、真实新安装用户路径与 UAC/跨 SID、现有发行版本升级/自动卸载、真实 WebView/Tauri sidecar 登录、macOS、生产运行。实际开发包也不能作为这些事项的通过证据。
