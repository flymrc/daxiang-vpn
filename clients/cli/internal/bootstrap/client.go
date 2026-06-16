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

	"zongheng-vpn/shared/config"
)

type request struct {
	Token string `json:"token"`
}

type rotateIPRequest struct {
	Token       string `json:"token"`
	DownSeconds int    `json:"down_seconds"`
}

type RotateIPResult struct {
	Status            string `json:"status"`
	Egress            string `json:"egress"`
	DownSeconds       int    `json:"down_seconds"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
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
	if resp.StatusCode == http.StatusConflict {
		return config.Config{}, errors.New("授权码正在其他网络使用，请先断开另一台设备或等待约 30 秒后重试")
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

func RotateIP(token string, downSeconds int) (RotateIPResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return RotateIPResult{}, errors.New("授权码不能为空")
	}
	body, err := json.Marshal(rotateIPRequest{Token: token, DownSeconds: downSeconds})
	if err != nil {
		return RotateIPResult{}, err
	}
	req, err := http.NewRequest(http.MethodPost, apiBase()+"/api/client/rotate-ip", bytes.NewReader(body))
	if err != nil {
		return RotateIPResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return RotateIPResult{}, errors.New("换 IP 服务连接失败")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		res, err := decodeRotateIPResponse(resp)
		if err != nil {
			return RotateIPResult{}, err
		}
		if res.Status == "" {
			res.Status = "triggered"
		}
		return res, nil
	case http.StatusConflict:
		res, err := decodeRotateIPResponse(resp)
		if err != nil {
			return RotateIPResult{}, err
		}
		if res.Status == "" {
			res.Status = "busy"
		}
		if res.Message == "" {
			res.Message = "换 IP 正在进行中，请稍后再试"
		}
		return res, nil
	case http.StatusUnauthorized:
		return RotateIPResult{}, errors.New("授权码无效或已过期")
	case http.StatusBadGateway:
		return RotateIPResult{}, errors.New("Hub 未能触发 Android 控制面换 IP，请联系管理员检查控制面 key 或手机状态")
	case http.StatusBadRequest:
		return RotateIPResult{}, errors.New("当前出口不支持一键换 IP")
	default:
		return RotateIPResult{}, fmt.Errorf("换 IP 服务异常：%d", resp.StatusCode)
	}
}

func decodeRotateIPResponse(resp *http.Response) (RotateIPResult, error) {
	var res RotateIPResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return RotateIPResult{}, errors.New("换 IP 服务响应解析失败")
	}
	return res, nil
}

func apiBase() string {
	if value := strings.TrimRight(os.Getenv("ZHVPN_API_BASE"), "/"); value != "" {
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
