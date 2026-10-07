package updateverify

import (
	"strconv"
	"strings"
)

type version struct {
	core [3]uint64
	pre  []string
}

// Strict SemVer with uint32-bounded major/minor/patch and bounded ASCII IDs.
// Numeric prerelease comparison uses length/text, never machine integer parsing.
// Build metadata is valid but does not affect SemVer precedence.
func parseVersion(s string) (version, bool) {
	var result version
	if len(s) == 0 || len(s) > 128 {
		return result, false
	}
	parts := strings.Split(s, "+")
	if len(parts) > 2 || len(parts) == 2 && !validVersionIDs(parts[1], false) {
		return result, false
	}
	main := strings.SplitN(parts[0], "-", 2)
	if len(main) == 2 {
		if !validVersionIDs(main[1], true) {
			return result, false
		}
		result.pre = strings.Split(main[1], ".")
	}
	core := strings.Split(main[0], ".")
	if len(core) != 3 {
		return result, false
	}
	for i, item := range core {
		if !numeric(item) || len(item) > 1 && item[0] == '0' {
			return result, false
		}
		n, err := strconv.ParseUint(item, 10, 32)
		if err != nil {
			return result, false
		}
		result.core[i] = n
	}
	return result, true
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validVersionIDs(s string, prerelease bool) bool {
	ids := strings.Split(s, ".")
	if len(ids) == 0 || len(ids) > 8 {
		return false
	}
	for _, id := range ids {
		if len(id) == 0 || len(id) > 64 || prerelease && numeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
		for i := range id {
			c := id[i]
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
				return false
			}
		}
	}
	return true
}

func compareVersion(a, b version) int {
	for i := range a.core {
		if a.core[i] < b.core[i] {
			return -1
		}
		if a.core[i] > b.core[i] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		if x == y {
			continue
		}
		xn, yn := numeric(x), numeric(y)
		if xn && !yn {
			return -1
		}
		if !xn && yn {
			return 1
		}
		if xn && len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
		return strings.Compare(x, y)
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
