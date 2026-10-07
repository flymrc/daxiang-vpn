package main

import (
	"context"
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
	"zongheng-vpn/hub/internal/deviceapi"
	"zongheng-vpn/hub/internal/httpboundary"
	"zongheng-vpn/hub/internal/processbudget"
)

func main() {
	if code, handled := processbudget.SupervisorMain(os.Args[1:]); handled {
		os.Exit(code)
	}
	deviceConfig, deviceEnabled, err := deviceConfigFromEnv()
	if err != nil {
		log.Fatalf("设备授权 opt-in 配置无效")
	}
	var deviceService *deviceapi.Service
	if deviceEnabled {
		deviceService, err = deviceapi.Open(context.Background(), deviceConfig)
		if err != nil {
			log.Fatalf("设备授权 opt-in 初始化失败")
		}
		defer deviceService.Close()
	}
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
			if err := httpboundary.NewServer(adminConfig.ListenAddr, adminServer).ListenAndServe(); err != nil {
				log.Fatalf("控制台服务退出：%v", err)
			}
		}()
	}

	errCh := make(chan error, 3)
	if deviceService != nil {
		go func() {
			log.Printf("zhhub 设备授权隔离入口已启动：%s", deviceConfig.ListenAddr)
			errCh <- deviceService.Run(context.Background())
		}()
	}
	go func() {
		log.Printf("zhhub 兼容入口已启动：%s", compatListenAddr)
		errCh <- httpboundary.NewServer(compatListenAddr, compatMux).ListenAndServe()
	}()
	go func() {
		log.Printf("zhhub 可信代理入口已启动：%s", trustedProxyListenAddr)
		errCh <- httpboundary.NewServer(trustedProxyListenAddr, trustedProxyMux).ListenAndServe()
	}()
	log.Fatalf("客户端服务退出：%v", <-errCh)
}

func deviceConfigFromEnv() (deviceapi.Config, bool, error) {
	flag := env("ZHHUB_DEVICE_AUTH_ENABLED", "0")
	if flag == "0" {
		return deviceapi.Config{}, false, nil
	}
	if flag != "1" {
		return deviceapi.Config{}, false, errors.New("invalid opt-in flag")
	}
	c := deviceapi.Config{ListenAddr: env("ZHHUB_DEVICE_LISTEN", "127.0.0.1:18443"), DBPath: os.Getenv("ZHHUB_DEVICE_DB"), PolicyPath: os.Getenv("ZHHUB_DEVICE_POLICY"), WGExecutable: os.Getenv("ZHHUB_DEVICE_WG_BIN"), SupervisorExecutable: os.Getenv("ZHHUB_DEVICE_SUPERVISOR_BIN"), WGInterface: os.Getenv("ZHHUB_DEVICE_WG_INTERFACE"), TLSCert: os.Getenv("ZHHUB_DEVICE_TLS_CERT"), TLSKey: os.Getenv("ZHHUB_DEVICE_TLS_KEY")}
	// No default profile: enabling the authority does not enable proxy bootstrap.
	c.ProxyProfilePath = os.Getenv("ZHHUB_DEVICE_PROXY_PROFILE")
	// Legacy APIs still mutate their configured interface. Until cutover installs
	// a sole-writer boundary, the v2 listener is isolated on a different interface.
	legacyInterface := strings.TrimSpace(env("ZHHUB_WG_INTERFACE", "wg0"))
	if legacyInterface == "" {
		legacyInterface = "wg0"
	}
	if c.WGInterface == "" || c.WGInterface == legacyInterface {
		return c, false, errors.New("v2 requires an isolated interface before authority cutover")
	}
	return c, true, nil
}

func clientMux(server *auth.Server, ingress auth.ClientIngress) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.Health)
	mux.HandleFunc("/api/client/bootstrap", server.BootstrapHandler(ingress))
	mux.HandleFunc("/api/client/rotate-ip", server.RotateIPHandler(ingress))
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
