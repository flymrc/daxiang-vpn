package egressconfig

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Node      NodeConfig      `json:"node" yaml:"node"`
	Hub       HubConfig       `json:"hub" yaml:"hub"`
	WireGuard WireGuardConfig `json:"wireguard" yaml:"wireguard"`
	Proxy     ProxyConfig     `json:"proxy" yaml:"proxy"`
}

type NodeConfig struct {
	Name string `json:"name" yaml:"name"`
}

type HubConfig struct {
	Endpoint  string `json:"endpoint" yaml:"endpoint"`
	PublicKey string `json:"public_key" yaml:"public_key"`
}

type WireGuardConfig struct {
	Address    string `json:"address" yaml:"address"`
	PrivateKey string `json:"private_key" yaml:"private_key"`
	// System 决定 sing-box 用系统 WireGuard（真实 tun 接口，需 root）还是用户态 gVisor 栈。
	// egress 节点要在 WireGuard 内网地址上暴露代理端口，必须用系统接口，因此默认 true。
	System *bool `json:"system,omitempty" yaml:"system,omitempty"`
}

// SystemTun 返回是否使用系统 WireGuard 接口，未配置时默认 true。
func (w WireGuardConfig) SystemTun() bool {
	if w.System == nil {
		return true
	}
	return *w.System
}

type ProxyConfig struct {
	ListenAddr string `json:"listen_addr,omitempty" yaml:"listen_addr,omitempty"`
	ListenPort int    `json:"listen_port,omitempty" yaml:"listen_port,omitempty"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func (c *Config) ApplyDefaults() {
	if c.Proxy.ListenPort == 0 {
		c.Proxy.ListenPort = 1080
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Node.Name) == "" {
		return errors.New("node.name 不能为空")
	}
	if err := validateHostPort("hub.endpoint", c.Hub.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(c.Hub.PublicKey) == "" {
		return errors.New("hub.public_key 不能为空")
	}
	ip, err := c.WireGuard.AddressIP()
	if err != nil {
		return err
	}
	if ip == nil {
		return errors.New("wireguard.address 必须包含有效 IP")
	}
	if strings.TrimSpace(c.WireGuard.PrivateKey) == "" {
		return errors.New("wireguard.private_key 不能为空")
	}
	if c.Proxy.ListenPort < 1 || c.Proxy.ListenPort > 65535 {
		return errors.New("proxy.listen_port 必须在 1-65535 之间")
	}
	if c.Proxy.ListenAddr != "" && net.ParseIP(c.Proxy.ListenAddr) == nil {
		return errors.New("proxy.listen_addr 不是有效 IP")
	}
	return nil
}

func (c Config) ProxyListenAddr() (string, error) {
	if strings.TrimSpace(c.Proxy.ListenAddr) != "" {
		return c.Proxy.ListenAddr, nil
	}
	ip, err := c.WireGuard.AddressIP()
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}

func (c Config) ProxyAddr() (string, error) {
	addr, err := c.ProxyListenAddr()
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(addr, strconv.Itoa(c.Proxy.ListenPort)), nil
}

// SubnetCIDR 返回 wireguard.address 所在子网，例如 10.66.0.101/24 -> 10.66.0.0/24。
// 用于在系统 WG 模式下添加策略路由，确保回 Hub 的流量走 wg 接口。
func (w WireGuardConfig) SubnetCIDR() (string, error) {
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(w.Address))
	if err != nil {
		return "", fmt.Errorf("wireguard.address 格式错误，应类似 10.66.0.101/24")
	}
	return ipnet.String(), nil
}

func (w WireGuardConfig) AddressIP() (net.IP, error) {
	ip, _, err := net.ParseCIDR(strings.TrimSpace(w.Address))
	if err != nil {
		return nil, fmt.Errorf("wireguard.address 格式错误，应类似 10.66.0.100/32")
	}
	return ip, nil
}

func validateHostPort(field, value string) error {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s 格式错误，应类似 host:port", field)
	}
	if host == "" {
		return fmt.Errorf("%s 主机不能为空", field)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s 端口无效", field)
	}
	return nil
}
