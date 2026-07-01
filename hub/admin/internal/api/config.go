package api

import "time"

type Config struct {
	ListenAddr       string
	DBPath           string
	PublicHost       string
	HubPublicIP      string
	HubWGIP          string
	Version          string
	AdminUsername    string
	AdminPasswordPHC string
	ReverseHealthURL string
	SessionTTL       time.Duration
	CookieSecure     bool
}
