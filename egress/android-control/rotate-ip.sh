#!/system/bin/sh
# 安全切换出口公网 IP:通过飞行模式开关让蜂窝无线电向运营商重注册,通常会拿到新公网 IP。
# 不重启手机;隧道 IP(10.66.0.101)不变,切换后 WireGuard 自动重握手恢复。
#
# 关键:必须"脱离会话"执行——切换瞬间隧道会断,若在前台执行,你的 SSH 会在
# "开飞行"后当场断线、"关飞行"还没跑,手机就一直离线把你锁在外面。setsid 保证
# 即使你断线,本机仍会把飞行模式关回来。
#
# 用法(从隧道内,如本机/Hub):
#   ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101 'sh /data/adb/dxandroid/rotate-ip.sh'
#   ssh ... 'sh /data/adb/dxandroid/rotate-ip.sh 12'   # 自定义断网秒数(默认 8,越长换 IP 概率越高)
# 之后等 ~20-40s 重连,用 scripts/check-android-egress-health.ps1 或经 :1080 代理 curl 确认新 IP。
#
# 注意:运营商可能仍返回相同/粘性 IP,不保证每次都变;切换瞬间出口中断十几秒。

DOWN="${1:-8}"
LOG=/data/local/tmp/dxandroid-control.log

setsid sh -c "
  echo \"\$(date '+%F %T') rotate-ip: airplane ON (down=${DOWN}s)\" >> $LOG
  cmd connectivity airplane-mode enable
  sleep ${DOWN}
  cmd connectivity airplane-mode disable
  echo \"\$(date '+%F %T') rotate-ip: airplane OFF\" >> $LOG
" >/dev/null 2>&1 </dev/null &

echo "rotate-ip dispatched (down=${DOWN}s); 约 20-40s 后重连并核对出口 IP"
