# 2026-06-16 Android rotate-ip 并发保护

## 背景

多个客户端同时请求 Android 出口换 IP 时,Hub 会并发触发 `/data/adb/zhandroid/rotate-ip.sh`,导致飞行模式切换互相覆盖,出现竞态。

## 改动

- `hub/internal/auth`:给 `/api/client/rotate-ip` 增加按出口维度的非阻塞锁。
  - 第一个请求触发 Android 控制面。
  - 保护窗口内的后续请求返回 `409` + `status:"busy"`,不再执行第二次 SSH。
  - 保护窗口为 `down_seconds + ZHHUB_ROTATE_LOCK_EXTRA_SECONDS`,默认额外 45 秒。
  - 控制面触发失败时立即释放锁,允许下一次重试。
- `clients/cli/internal/bootstrap`:识别 Hub 的 `409 busy` 响应,把它作为正常结果返回给 CLI。
- `clients/cli/internal/app`:忙碌时 `rotate-ip --json` 输出 `ok:true,status:"busy",message:"..."`,不等待出口恢复。
- `clients/desktop-gui`:忙碌时展示 Hub 返回的信息,不再显示 `? -> ?`。
- `sdk/python`: `RotateResult` 增加 `status` / `message` 字段。
- `docs/20-operations/runbooks/hub-api-deploy.md`:记录 Hub 换 IP 锁和环境变量。

## 验证

- `go test ./hub/... ./clients/cli/...`:通过。
- `python -m unittest discover sdk/python/tests`:通过。
- `npm run check` in `clients/desktop-gui`:0 error,保留既有 `Cannot find type definition file for 'node'` warning。
