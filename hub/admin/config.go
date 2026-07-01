package admin

import (
	"os"
	"strconv"
	"time"

	"zongheng-vpn/hub/admin/internal/api"
)

type Config = api.Config

func ConfigFromEnv() Config {
	return Config{
		ListenAddr:       env("ZHHUB_ADMIN_LISTEN", "127.0.0.1:18100"),
		DBPath:           env("ZHHUB_ADMIN_DB", "/opt/zongheng/zhhub/admin.db"),
		PublicHost:       env("ZHHUB_ADMIN_PUBLIC_HOST", "jp-proxy.ruichao.dev"),
		HubPublicIP:      env("ZHHUB_PUBLIC_IP", "36.50.84.68"),
		HubWGIP:          env("ZHHUB_WG_IP", "10.66.0.1"),
		Version:          env("ZHHUB_VERSION", "dev"),
		AdminUsername:    env("ZHHUB_ADMIN_USER", "admin"),
		AdminPasswordPHC: os.Getenv("ZHHUB_ADMIN_PASSWORD_HASH"),
		ReverseHealthURL: env("ZHHUB_ADMIN_REVERSE_HEALTH_URL", "http://10.66.0.1:18081/debug/session-health"),
		SessionTTL:       durationFromEnv("ZHHUB_ADMIN_SESSION_HOURS", 12*time.Hour),
		CookieSecure:     boolFromEnv("ZHHUB_ADMIN_COOKIE_SECURE", true),
	}
}

func env(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	hours, err := strconv.Atoi(value)
	if err != nil || hours < 1 || hours > 168 {
		return fallback
	}
	return time.Duration(hours) * time.Hour
}

func boolFromEnv(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	case "0", "false", "FALSE", "no", "NO":
		return false
	default:
		return fallback
	}
}
