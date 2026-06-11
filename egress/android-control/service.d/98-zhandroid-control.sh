#!/system/bin/sh
# Magisk service.d 启动项:开机(late_start)后拉起 zhandroid-control 看门狗。
# 部署目标:/data/adb/service.d/98-zhandroid-control.sh(需可执行)。
#
# 编号 98 早于 99-zhreverse-egress.sh,但 watchdog 自己会等隧道就绪、
# 并负责把 egress 拉起,二者先后无强依赖。

BASE=/data/adb/zhandroid
WATCHDOG=$BASE/watchdog.sh
LOG=/data/local/tmp/zhandroid-control.log

# 等系统起来一点,避免开机早期 ip/pgrep 尚不可用。
sleep 20

if [ -x "$WATCHDOG" ]; then
    echo "$(date '+%F %T') service.d: launching watchdog" >> "$LOG"
    sh "$WATCHDOG" &
else
    echo "$(date '+%F %T') service.d: $WATCHDOG missing or not executable" >> "$LOG"
fi
