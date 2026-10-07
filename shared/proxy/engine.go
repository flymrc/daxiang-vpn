package proxy

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	return runEngineWithHooks(ctx, nil, nil)
}

// RunDedicatedEngine is only for the hidden CLI child-process entrypoint.
// A stuck start/close can self-exit after authenticated cancellation or lease
// expiry. General library/GUI callers of RunEngine are never terminated.
func RunDedicatedEngine(ctx paths.Context) error {
	return RunDedicatedEngineWithHooks(ctx, nil)
}

func RunDedicatedEngineWithHooks(ctx paths.Context, hooks RuntimeHooks) error {
	return runEngineWithHooks(ctx, func() { os.Exit(1) }, hooks)
}

func runEngine(ctx paths.Context, exitDedicated func()) error {
	return runEngineWithHooks(ctx, exitDedicated, nil)
}
func runEngineWithHooks(ctx paths.Context, exitDedicated func(), hooks RuntimeHooks) error {
	finished := make(chan struct{})
	defer close(finished)
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return err
	}
	content, err := readPrivateFile(ctx, ctx.SingBoxConfig)
	if err != nil {
		return err
	}
	control, err := beginEngineControlWithHooks(ctx, content, hooks)
	if err != nil {
		return err
	}
	defer control.close()
	engineCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-control.done:
			cancel()
			if exitDedicated != nil {
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				select {
				case <-finished:
				case <-timer.C:
					exitDedicated()
				}
			}
		case <-engineCtx.Done():
		}
	}()

	boxCtx := box.Context(
		engineCtx,
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
	control.ready()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	for {
		select {
		case <-signals:
			if err := control.requestStop(); err != nil {
				continue
			}
		case <-control.done:
		}
		return nil
	}
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
