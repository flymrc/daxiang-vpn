# Hub 授权 API MVP

## 目标

第一版先解决一个问题：客户不能拿到 `dxvpn.exe` 后随便输入授权码就使用出口。

现在流程改为：

```text
dxvpn.exe login <授权码>
-> 请求 Hub 授权 API
-> Hub 校验 tokens.yaml
-> 校验通过后返回运行配置
-> 客户端只保存授权码
```

## 目录

```text
backend/dxhub
  cmd/dxhub
  internal/auth
  config/tokens.example.yaml

frontend/dxvpn
  cmd/dxvpn
  internal/bootstrap
  internal/app
  internal/proxy
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
  "token": "DX-JP-TEST-001"
}
```

授权失败返回：

```text
401 Unauthorized
```

授权成功返回客户端运行配置。

## Token 管理

真实授权文件：

```text
backend/dxhub/config/tokens.yaml
```

这个文件是服务端私有文件，不提交、不交付客户。

示例文件：

```text
backend/dxhub/config/tokens.example.yaml
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
授权成功只保存 token。
```

`start/status`：

```text
每次用本地 token 请求 Hub API。
Hub 禁用 token 后，客户端无法继续启动。
```

## 当前边界

- 这是最小可用授权版。
- 还没有后台管理页面。
- 还没有设备绑定。
- 还没有请求签名。
- 还没有自动创建 WireGuard Peer。
- 后续应改为短期会话配置和密钥轮换。
