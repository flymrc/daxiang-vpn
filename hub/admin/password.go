package admin

import "zongheng-vpn/hub/admin/internal/security"

func HashPassword(password string) (string, error) {
	return security.HashPassword(password)
}

func VerifyPassword(phc string, password string) bool {
	return security.VerifyPassword(phc, password)
}
