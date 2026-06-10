#!/system/bin/sh
# Interleaved IPv4/IPv6 upload A/B rounds for RST forensics.
CB=/data/local/tmp/cellbench
for r in 1 2 3; do
  echo "##### round $r hub80-v4 #####"
  AF=4 $CB up http://36.50.84.68/backend/empty.php 4 1
  echo "##### round $r cf443-v4 #####"
  PIN_IP=172.66.0.218 AF=4 $CB up https://speed.cloudflare.com/__up 4 1
  echo "##### round $r cf443-v6 #####"
  PIN_IP=2606:4700:7::da AF=6 $CB up https://speed.cloudflare.com/__up 4 1
done
