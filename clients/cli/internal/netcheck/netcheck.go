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

type PublicIPs struct {
	IPv4 string
	IPv6 string
}

func TCP(addr string, timeout time.Duration) (bool, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false, err
	}
	_ = conn.Close()
	return true, nil
}

func PublicIPViaHTTPProxy(proxyAddr string) (string, error) {
	ips, err := PublicIPsViaHTTPProxy(proxyAddr)
	if err != nil {
		return "", err
	}
	if ips.IPv6 != "" {
		return ips.IPv6, nil
	}
	if ips.IPv4 != "" {
		return ips.IPv4, nil
	}
	return "", errors.New("public ip endpoints returned no ip")
}

func PublicIPsViaHTTPProxy(proxyAddr string) (PublicIPs, error) {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return PublicIPs{}, err
	}

	v6Endpoints := []string{
		"https://api6.ipify.org",
		"https://api64.ipify.org",
		"https://ifconfig.me/ip",
	}
	v4Endpoints := []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
	}

	var errs []error
	var result PublicIPs
	for _, endpoint := range v6Endpoints {
		ip, err := publicIPViaHTTPProxyEndpoint(proxyURL, endpoint)
		if err == nil && ip != "" && strings.Contains(ip, ":") {
			result.IPv6 = ip
			break
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	for _, endpoint := range v4Endpoints {
		ip, err := publicIPViaHTTPProxyEndpoint(proxyURL, endpoint)
		if err == nil && ip != "" && strings.Contains(ip, ".") {
			result.IPv4 = ip
			break
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if result.IPv4 != "" || result.IPv6 != "" {
		return result, nil
	}
	return PublicIPs{}, errors.Join(errs...)
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
