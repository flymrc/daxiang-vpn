package auth

import (
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type TokenStore struct {
	Tokens map[string]TokenRecord `yaml:"tokens"`
}

type TokenRecord struct {
	Enabled    bool       `yaml:"enabled"`
	ClientName string     `yaml:"client_name"`
	ExpiresAt  string     `yaml:"expires_at"`
	Egress     Egress     `yaml:"egress"`
	LocalProxy LocalProxy `yaml:"local_proxy"`
	Hub        Hub        `yaml:"hub"`
	WireGuard  WireGuard  `yaml:"wireguard"`
}

type TokenSnapshot struct {
	Token  string
	Record TokenRecord
}

type Hub struct {
	Endpoint  string `yaml:"endpoint" json:"endpoint"`
	PublicKey string `yaml:"public_key" json:"public_key"`
}

type Egress struct {
	Name           string `yaml:"name" json:"name"`
	DisplayName    string `yaml:"display_name" json:"display_name"`
	Region         string `yaml:"region" json:"region"`
	Type           string `yaml:"type" json:"type"`
	ManagementAddr string `yaml:"management_addr" json:"management_addr"`
	ProxyAddr      string `yaml:"proxy_addr" json:"proxy_addr"`
}

type LocalProxy struct {
	ListenAddr string `yaml:"listen_addr" json:"listen_addr"`
	ListenPort int    `yaml:"listen_port" json:"listen_port"`
}

type WireGuard struct {
	Address    string `yaml:"address" json:"address"`
	PrivateKey string `yaml:"private_key" json:"private_key"`
}

func LoadTokenStore(path string) (*TokenStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var store TokenStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if len(store.Tokens) == 0 {
		return nil, errors.New("tokens 不能为空")
	}
	return &store, nil
}

func (s *TokenStore) Resolve(token string, now time.Time) (TokenRecord, bool) {
	record, ok := s.Tokens[strings.TrimSpace(token)]
	if !ok || !record.Enabled {
		return TokenRecord{}, false
	}
	if record.ExpiresAt != "" {
		expiry, err := time.Parse("2006-01-02", record.ExpiresAt)
		if err != nil {
			return TokenRecord{}, false
		}
		if now.After(expiry.Add(24 * time.Hour)) {
			return TokenRecord{}, false
		}
	}
	record.ApplyDefaults()
	return record, true
}

func (s *TokenStore) Snapshot() []TokenSnapshot {
	if s == nil {
		return nil
	}
	items := make([]TokenSnapshot, 0, len(s.Tokens))
	for token, record := range s.Tokens {
		record.ApplyDefaults()
		items = append(items, TokenSnapshot{
			Token:  token,
			Record: record,
		})
	}
	return items
}

func (s *TokenStore) EgressByName(name string) (Egress, bool) {
	name = strings.TrimSpace(name)
	if s == nil || name == "" {
		return Egress{}, false
	}
	for _, record := range s.Tokens {
		record.ApplyDefaults()
		if record.Egress.Name == name {
			return record.Egress, true
		}
	}
	return Egress{}, false
}

func (r *TokenRecord) ApplyDefaults() {
	if r.Egress.DisplayName == "" {
		r.Egress.DisplayName = "日本住宅出口"
	}
	if r.Egress.Region == "" {
		r.Egress.Region = "日本"
	}
	if r.Egress.Type == "" {
		r.Egress.Type = "住宅 IP"
	}
	if r.LocalProxy.ListenAddr == "" {
		r.LocalProxy.ListenAddr = "127.0.0.1"
	}
	if r.LocalProxy.ListenPort == 0 {
		r.LocalProxy.ListenPort = 7890
	}
}
