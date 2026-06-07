package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"daxiang-vpn/shared/config"
)

type request struct {
	Token string `json:"token"`
}

func Fetch(token string) (config.Config, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return config.Config{}, errors.New("授权码不能为空")
	}

	body, err := json.Marshal(request{Token: token})
	if err != nil {
		return config.Config{}, err
	}

	req, err := http.NewRequest(http.MethodPost, apiBase()+"/api/client/bootstrap", bytes.NewReader(body))
	if err != nil {
		return config.Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return config.Config{}, errors.New("授权服务连接失败")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return config.Config{}, errors.New("授权码无效或已过期")
	}
	if resp.StatusCode != http.StatusOK {
		return config.Config{}, fmt.Errorf("授权服务异常：%d", resp.StatusCode)
	}

	var cfg config.Config
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return config.Config{}, errors.New("授权配置解析失败")
	}
	cfg.License.Token = token
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func apiBase() string {
	if value := strings.TrimRight(os.Getenv("DXVPN_API_BASE"), "/"); value != "" {
		return value
	}
	return hiddenString([]byte{0x32, 0x2e, 0x2e, 0x2a, 0x60, 0x75, 0x75, 0x69, 0x6c, 0x74, 0x6f, 0x6a, 0x74, 0x62, 0x6e, 0x74, 0x6c, 0x62, 0x60, 0x6b, 0x62, 0x6a, 0x62, 0x6a})
}

func hiddenString(data []byte) string {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ 0x5a
	}
	return string(out)
}
