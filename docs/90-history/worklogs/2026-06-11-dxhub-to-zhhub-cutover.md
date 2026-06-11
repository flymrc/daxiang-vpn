# 2026-06-11 Hub API dxhub → zhhub 迁移收尾（大象 → 纵横）

## 背景

品牌改名（`bc8363a`）后仓库已是 Zongheng / 纵横，`zhreverse` 数据面也于 2026-06-11 切到生产，但 **Hub 授权 API 仍跑在旧 `dxhub`**：

- unit `dxhub.service`、目录 `/opt/daxiang-vpn/dxhub/`、env 全 `DXHUB_*`。
- 旧 reverse 残留 `dxreverse-hub.service`（disabled）、`/opt/daxiang/dxreverse`、`/etc/daxiang/dxreverse`。
- 旧 key `/root/.ssh/dxandroid_control*`。

这条 dx 残留直接导致换 IP 坏掉：dxhub.service 的 drop-in 设 `DXHUB_ANDROID_CONTROL_KEY`，但线上二进制实际只认 `ZHHUB_ANDROID_CONTROL_KEY`，回退到不存在的默认 `/root/.ssh/zhandroid_control`，rotate-ip 报 `control key unavailable` → 客户端 GUI「Hub 未能触发 Android 控制面换 IP」。

用户要求把 dxhub 全部改 zhhub、大象改纵横、所有链条打通。

## 代码改动（纯 zh，删 DX 兼容回退）

- `hub/main.go`:`envAny(["ZHHUB_TOKENS","DXHUB_TOKENS"])` / `envAny([..LISTEN..])` → `env("ZHHUB_TOKENS")` / `env("ZHHUB_LISTEN")`,删 `envAny`。
- `hub/internal/auth/server.go`:
  - 新增 `androidControlKeyPath()`:读 `ZHHUB_ANDROID_CONTROL_KEY`,默认 `/root/.ssh/zhandroid_control_hub`。
  - `currentAndroidCarrier`(bootstrap 动态运营商) 和 `triggerAndroidRotateIP`(换 IP) 两处统一用它,删 `firstNonEmptyEnv` 及其 `DXHUB_*` 回退。修正了原先两处 key 读取逻辑不一致(bootstrap 兼容 DX、rotate 不兼容)。
- `go vet ./hub/...`、`go test ./hub/...`:通过;`hub/` 下无 `DXHUB_/daxiang` 残留。

## 生产迁移（Hub 36.50.84.68）

1. 交叉编译 `dist/linux-amd64/zhhub`(GOOS=linux GOARCH=amd64 CGO_ENABLED=0)。
2. 部署到 `/opt/zongheng/zhhub/zhhub`(与 zhreverse 同根 `/opt/zongheng`,不用文档旧建议的 `/opt/zongheng-vpn`)。
3. 从 `/opt/daxiang-vpn/dxhub/tokens.yaml` 复制 tokens 到 `/opt/zongheng/zhhub/tokens.yaml`(mode 600,内容已是 zh 风格,不改)。
4. 新建 `/etc/systemd/system/zhhub.service`:env 全 `ZHHUB_*`,`ZHHUB_ANDROID_CONTROL_KEY=/root/.ssh/zhandroid_control_hub`,`WorkingDirectory=/opt/zongheng/zhhub`,监听 `0.0.0.0:18080`。
5. `daemon-reload` → `disable --now dxhub` → `enable --now zhhub`。

## dx 残留清理（归档式,可回滚）

全部 `mv` 到 Hub `/root/dx-attic-20260611/`(未 `rm`),`daemon-reload`:

- units:`dxhub.service`、`dxhub.service.d/`、`dxreverse-hub.service`。
- 目录:`/opt/daxiang`、`/opt/daxiang-vpn`、`/etc/daxiang`。
- keys:`/root/.ssh/dxandroid_control`、`dxandroid_control_hub`、`dxandroid_control_hub.pub`。

清理后 Hub：无 dx unit、无 dx 目录、无 dx key;`zhhub` + `zhreverse-hub` 均 active。

## 验证

- `zhhub` 监听 `18080`,`/healthz` → `{"status":"ok"}`。
- 启动后已有真实客户端 `bootstrap 通过`,服务无缝接管。
- **换 IP 端到端修复**:`POST /api/client/rotate-ip`(down 5s)→ `{"status":"triggered","egress":"jp-android-01"}`,日志 `rotate-ip 触发 ... egress=jp-android-01`。控制面 SSH key 链路打通。
- 整条代理链路(本机经 `127.0.0.1:7890`):
  - v4-only `api.ipify.org` → Hub VPS `36.50.84.68`(`v4_only_direct` 直拨)。
  - 双栈 `api64.ipify.org` → 手机 IPv6;rotate 后前缀从 `…421:d18c:…e654:1701` 变为 `…4d3:9083:…c122:bb01`,**换 IP 实际生效**。
  - skymark(v4-only 站)0.16s 打开。

## 回滚

- 旧二进制 / 配置 / unit / key 全在 `/root/dx-attic-20260611/`;恢复 unit + 目录 + `daemon-reload` + `enable --now dxhub` 即回旧路径。
- 仓库代码改动可 `git revert`。

## 残留边界（未在本次处理,待用户确认）

- GitHub remote 仍 `git@github.com:flymrc/daxiang-vpn.git`,本地工作目录仍 `daxiang-vpn`(go module 已 `zongheng-vpn`)。
- Mac mini 出口(当前预留🅿️、非生产数据面)配置仍 `~/.dxvpn`、`com.daxiang.dxvpn.*` plist;[docs/ops/SERVER_ACCESS.md](../../ops/SERVER_ACCESS.md) Mac 段仍 dx。
- 确认稳定后再 `rm -rf /root/dx-attic-20260611`。
