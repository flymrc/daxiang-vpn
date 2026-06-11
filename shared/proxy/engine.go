package proxy

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	dnstransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/dns/transport/hosts"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/block"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/mixed"
	"github.com/sagernet/sing-box/protocol/wireguard"
	"github.com/sagernet/sing/common/json"

	"zongheng-vpn/shared/paths"
)

// RunEngine runs sing-box in-process and blocks until the process is
// terminated. It is invoked in a detached child process started by Start.
//
// Only the protocols this product actually uses are registered here. Keeping
// the registries minimal lets the Go linker's dead-code elimination drop the
// dozens of protocols (vmess/vless/trojan/shadowsocks/tor/...) that the full
// sing-box ships with, which is what keeps the binary small.
func RunEngine(ctx paths.Context) error {
	// Record our own PID so Stop can find us — works whether we were launched
	// normally or elevated (where the parent can't capture the child PID).
	_ = os.MkdirAll(filepath.Dir(ctx.PIDPath), 0700)
	if err := os.WriteFile(ctx.PIDPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return err
	}

	content, err := os.ReadFile(ctx.SingBoxConfig)
	if err != nil {
		return err
	}

	boxCtx := box.Context(
		context.Background(),
		inboundRegistry(),
		outboundRegistry(),
		endpointRegistry(),
		dnsTransportRegistry(),
		boxservice.NewRegistry(),
	)

	options, err := json.UnmarshalExtendedContext[option.Options](boxCtx, content)
	if err != nil {
		return err
	}

	instance, err := box.New(box.Options{Context: boxCtx, Options: options})
	if err != nil {
		return err
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return err
	}
	defer instance.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	return nil
}

func inboundRegistry() *inbound.Registry {
	registry := inbound.NewRegistry()
	mixed.RegisterInbound(registry)
	return registry
}

func outboundRegistry() *outbound.Registry {
	registry := outbound.NewRegistry()
	direct.RegisterOutbound(registry)
	block.RegisterOutbound(registry)
	http.RegisterOutbound(registry)
	return registry
}

func endpointRegistry() *endpoint.Registry {
	registry := endpoint.NewRegistry()
	wireguard.RegisterEndpoint(registry)
	return registry
}

func dnsTransportRegistry() *dns.TransportRegistry {
	registry := dns.NewTransportRegistry()
	dnstransport.RegisterTCP(registry)
	dnstransport.RegisterUDP(registry)
	hosts.RegisterTransport(registry)
	local.RegisterTransport(registry)
	return registry
}
