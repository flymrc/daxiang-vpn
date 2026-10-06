# CLI 系统代理租约核心

> 2026-10-06 JST。本地 foundation：`clients/cli/internal/runtime/systemproxy` 状态机和 `shared/systemproxy` Windows adapter；尚未接入 CLI 命令、真实引擎控制 RPC、stop hook 或 GUI。当前 GUI 仍使用[恢复合同](client-runtime-safety-contract.md)中的 v1 journal，P3.1 未完成。

## 租约与授权边界

Manager 的 Acquire/Release/ReleaseOwned/Recover 使用同源 `contracts.EngineIdentity`，租约绑定 SID、规范化 home、完整引擎身份和随机 lease ID。Authority 必须在整个 callback 中持有真实引擎 phase/lifetime gate；“Inspect 成功再调用 callback”不能实现它。本切片的测试 Authority 只能证明状态机合同，不能冒充已接入真实控制面。

Acquire 要求认证 ready 实例、有效 loopback proxy 地址和 TCP 可达证据；不以配置端口认领全局代理。正常 release 匹配完整 owner 与显式 lease ID；用于 stop 的 ReleaseOwned 遇另一 home 租约是 no-op。Recover 要求匹配 home 的引擎已经停止并持有其空闲 lifetime 边界；不能让新实例默默收养旧实例的租约。

后续集成必须采用 home operation/真实引擎 phase gate→用户 proxy transaction 的锁顺序；CLI 不持 user proxy lock 等待引擎 stop。正常 stop 要先恢复自己的租约，恢复失败保留引擎与错误，不能先关闭数据面再伪报正常断开。崩溃恢复依赖 durable journal，不能承诺 os.Exit/机器断电时立刻恢复注册表。

## 固定用户 scope 与 WAL

Windows 使用实际 process token SID 和 KnownFolder RoamingAppData 的 `com.zongheng.vpn`，不从 APPDATA/LOCALAPPDATA/ZHVPN_HOME 为 journal 选择另一个目录。另一账户的提权进程、服务账户、impersonated thread、非本地固定磁盘、权限/owner 不匹配均拒绝；不会把提升后的另一个用户 HKCU 当原用户配置。

与旧 GUI 使用同名 `proxy-operation.lock` 和 `proxy-backup.json`。transaction 持禁止 DELETE 共享的目录/锁句柄，取得内核锁后重新核验 scope、owner/DACL 和 file ID，不删除锁文件来解锁。scope 目录不重写 DACL，避免影响其下其他对象。

v2 immutable WAL 先持久化原始字段的存在性、registry type 和 numeric byte array，再修改四个允许字段。v1 缺 SID/home/engine 归属，拒绝自动迁移；旧 GUI v1 reader 也拒绝 v2。部署前必须先通过旧 GUI 核验/恢复旧 journal，再切换新控制面，不静默填补身份或覆盖恢复材料。

读/写/notify/删除或部分恢复失败保留 WAL，外部修改返回冲突；notify 后再次整组读回，观察到变化时不能删除 WAL 并宣称恢复成功。v2 的各层字段名精确校验、大小写折叠判重与数组长度检查保证损坏/别名输入返回错误，不越界 panic。整组预检查、逐字段再次读写与 readback 缩小外部竞争窗口；Windows registry 不提供该方案的全局 CAS，不能保证所有第三方竞争都无损。无 journal 的释放不打开 OS adapter，也不关闭他人的代理。

## 平台与当前限制

测试仅用 fake adapter 或合成 HKCU 子键，禁用真实 WinINET 通知。真实 child/句柄证明跨进程锁、崩溃释放、immutable WAL 与路径替换保护；未改实际 Internet Settings。

当前机器真实 Roaming 目录有非白名单修改权限，production Resolver 返回拒绝；未为通过测试修改用户目录 ACL。这意味着该机器尚不能直接切换这套真实 CLI 全局代理。Mac 返回 unsupported；UID 锁不能直接证明机器/网络服务级 networksetup 的所有权，Mac adapter 与实机验证尚缺。

CLI 认证扩展协议、控制错误传播、停止阻断、GUI typed command 迁移、跨版本真实升级与原用户 scope 权限处置都是下一集成切片，不能把本核心勾为 P3.G 或正式发行。
