package config

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
	License    LicenseConfig    `json:"license,omitempty" yaml:"license,omitempty"`
	Client     ClientConfig     `json:"client,omitempty" yaml:"client,omitempty"`
	Hub        HubConfig        `json:"hub,omitempty" yaml:"hub,omitempty"`
	Egress     EgressConfig     `json:"egress,omitempty" yaml:"egress,omitempty"`
	LocalProxy LocalProxyConfig `json:"local_proxy,omitempty" yaml:"local_proxy,omitempty"`
	WireGuard  WireGuardConfig  `json:"wireguard,omitempty" yaml:"wireguard,omitempty"`
	Runtime    RuntimeConfig    `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

type LicenseConfig struct {
	Token string `json:"token" yaml:"token"`
}

type ClientConfig struct {
	Name string `json:"name" yaml:"name"`
}

type HubConfig struct {
	Endpoint  string `json:"endpoint" yaml:"endpoint"`
	PublicKey string `json:"public_key,omitempty" yaml:"public_key,omitempty"`
}

type EgressConfig struct {
	Name           string `json:"name" yaml:"name"`
	DisplayName    string `json:"display_name" yaml:"display_name"`
	Region         string `json:"region" yaml:"region"`
	Type           string `json:"type" yaml:"type"`
	ManagementAddr string `json:"management_addr" yaml:"management_addr"`
	ProxyAddr      string `json:"proxy_addr" yaml:"proxy_addr"`
}

type LocalProxyConfig struct {
	ListenAddr string `json:"listen_addr" yaml:"listen_addr"`
	ListenPort int    `json:"listen_port" yaml:"listen_port"`
}

type WireGuardConfig struct {
	Address    string `json:"address,omitempty" yaml:"address,omitempty"`
	PrivateKey string `json:"private_key,omitempty" yaml:"private_key,omitempty"`
	PublicKey  string `json:"public_key,omitempty" yaml:"public_key,omitempty"`
}

type RuntimeConfig struct {
	SingBoxPath string `json:"sing_box_path,omitempty" yaml:"sing_box_path,omitempty"`
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
	return cfg, nil
}

func Save(path string, cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (c *Config) ApplyDefaults() {
	if c.Egress.Region == "" {
		c.Egress.Region = "日本"
	}
	if c.LocalProxy.ListenAddr == "" {
		c.LocalProxy.ListenAddr = "127.0.0.1"
	}
	if c.LocalProxy.ListenPort == 0 {
		c.LocalProxy.ListenPort = 7890
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.License.Token) != "" && strings.TrimSpace(c.Egress.ProxyAddr) == "" {
		return nil
	}
	if strings.TrimSpace(c.Client.Name) == "" {
		return errors.New("client.name 不能为空")
	}
	if err := validateHostPort("hub.endpoint", c.Hub.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(c.Egress.Name) == "" {
		return errors.New("egress.name 不能为空")
	}
	if strings.TrimSpace(c.Egress.ManagementAddr) == "" {
		return errors.New("egress.management_addr 不能为空")
	}
	if err := validateHostPort("egress.proxy_addr", c.Egress.ProxyAddr); err != nil {
		return err
	}
	if c.LocalProxy.ListenPort < 1 || c.LocalProxy.ListenPort > 65535 {
		return errors.New("local_proxy.listen_port 必须在 1-65535 之间")
	}
	if net.ParseIP(c.LocalProxy.ListenAddr) == nil {
		return errors.New("local_proxy.listen_addr 不是有效 IP")
	}
	return nil
}

func validateHostPort(field, value string) error {
	host, portText, err := net.SplitHostPort(value)
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

func (c LocalProxyConfig) Addr() string {
	return net.JoinHostPort(c.ListenAddr, strconv.Itoa(c.ListenPort))
}

func hiddenString(data []byte) string {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ 0x5a
	}
	return string(out)
}

func (c EgressConfig) CustomerName() string {
	if strings.TrimSpace(c.DisplayName) != "" {
		return c.DisplayName
	}
	if strings.TrimSpace(c.Region) != "" && strings.TrimSpace(c.Type) != "" {
		return c.Region + c.Type
	}
	if strings.TrimSpace(c.Region) != "" {
		return c.Region + "出口"
	}
	return "默认出口"
}
