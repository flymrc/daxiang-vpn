# 2026-07-13 APB 测试 token 与客户端验证

为 `akamai-personal-bypass` 的 ZH VPN 出口实验创建了一枚独立测试 token。完整 token、客户端配置和本地 WireGuard 私钥只保存在父工作区的 `.local/zongheng-vpn/`，不进入 Git、文档或命令输出。

## 变更

- 从当前启用的 `jp-android-01` 配置复制非敏感出口参数。
- 自动分配未占用的客户端 WireGuard 地址，不复用现有 token 地址。
- token 有效期设置为 `2027-07-13`。
- 更新生产 `tokens.yaml` 前在 Hub 保留权限为 root-only 的备份；配置采用同目录原子替换。
- 重启 `zhhub.service` 后确认本地 health endpoint 恢复正常。

## 验证

- `go test ./...`：通过。
- Python SDK `unittest`：5 项通过。
- Desktop GUI `npm run check`：0 error，保留一个既有的 `@types/node` warning。
- Hub admin web `npm run check`：0 error / 0 warning。
- 使用当前源码构建 Windows CLI，并以隔离的 `ZHVPN_HOME` 执行真实 `login --json`：通过。
- HTTPS bootstrap 返回完整出口与本地代理配置，Hub 成功应用客户端 WireGuard 公钥。
- 未执行 `start`，未修改测试机系统代理或启动 VPN 数据面。

## 回滚

如不再需要该测试 token，从生产 `tokens.yaml` 删除对应条目并重启 `zhhub.service`；如果配置异常，使用本次变更前创建的 Hub 备份恢复。
