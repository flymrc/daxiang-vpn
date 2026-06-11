package netcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ShortTimeout = 3 * time.Second

func TCP(addr string, timeout time.Duration) (bool, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false, err
	}
	_ = conn.Close()
	return true, nil
}

func PublicIPViaHTTPProxy(proxyAddr string) (string, error) {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return "", err
	}

	endpoints := []string{
		"https://api64.ipify.org",
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
	}

	var errs []error
	for _, endpoint := range endpoints {
		ip, err := publicIPViaHTTPProxyEndpoint(proxyURL, endpoint)
		if err == nil && ip != "" {
			return ip, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return "", errors.Join(errs...)
}

func publicIPViaHTTPProxyEndpoint(proxyURL *url.URL, endpoint string) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("public ip endpoint returned " + resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", errors.New("public ip endpoint returned non-ip response")
	}
	return ip, nil
}
