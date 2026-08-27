package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func TokenID(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])[:12]
}
