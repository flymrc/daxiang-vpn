package main

import (
	"log"
	"net/http"
	"os"

	"zongheng-vpn/hub/internal/auth"
)

func main() {
	configPath := env("ZHHUB_TOKENS", "./config/tokens.yaml")
	listenAddr := env("ZHHUB_LISTEN", "0.0.0.0:18080")

	store, err := auth.LoadTokenStore(configPath)
	if err != nil {
		log.Fatalf("加载授权配置失败：%v", err)
	}

	server := auth.NewServer(store)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.Health)
	mux.HandleFunc("/api/client/bootstrap", server.Bootstrap)
	mux.HandleFunc("/api/client/rotate-ip", server.RotateIP)

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
