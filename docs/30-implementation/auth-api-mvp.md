# Hub 授权 API MVP

## 目标

第一版先解决一个问题：客户不能拿到 `zhvpn.exe` 后随便输入授权码就使用出口。

现在流程改为：

```text
zhvpn.exe login <授权码>
-> 请求 Hub 授权 API
-> Hub 校验 tokens.yaml
-> 客户端上报本地 WireGuard 公钥
-> Hub 把该公钥应用到 wg0 peer
-> 校验通过后返回运行配置
-> 客户端保存授权码和本地 WireGuard 私钥
```

## 目录

```text
hub
  main.go
  internal/auth
  config/tokens.example.yaml

clients/cli
  main.go
  internal/bootstrap
  internal/app

shared
  config
  paths
  proxy
```

## Hub API

### 健康检查

```text
GET /healthz
```

返回：

```json
{"status":"ok"}
```

### 客户端启动配置

```text
POST /api/client/bootstrap
```

请求：

```json
{
  "token": "ZH-JP-TEST-001",
  "wireguard_public_key": "CLIENT_WIREGUARD_PUBLIC_KEY"
}
```

授权失败返回：

```text
401 Unauthorized
```

授权成功返回客户端运行配置。新协议下响应不包含 `wireguard.private_key`;客户端使用本地
`ZHVPN_HOME/wireguard/client.key`。迁移期内,未上报 `wireguard_public_key` 的老客户端仍会收到
tokens 配置里的 legacy `wireguard.private_key`。

## Token 管理

真实授权文件：

```text
hub/config/tokens.yaml
```

这个文件是服务端私有文件，不提交、不交付客户。

示例文件：

```text
hub/config/tokens.example.yaml
```

禁用客户：

```yaml
enabled: false
```

## 客户端行为

`login`：

```text
必须请求 Hub API。
授权失败不保存 token。
首次生成本地 WireGuard 私钥并只上传公钥。
授权成功保存 token 和本地私钥;状态缓存不写 private_key。
```

`start/status`：

```text
start 每次用本地 token + 公钥请求 Hub API。
status 只读本地状态缓存,不把 bootstrap 当心跳。
Hub 禁用 token 后，客户端无法继续启动。
```

## 当前边界

- 这是最小可用授权版。
- 还没有后台管理页面。
- 还没有设备绑定。
- 还没有请求签名。
- Hub 会用 `wg set <iface> peer <public_key> allowed-ips <client_ip>/32` 应用 peer,但
  持久化清理旧 peer 仍是运维迁移事项。
- 后续应改为短期会话配置和密钥轮换。
