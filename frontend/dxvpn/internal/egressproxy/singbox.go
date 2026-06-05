package egressproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"daxiang-vpn/frontend/dxvpn/internal/egressconfig"
	"daxiang-vpn/frontend/dxvpn/internal/paths"
)

type singBoxConfig struct {
	Log       singBoxLog        `json:"log"`
	DNS       singBoxDNS        `json:"dns"`
	Endpoints []singBoxEndpoint `json:"endpoints,omitempty"`
	Inbounds  []singBoxInbound  `json:"inbounds"`
	Outbounds []singBoxOutbound `json:"outbounds"`
	Route     singBoxRoute      `json:"route"`
}

type singBoxLog struct {
	Level string `json:"level"`
}

type singBoxDNS struct {
	Servers  []singBoxDNSServer `json:"servers"`
	Strategy string             `json:"strategy,omitempty"`
}

type singBoxDNSServer struct {
	Address string `json:"address"`
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
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type singBoxRoute struct {
	Final string `json:"final"`
}

func WriteConfig(ctx paths.Context, cfg egressconfig.Config) error {
	listenAddr, err := cfg.ProxyListenAddr()
	if err != nil {
		return err
	}
	hubHost, hubPortText, err := splitAddr(cfg.Hub.Endpoint)
	if err != nil {
		return err
	}
	hubPort, _ := strconv.Atoi(hubPortText)

	sb := singBoxConfig{
		Log: singBoxLog{Level: "info"},
		DNS: singBoxDNS{
			Servers: []singBoxDNSServer{
				{Address: "1.1.1.1"},
				{Address: "8.8.8.8"},
			},
			Strategy: "prefer_ipv4",
		},
		Endpoints: []singBoxEndpoint{{
			Type:       "wireguard",
			Tag:        "dxvpn-wg",
			System:     cfg.WireGuard.SystemTun(),
			MTU:        1280,
			Address:    []string{cfg.WireGuard.Address},
			PrivateKey: cfg.WireGuard.PrivateKey,
			Peers: []singBoxWGPeer{{
				Address:                     hubHost,
				Port:                        hubPort,
				PublicKey:                   cfg.Hub.PublicKey,
				AllowedIPs:                  []string{"10.66.0.0/24"},
				PersistentKeepaliveInterval: 25,
			}},
		}},
		Inbounds: []singBoxInbound{{
			Type:       "mixed",
			Tag:        "egress-proxy-in",
			Listen:     listenAddr,
			ListenPort: cfg.Proxy.ListenPort,
		}},
		Outbounds: []singBoxOutbound{{
			Type: "direct",
			Tag:  "cellular-direct",
		}},
		Route: singBoxRoute{Final: "cellular-direct"},
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
		return "", "", fmt.Errorf("地址格式错误：%s", addr)
	}
	return addr[:idx], addr[idx+1:], nil
}
