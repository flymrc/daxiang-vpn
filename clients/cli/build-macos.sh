#!/usr/bin/env bash
# 构建纵横 VPN macOS CLI 发布包。
#
# 与 Windows CLI 一样，sing-box 以代码库形式编译进 zhvpn；默认使用
# 用户态 WireGuard/gVisor，不需要管理员权限或系统 TUN。

set -euo pipefail
cd "$(dirname "$0")"

repo_root="$(cd ../.. && pwd)"
tags="with_gvisor"
ldflags="-s -w"

targets=(
  "arm64:macos-arm64"
  "amd64:macos-amd64"
)

for target in "${targets[@]}"; do
  arch="${target%%:*}"
  dir="${target#*:}"
  out_dir="$repo_root/dist/$dir"
  out="$out_dir/zhvpn"
  mkdir -p "$out_dir"

  echo "构建 macOS $arch -> $out"
  GOOS=darwin GOARCH="$arch" go build -tags "$tags" -trimpath -ldflags "$ldflags" -o "$out" .
  chmod 755 "$out"
  size_bytes=$(wc -c < "$out" | tr -d ' ')
  echo "  完成：$size_bytes bytes"
done

echo "全部构建完成。"
