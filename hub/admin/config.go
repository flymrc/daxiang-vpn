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
		ListenAddr:            env("ZHHUB_ADMIN_LISTEN", "127.0.0.1:18100"),
		DBPath:                env("ZHHUB_ADMIN_DB", "/opt/zongheng/zhhub/admin.db"),
		PublicHost:            env("ZHHUB_ADMIN_PUBLIC_HOST", "jp-proxy.ruichao.dev"),
		HubPublicIP:           env("ZHHUB_PUBLIC_IP", "36.50.84.68"),
		HubWGIP:               env("ZHHUB_WG_IP", "10.66.0.1"),
		Version:               env("ZHHUB_VERSION", "dev"),
		AdminUsername:         env("ZHHUB_ADMIN_USER", "admin"),
		AdminPasswordPHC:      os.Getenv("ZHHUB_ADMIN_PASSWORD_HASH"),
		ReverseHealthURL:      env("ZHHUB_ADMIN_REVERSE_HEALTH_URL", "http://10.66.0.1:18081/debug/session-health"),
		SessionTTL:            durationFromEnv("ZHHUB_ADMIN_SESSION_HOURS", 12*time.Hour),
		CookieSecure:          boolFromEnv("ZHHUB_ADMIN_COOKIE_SECURE", true),
		AuditRetention:        daysFromEnv("ZHHUB_ADMIN_AUDIT_RETENTION_DAYS", 90),
		MaxAuditEvents:        int64FromEnv("ZHHUB_ADMIN_AUDIT_MAX_ROWS", 50000),
		LoginAttemptRetention: daysFromEnv("ZHHUB_ADMIN_LOGIN_ATTEMPT_RETENTION_DAYS", 7),
		MaxLoginAttempts:      int64FromEnv("ZHHUB_ADMIN_LOGIN_ATTEMPT_MAX_ROWS", 10000),
		MaintenanceInterval:   minutesFromEnv("ZHHUB_ADMIN_DB_MAINTENANCE_MINUTES", 60),
		ExitIPCheckURL:        env("ZHHUB_ADMIN_EXIT_IP_CHECK_URL", "https://api64.ipify.org"),
		ExitIPv4CheckURL:      env("ZHHUB_ADMIN_EXIT_IPV4_CHECK_URL", "https://api.ipify.org"),
		ExitIPv6CheckURL:      env("ZHHUB_ADMIN_EXIT_IPV6_CHECK_URL", "https://api6.ipify.org"),
		ExitIPCheckTimeout:    secondsFromEnv("ZHHUB_ADMIN_EXIT_IP_CHECK_TIMEOUT_SECONDS", 8),
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

func daysFromEnv(name string, fallbackDays int) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return time.Duration(fallbackDays) * 24 * time.Hour
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > 3650 {
		return time.Duration(fallbackDays) * 24 * time.Hour
	}
	return time.Duration(days) * 24 * time.Hour
}

func minutesFromEnv(name string, fallbackMinutes int) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return time.Duration(fallbackMinutes) * time.Minute
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 1 || minutes > 1440 {
		return time.Duration(fallbackMinutes) * time.Minute
	}
	return time.Duration(minutes) * time.Minute
}

func secondsFromEnv(name string, fallbackSeconds int) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return time.Duration(fallbackSeconds) * time.Second
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > 60 {
		return time.Duration(fallbackSeconds) * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func int64FromEnv(name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1000 || parsed > 10000000 {
		return fallback
	}
	return parsed
}
