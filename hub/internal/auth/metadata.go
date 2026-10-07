package auth

import (
	"regexp"
	"strings"
)

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// This validates a self-reported transport candidate, not an approved build.
func validReleaseVersion(v string) bool {
	if len(v) == 0 || len(v) > 64 || !releaseVersion.MatchString(v) {
		return false
	}
	for _, part := range strings.FieldsFunc(strings.ToLower(v), func(c rune) bool { return c == '-' || c == '.' || c == '+' }) {
		switch part {
		case "dev", "development", "dirty", "snapshot":
			return false
		}
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre := v[i+1:]
		if j := strings.IndexByte(pre, '+'); j >= 0 {
			pre = pre[:j]
		}
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				if c < '0' || c > '9' {
					numeric = false
				}
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return false
			}
			switch strings.ToLower(part) {
			case "dev", "development", "dirty", "snapshot":
				return false
			}
		}
	}
	return true
}

// ValidClientMetadata validates transport metadata only, never release approval.
func ValidClientMetadata(product, version string, protocol int) bool {
	return validClientMetadata(product, version, protocol)
}

func NormalizeClientMetadata(product, version string, protocol int) (string, string, int) {
	if !validClientMetadata(product, version, protocol) {
		return "", "", 0
	}
	return strings.TrimSpace(product), version, protocol
}
