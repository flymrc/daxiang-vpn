#!/bin/sh
# Local-only acceptance. Tests mutate only their owned user/network namespaces.
# Run with an explicit trusted Go toolchain on Linux with user namespaces and
# WireGuard available; a missing capability is a failed gate, never a skip.
set -eu
cd "$(dirname "$0")/.."
for required in go unshare ip wg; do
    command -v "$required" >/dev/null
done
go run ./shared/proxygate/cmd/schemagen -check
go test ./shared/proxygate -count=1
go test -tags with_gvisor ./egress/reverse -run '^TestProxyGate' -count=1
go test -v -tags integration,with_gvisor ./hub/internal/deviceapi -run '^Test(ProxyGate|ProxyBarrier|.*Service.*)' -count=1
go test ./hub/internal/deviceauth -run '^TestProxyConvergence' -count=1
go vet -tags integration,with_gvisor ./shared/proxygate/... ./egress/reverse ./hub/internal/deviceauth ./hub/internal/deviceapi
printf '%s\n' 'Local Linux proxy barrier gate passed; this is not production or all-WireGuard traffic acceptance.'
