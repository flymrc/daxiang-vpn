package main

import (
	"log"
	"net/http"
	"os"

	adminpanel "zongheng-vpn/hub/admin"
	"zongheng-vpn/hub/internal/auth"
)

func main() {
	configPath := env("ZHHUB_TOKENS", "./config/tokens.yaml")
	listenAddr := env("ZHHUB_LISTEN", "0.0.0.0:18080")
	adminEnabled := env("ZHHUB_ADMIN_ENABLED", "1") != "0"

	store, err := auth.LoadTokenStore(configPath)
	if err != nil {
		log.Fatalf("加载授权配置失败：%v", err)
	}

	server := auth.NewServer(store)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.Health)
	mux.HandleFunc("/api/client/bootstrap", server.Bootstrap)
	mux.HandleFunc("/api/client/rotate-ip", server.RotateIP)

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

	log.Printf("zhhub 已启动：%s", listenAddr)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("服务退出：%v", err)
	}
}

func env(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
