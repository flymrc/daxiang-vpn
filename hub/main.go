package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	adminpanel "zongheng-vpn/hub/admin"
	"zongheng-vpn/hub/internal/auth"
)

func main() {
	configPath := env("ZHHUB_TOKENS", "./config/tokens.yaml")
	compatListenAddr := env("ZHHUB_LISTEN", "0.0.0.0:18080")
	trustedProxyListenAddr := env("ZHHUB_TRUSTED_PROXY_LISTEN", "127.0.0.1:18079")
	adminEnabled := env("ZHHUB_ADMIN_ENABLED", "1") != "0"
	if err := validateClientListeners(compatListenAddr, trustedProxyListenAddr); err != nil {
		log.Fatalf("客户端监听配置无效：%v", err)
	}

	store, err := auth.LoadTokenStore(configPath)
	if err != nil {
		log.Fatalf("加载授权配置失败：%v", err)
	}

	server := auth.NewServer(store)
	compatMux := clientMux(server, auth.ClientIngressCompat)
	trustedProxyMux := clientMux(server, auth.ClientIngressTrustedProxy)

	if adminEnabled {
		adminConfig := adminpanel.ConfigFromEnv()
		adminServer, err := adminpanel.NewServer(adminConfig, store, server)
		if err != nil {
			log.Fatalf("加载 Hub 控制台失败：%v", err)
		}
		defer adminServer.Close()
		go func() {
			log.Printf("zhhub 控制台已启动：%s", adminConfig.ListenAddr)
			if err := http.ListenAndServe(adminConfig.ListenAddr, adminServer); err != nil {
				log.Fatalf("控制台服务退出：%v", err)
			}
		}()
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("zhhub 兼容入口已启动：%s", compatListenAddr)
		errCh <- http.ListenAndServe(compatListenAddr, compatMux)
	}()
	go func() {
		log.Printf("zhhub 可信代理入口已启动：%s", trustedProxyListenAddr)
		errCh <- http.ListenAndServe(trustedProxyListenAddr, trustedProxyMux)
	}()
	log.Fatalf("客户端服务退出：%v", <-errCh)
}

func clientMux(server *auth.Server, ingress auth.ClientIngress) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.Health)
	mux.HandleFunc("/api/client/bootstrap", server.BootstrapHandler(ingress))
	mux.HandleFunc("/api/client/rotate-ip", server.RotateIP)
	return mux
}

func validateClientListeners(compatAddr string, trustedProxyAddr string) error {
	_, compatPort, err := parseListenerAddress(compatAddr)
	if err != nil {
		return fmt.Errorf("ZHHUB_LISTEN: %w", err)
	}
	trustedIP, trustedPort, err := parseListenerAddress(trustedProxyAddr)
	if err != nil {
		return fmt.Errorf("ZHHUB_TRUSTED_PROXY_LISTEN: %w", err)
	}
	if trustedIP == nil || !trustedIP.IsLoopback() {
		return errors.New("ZHHUB_TRUSTED_PROXY_LISTEN must use a loopback IP literal")
	}
	if compatPort == trustedPort {
		return errors.New("client listeners must use different ports")
	}
	return nil
}

func parseListenerAddress(addr string) (net.IP, int, error) {
	host, rawPort, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return nil, 0, err
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, 0, fmt.Errorf("invalid port %q", rawPort)
	}
	return net.ParseIP(strings.Trim(host, "[]")), port, nil
}

func env(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
