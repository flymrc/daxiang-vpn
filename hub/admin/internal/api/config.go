package api

import "time"

const (
	defaultAuditRetention        = 90 * 24 * time.Hour
	defaultMaxAuditEvents        = int64(50000)
	defaultLoginAttemptRetention = 7 * 24 * time.Hour
	defaultMaxLoginAttempts      = int64(10000)
	defaultMaintenanceInterval   = time.Hour
	defaultExitIPCheckURL        = "https://api64.ipify.org"
	defaultExitIPv4CheckURL      = "https://api.ipify.org"
	defaultExitIPv6CheckURL      = "https://api6.ipify.org"
	defaultExitIPCheckTimeout    = 8 * time.Second
)

type Config struct {
	ListenAddr            string
	DBPath                string
	PublicHost            string
	HubPublicIP           string
	HubWGIP               string
	Version               string
	AdminUsername         string
	AdminPasswordPHC      string
	ReverseHealthURL      string
	SessionTTL            time.Duration
	CookieSecure          bool
	AuditRetention        time.Duration
	MaxAuditEvents        int64
	LoginAttemptRetention time.Duration
	MaxLoginAttempts      int64
	MaintenanceInterval   time.Duration
	ExitIPCheckURL        string
	ExitIPv4CheckURL      string
	ExitIPv6CheckURL      string
	ExitIPCheckTimeout    time.Duration
}

func (cfg Config) withDefaults() Config {
	if cfg.AuditRetention == 0 {
		cfg.AuditRetention = defaultAuditRetention
	}
	if cfg.MaxAuditEvents == 0 {
		cfg.MaxAuditEvents = defaultMaxAuditEvents
	}
	if cfg.LoginAttemptRetention == 0 {
		cfg.LoginAttemptRetention = defaultLoginAttemptRetention
	}
	if cfg.MaxLoginAttempts == 0 {
		cfg.MaxLoginAttempts = defaultMaxLoginAttempts
	}
	if cfg.MaintenanceInterval == 0 {
		cfg.MaintenanceInterval = defaultMaintenanceInterval
	}
	if cfg.ExitIPCheckURL == "" {
		cfg.ExitIPCheckURL = defaultExitIPCheckURL
	}
	if cfg.ExitIPv4CheckURL == "" {
		cfg.ExitIPv4CheckURL = defaultExitIPv4CheckURL
	}
	if cfg.ExitIPv6CheckURL == "" {
		cfg.ExitIPv6CheckURL = defaultExitIPv6CheckURL
	}
	if cfg.ExitIPCheckTimeout == 0 {
		cfg.ExitIPCheckTimeout = defaultExitIPCheckTimeout
	}
	return cfg
}
