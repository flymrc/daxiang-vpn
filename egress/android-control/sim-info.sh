#!/system/bin/sh
# 一览当前 SIM / 蜂窝状态:换卡或排查慢速时跑一次,快速看清
#   "设置对不对" + "落在什么小区/信号质量如何" + "当前出口公网 IP"。
#
# 部署:/data/adb/dxandroid/sim-info.sh(root 运行)。
# 远程触发(Windows/隧道内):
#   ssh -i ~/.ssh/dxandroid_control -p 2022 root@10.66.0.101 'sh /data/adb/dxandroid/sim-info.sh'
# 或 ADB:  adb shell su -c 'sh /data/adb/dxandroid/sim-info.sh'

echo "===== 运营商 / 制式 ====="
echo "operator   : $(getprop gsm.operator.alpha)"
echo "net.type   : $(getprop gsm.network.type)"

echo "===== 网络模式(按卡槽,27=NR/LTE/...全制式) ====="
echo "slot1 mode : $(settings get global preferred_network_mode)"
echo "slot2 mode : $(settings get global preferred_network_mode1)"

echo "===== 省流 / 后台限制(应都为 off/null/disabled) ====="
echo "data_saver : $(settings get global data_saver_mode)"
echo "restrict-bg: $(cmd netpolicy get restrict-background 2>/dev/null)"

echo "===== 当前生效 APN ====="
content query --uri content://telephony/carriers/preferapn --projection name:apn:mcc:mnc:type 2>/dev/null \
  || echo "(读取 APN 失败:可能无 preferapn,Android 多为自动匹配)"

echo "===== 信号 / 频段 / 小区 / 漫游(serving + 邻区去重) ====="
dumpsys telephony.registry 2>/dev/null | grep -oE \
  "accessNetworkTechnology=[A-Za-z]+|mBands=\[[0-9]+\]|mEarfcn=[0-9]+|rsrp=-?[0-9]+ rsrq=-?[0-9]+ rssnr=-?[0-9]+|isUsingCarrierAggregation=[a-z]+|roamingType=[A-Z_]+|mPci=[0-9]+" \
  | sort -u | head -20
echo "提示:rssnr=2147483647 是无效值(邻区);看 serving 小区那条真实的 rssnr —— >10 好,1~3 差(拥塞/干扰)。"

echo "===== 当前出口公网 IP(手机直连蜂窝) ====="
curl -s -m 10 https://api.ipify.org; echo
