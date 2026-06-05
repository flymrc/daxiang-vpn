//go:build windows

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"daxiang-vpn/frontend/dxvpn/internal/config"
	"daxiang-vpn/frontend/dxvpn/internal/paths"
)

// EngineCommand is the hidden subcommand that runs sing-box in-process. Start
// re-executes this binary with this argument as a detached background process.
const EngineCommand = "__engine"

// KillCommand is the hidden subcommand that terminates a PID. It exists so the
// elevated kill path (for --fast engines) shows our app name in the UAC prompt
// rather than taskkill.
const KillCommand = "__killpid"

// HomeFlag passes the resolved app root to the engine child, so an elevated
// engine reads the same config/PID paths even if UAC ran under another account.
const HomeFlag = "--home"

// Windows process creation flags used to launch the engine without a console
// window and detached from this CLI process group.
const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

type singBoxConfig struct {
	Log       singBoxLog        `json:"log"`
	Endpoints []singBoxEndpoint `json:"endpoints,omitempty"`
	Inbounds  []singBoxInbound  `json:"inbounds"`
	Outbounds []singBoxOutbound `json:"outbounds"`
	Route     singBoxRoute      `json:"route"`
}

type singBoxLog struct {
	Level string `json:"level"`
}

type singBoxInbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type singBoxEndpoint struct {
	Type       string          `json:"type"`
	Tag        string          `json:"tag"`
	System     bool            `json:"system"`
	MTU        int             `json:"mtu,omitempty"`
	Workers    int             `json:"workers,omitempty"`
	Address    []string        `json:"address"`
	PrivateKey string          `json:"private_key"`
	Peers      []singBoxWGPeer `json:"peers"`
}

type singBoxWGPeer struct {
	Address                     string   `json:"address"`
	Port                        int      `json:"port"`
	PublicKey                   string   `json:"public_key"`
	AllowedIPs                  []string `json:"allowed_ips"`
	PersistentKeepaliveInterval int      `json:"persistent_keepalive_interval"`
}

type singBoxOutbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Detour     string `json:"detour,omitempty"`
}

type singBoxRoute struct {
	Final string `json:"final"`
}

func WriteSingBoxConfig(ctx paths.Context, cfg config.Config, systemTUN bool) error {
	host, portText, err := splitAddr(cfg.Egress.ProxyAddr)
	if err != nil {
		return err
	}
	port, _ := strconv.Atoi(portText)
	hubHost, hubPortText, err := splitAddr(cfg.Hub.Endpoint)
	if err != nil {
		return err
	}
	hubPort, _ := strconv.Atoi(hubPortText)
	sb := singBoxConfig{
		Log: singBoxLog{Level: "error"},
		Endpoints: []singBoxEndpoint{{
			Type: "wireguard",
			Tag:  "dxvpn-wg",
			// system=false 走用户态 gVisor 网络栈（免管理员，默认）；
			// system=true 走系统 wintun TUN（--fast，需管理员，性能更高、延迟更低）。
			System: systemTUN,
			// 用户态 gVisor WireGuard 在高延迟链路上对 MTU 敏感：中国移动等
			// 网络实际 MTU 常低于 1420，大包在 client→Hub 段被丢会导致上传塌陷。
			// 取 1280（IPv6 最小 MTU）最保守，杜绝分片丢包。
			MTU: 1280,
			// 并行加密 worker，缓解用户态 WireGuard 的 CPU 瓶颈。
			Workers:    runtime.NumCPU(),
			Address:    []string{cfg.WireGuard.Address},
			PrivateKey: cfg.WireGuard.PrivateKey,
			Peers: []singBoxWGPeer{{
				Address:                     hubHost,
				Port:                        hubPort,
				PublicKey:                   cfg.Hub.PublicKey,
				AllowedIPs:                  []string{hiddenString([]byte{0x6b, 0x6a, 0x74, 0x6c, 0x6c, 0x74, 0x6a, 0x74, 0x6a, 0x75, 0x68, 0x6e})},
				PersistentKeepaliveInterval: 25,
			}},
		}},
		Inbounds: []singBoxInbound{{
			Type:       "mixed",
			Tag:        "local-mixed-in",
			Listen:     cfg.LocalProxy.ListenAddr,
			ListenPort: cfg.LocalProxy.ListenPort,
		}},
		Outbounds: []singBoxOutbound{{
			Type:       "http",
			Tag:        "mac-egress",
			Server:     host,
			ServerPort: port,
			Detour:     "dxvpn-wg",
		}},
		Route: singBoxRoute{Final: "mac-egress"},
	}
	data, err := json.MarshalIndent(sb, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(ctx.SingBoxConfig, data, 0600)
}

func splitAddr(addr string) (string, string, error) {
	idx := strings.LastIndex(addr, ":")
	if idx <= 0 || idx == len(addr)-1 {
		return "", "", fmt.Errorf("代理地址格式错误：%s", addr)
	}
	return addr[:idx], addr[idx+1:], nil
}

func hiddenString(data []byte) string {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ 0x5a
	}
	return string(out)
}

// Start launches the in-process sing-box engine in a detached background child
// process (this same binary re-executed with EngineCommand). The engine writes
// its own PID file. When fast is true the engine needs administrator rights to
// create the system TUN, so it is launched elevated via UAC.
func Start(ctx paths.Context, cfg config.Config, fast bool) error {
	_ = cfg
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	if fast {
		// System TUN requires admin; elevate the engine (UAC prompt). The
		// resolved root is passed so the elevated process reads the same paths.
		return runElevated(exe, fmt.Sprintf(`%s %s "%s"`, EngineCommand, HomeFlag, ctx.Root), false)
	}

	cmd := exec.Command(exe, EngineCommand, HomeFlag, ctx.Root)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach: the engine writes its own PID and keeps running after we exit.
	_ = cmd.Process.Release()
	return nil
}

func Stop(ctx paths.Context) (bool, error) {
	pid, err := readPID(ctx.PIDPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !pidRunning(pid) {
		_ = os.Remove(ctx.PIDPath)
		return false, nil
	}
	// Normal kill — works for the default (non-elevated) gVisor engine.
	_ = KillPID(pid)
	if pidRunning(pid) {
		// Still alive: likely an elevated --fast engine. Retry the kill with
		// elevation via our own __killpid (so UAC shows the app name).
		if exe, e := os.Executable(); e == nil {
			_ = runElevated(exe, fmt.Sprintf("%s %d", KillCommand, pid), true)
		}
	}
	if pidRunning(pid) {
		return false, errors.New("停止失败，请重试")
	}
	_ = os.Remove(ctx.PIDPath)
	return true, nil
}

// KillPID force-terminates a process tree by PID. Invoked directly by Stop and,
// elevated, via the KillCommand subcommand.
func KillPID(pid int) error {
	taskkill := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	return exec.Command(taskkill, "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

func IsRunning(ctx paths.Context) (bool, int) {
	pid, err := readPID(ctx.PIDPath)
	if err != nil {
		return false, 0
	}
	if !pidRunning(pid) {
		return false, pid
	}
	return true, pid
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
