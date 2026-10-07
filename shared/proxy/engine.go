package proxy

import (
	"context"
	"os"
	"os/signal"
	"sync"
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
	return RunDedicatedEngineWithBuildInfo(ctx, hooks, EngineBuildInfo{})
}

// RunDedicatedEngineWithBuildInfo is exclusively a hidden child-process
// entrypoint. It suppresses all raw standard output for the remainder of this
// process and records only fixed typed events. BuildID is public provenance;
// an empty ID explicitly means an unlabelled developer build.
func RunDedicatedEngineWithBuildInfo(ctx paths.Context, hooks RuntimeHooks, info EngineBuildInfo) error {
	finished := make(chan struct{})
	defer close(finished)
	capture, err := suppressDedicatedOutput()
	if err != nil {
		return safeEngineFailure(EngineLogCodeCaptureFailed)
	}
	ctx, err = canonicalContext(ctx)
	if err != nil {
		return safeEngineFailure(EngineLogCodeConfigRead)
	}
	log, err := OpenEngineLog(ctx, info)
	if err != nil {
		return safeEngineFailure(EngineLogCodeOpen)
	}
	recorder := newEngineLogRecorder(log)
	capture.startLog(recorder)
	var finalizeOnce sync.Once
	finalize := func() { finalizeOnce.Do(func() { capture.finish(recorder); recorder.close() }) }
	defer finalize()
	_ = recorder.append(EngineLogEvent{Event: EngineLogStarting})
	return runEngineWithLog(ctx, func() { os.Exit(1) }, hooks, recorder, finished, finalize)
}

func runEngine(ctx paths.Context, exitDedicated func()) error {
	return runEngineWithHooks(ctx, exitDedicated, nil)
}
func runEngineWithHooks(ctx paths.Context, exitDedicated func(), hooks RuntimeHooks) error {
	return runEngineWithLog(ctx, exitDedicated, hooks, nil, nil, nil)
}

func safeEngineFailure(code EngineLogCode) error {
	return &RuntimeError{Code: string(code), Message: "引擎运行失败；请查看安全日志与日志健康状态"}
}

func engineStageFailure(recorder *engineLogRecorder, code EngineLogCode, err error) error {
	if recorder == nil {
		return err
	}
	_ = recorder.append(EngineLogEvent{Event: EngineLogFailed, Code: code})
	return safeEngineFailure(code)
}

func runEngineWithLog(ctx paths.Context, exitDedicated func(), hooks RuntimeHooks, recorder *engineLogRecorder, finished <-chan struct{}, finalizeLogging func()) (result error) {
	if finished == nil {
		localFinished := make(chan struct{})
		defer close(localFinished)
		finished = localFinished
	}
	var log *EngineLog
	if recorder != nil {
		log = recorder.log
	}
	ctx, err := canonicalContext(ctx)
	if err != nil {
		return engineStageFailure(recorder, EngineLogCodeConfigRead, err)
	}
	content, err := readPrivateFile(ctx, ctx.SingBoxConfig)
	if err != nil {
		return engineStageFailure(recorder, EngineLogCodeConfigRead, err)
	}
	control, err := beginEngineControlWithHooks(ctx, content, hooks)
	if err != nil {
		return engineStageFailure(recorder, EngineLogCodeControlInit, err)
	}
	defer func() {
		if finalizeLogging != nil {
			finalizeLogging()
		}
		if recorder != nil && exitDedicated != nil {
			select {
			case <-recorder.done:
			default:
				select {
				case <-control.done:
					// The authorized stop is unfinished while this owned writer
					// holds files. Preserve the lifetime gate; the dedicated outer
					// 3s exit monitor covers this wait as well.
					<-recorder.done
				default:
				}
			}
		}
		control.close()
	}()
	control.setEngineRecorder(recorder)
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
		case <-finished:
		}
	}()
	if log != nil {
		_ = control.appendLog(EngineLogControlBound, "")
	}

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
		return engineStageFailure(recorder, EngineLogCodeConfigDecode, err)
	}
	boxOptions := box.Options{Context: boxCtx, Options: options}
	if log != nil {
		// User configuration must not re-enable sing-box's raw append file.
		// This changes only logging options. PlatformLogWriter is deliberately
		// absent: sing-box would otherwise enable Cache/Clash services. The
		// bounded process pipe categorizes its fixed WARN[/ERROR[ prefixes.
		if boxOptions.Options.Log == nil {
			boxOptions.Options.Log = &option.LogOptions{}
		}
		boxOptions.Options.Log.Output = "stderr"
		boxOptions.Options.Log.Disabled = false
		boxOptions.Options.Log.DisableColor = true
		boxOptions.Options.Log.Timestamp = false
		boxOptions.Options.Log.Level = "warn"
	}
	instance, err := box.New(boxOptions)
	if err != nil {
		return engineStageFailure(recorder, EngineLogCodeInitFailed, err)
	}
	if log != nil {
		_ = control.appendLog(EngineLogInitialized, "")
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return engineStageFailure(recorder, EngineLogCodeStartFailed, err)
	}
	defer func() {
		if err := instance.Close(); err != nil {
			if log != nil {
				result = engineStageFailure(recorder, EngineLogCodeCloseFailed, err)
			}
			return
		}
		if log != nil {
			_ = control.appendLog(EngineLogStopped, "")
		}
	}()
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
		_ = control.appendLog(EngineLogStopRequested, "")
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
