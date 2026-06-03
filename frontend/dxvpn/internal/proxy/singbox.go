package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

func WriteSingBoxConfig(ctx paths.Context, cfg config.Config) error {
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
			Type:    "wireguard",
			Tag:     "dxvpn-wg",
			System:  false,
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

// Start launches the in-process sing-box engine in a detached background
// child process (this same binary re-executed with EngineCommand) and records
// its PID so Stop can terminate it later.
func Start(ctx paths.Context, cfg config.Config) error {
	_ = cfg
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, EngineCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(ctx.PIDPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	// Detach: the engine must keep running after this CLI process exits.
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
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	if err := cmd.Run(); err != nil {
		return false, err
	}
	_ = os.Remove(ctx.PIDPath)
	return true, nil
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
