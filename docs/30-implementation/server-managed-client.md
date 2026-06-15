# 服务端管理客户端方案

> Deprecated note:本文早期示例里出现的 Mac `10.66.0.100:1080` / `mac-mini` 出口路线已于 2026-06-15 弃用。服务端托管的新客户端配置应默认指向 Android `zhreverse` / Hub 入口 `10.66.0.1:18081`。

## 核心原则

为了产品化和利益最大化，客户端不应该暴露底层网络信息。

客户不应该看到：

- Hub IP
- WireGuard 配置
- WireGuard 内网 IP
- 出口节点管理地址
- 远端代理地址
- `AllowedIPs`
- `sing-box`
- 路由规则
- 出口节点真实拓扑

客户只应该看到：

- 是否已连接
- 当前地区
- 当前出口类型
- 本地代理地址
- 当前出口 IP
- 流量/套餐/到期时间

理想体验：

```powershell
zhvpn.exe login <授权码>
zhvpn.exe start
zhvpn.exe status
```

输出：

```text
纵横 VPN 已连接
出口地区：日本
出口类型：住宅 IP
本地代理：127.0.0.1:7890
```

## 新架构

```text
zhvpn.exe
    |
    | 授权码 / token
    v
Hub 控制 API
    |
    | 返回运行配置
    v
客户端本地代理
    |
    | WireGuard / 远端代理 / 出口选择都由服务端下发
    v
日本出口节点
```

## 客户端配置

客户侧不再使用包含路由细节的 YAML。

### 推荐方式一：登录授权码

```powershell
zhvpn.exe login ZH-XXXX-XXXX
```

客户端只保存：

```yaml
account:
  token: <服务端签发的客户端 token>
```

### 推荐方式二：预生成授权文件

如果暂时没有登录系统，可以给客户一个极简授权文件：

```yaml
license:
  token: <客户授权 token>
```

这个文件不包含 Hub、出口节点、代理地址。

## 服务端下发运行配置

客户端启动时请求：

```text
GET /api/v1/client/bootstrap
Authorization: Bearer <token>
```

服务端返回：

```json
{
  "client": {
    "id": "cn-client-01",
    "status": "active"
  },
  "local_proxy": {
    "listen": "127.0.0.1",
    "port": 7890
  },
  "egress": {
    "display_name": "日本住宅 01",
    "region": "日本",
    "type": "residential"
  },
  "runtime": {
    "upstream_proxy": "10.66.0.100:1080",
    "hub_endpoint": "36.50.84.68:51820"
  }
}
```

注意：

- `runtime` 是服务端给客户端程序使用的运行参数。
- 客户端程序可以使用它，但不在界面上展示。
- 不把它写进客户可编辑配置文件。
- 同一个 token 默认只允许一个公网来源持续 bootstrap。Hub 记录 30 秒来源租约；不同公网来源在租约内复用同 token 会得到 `409 token_in_use`，客户端提示用户先断开另一台设备或等待约 30 秒后重试。

## 服务端职责

服务端需要管理：

- 客户账号。
- 客户 token。
- token 当前来源租约。
- 客户端 Peer。
- WireGuard IP 分配。
- 出口节点列表。
- 出口节点健康状态。
- 当前出口公网 IP。
- 客户到出口节点的分配关系。
- 运行配置下发。
- 流量统计和限速。
- 套餐到期和权限。

## 客户端职责

客户端只负责：

- 登录或导入 token。
- 请求服务端运行配置。
- 启动本地代理。
- 隐藏底层进程。
- 显示连接状态。
- 显示本地代理地址。
- 测试当前出口 IP。

客户端不负责：

- 选择具体 Hub。
- 选择真实出口主机地址。
- 暴露远端代理地址。
- 暴露 WireGuard 配置。
- 暴露路由细节。

## MVP 调整

之前的 MVP 配置：

```yaml
hub:
  endpoint: 36.50.84.68:51820

egress:
  management_addr: 100.80.36.89
  proxy_addr: 10.66.0.100:1080
```

这个方向要废弃，不作为客户配置。

新的 MVP 有两层配置：

### 客户可见配置

```yaml
license:
  token: ZH-DEV-TOKEN
```

### 服务端内部配置

```yaml
client:
  name: cn-client-01

hub:
  endpoint: 36.50.84.68:51820

egress:
  name: mac-mini
  region: 日本
  type: residential
  management_addr: 100.80.36.89
  proxy_addr: 10.66.0.100:1080
```

服务端内部配置可以明文维护，客户拿不到。

## 当前阶段落地建议

在服务端 API 还没完成之前，可以先做一个过渡方案：

1. `zhvpn.exe` 支持 `login <token>`。
2. 本地只保存 token。
3. 暂时把 bootstrap 运行配置内置在开发版本里，或从一个本地 admin 配置生成。
4. 后续补 Hub API 后，改成真正从服务端拉取。

过渡期也不要把 `proxy_addr`、`hub.endpoint` 暴露在客户配置里。

## 下一步开发顺序

1. 改 `zhvpn.exe`：客户配置只保留 token。
2. 增加 `zhvpn.exe login <token>`。
3. 增加 bootstrap 配置模型。
4. 临时从本地隐藏 runtime 文件读取运行配置。
5. 后续在 Hub 上实现 `/api/v1/client/bootstrap`。
6. 客户端改为向 Hub API 拉取运行配置。
