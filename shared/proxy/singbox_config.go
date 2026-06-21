//go:build windows || darwin

package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
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
			Tag:  "zhvpn-wg",
			// system=false 走用户态 gVisor 网络栈（免管理员，默认）；
			// system=true 走系统 TUN（Windows --fast，需管理员）。macOS CLI
			// 当前只支持默认用户态路径。
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
			Tag:        "android-egress",
			Server:     host,
			ServerPort: port,
			Detour:     "zhvpn-wg",
		}},
		Route: singBoxRoute{Final: "android-egress"},
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
