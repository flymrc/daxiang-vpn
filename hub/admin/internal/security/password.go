package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
)

func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", errors.New("password is required")
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(phc string, password string) bool {
	memory, iterations, parallelism, salt, key, err := parsePHC(phc)
	if err != nil {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(key)))
	return subtle.ConstantTimeCompare(candidate, key) == 1
}

func parsePHC(phc string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id hash")
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id params")
	}
	values := map[string]string{}
	for _, item := range params {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			return 0, 0, 0, nil, nil, errors.New("invalid argon2id param")
		}
		values[k] = v
	}
	memory, err := parseUint32(values["m"])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	iterations, err := parseUint32(values["t"])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	p, err := strconv.ParseUint(values["p"], 10, 8)
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	return memory, iterations, uint8(p), salt, key, nil
}

func parseUint32(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err
}
