package updateverify

import "time"

// ValidScope uses the canonical accepted product/platform/architecture/channel
// sets. It does not establish a publisher or enroll that scope.
func ValidScope(s Scope) bool { return validScope(s) }

// ScopeEnums returns copies for consumers/schema projection. A consumer cannot
// mutate the verifier's canonical accepted sets through the returned slices.
func ScopeEnums() map[string][]string {
	return map[string][]string{"product": append([]string(nil), products...), "platform": append([]string(nil), platforms...), "architecture": append([]string(nil), architectures...), "channel": append([]string(nil), channels...)}
}

// ValidatePolicy checks a caller-approved policy without a candidate package.
// It neither establishes trust nor stores the policy or mutates its key ring.
func ValidatePolicy(p Policy) error {
	_, err := validatePolicy(p)
	return err
}

// CompareVersions applies the same bounded SemVer precedence as Verify.
func CompareVersions(left, right string) (int, error) {
	a, okA := parseVersion(left)
	b, okB := parseVersion(right)
	if !okA || !okB {
		return 0, ErrPolicy
	}
	return compareVersion(a, b), nil
}

// ValidatePrevious checks the public shape and consistency of a receipt already
// read from a trusted store. It does not authenticate a receipt supplied by an
// untrusted caller; the storage boundary must establish that provenance.
func ValidatePrevious(previous Receipt, p Policy, now time.Time) error {
	if ValidatePolicy(p) != nil || now.IsZero() || now.Unix() <= 0 || now.Unix() > MaxTimestamp {
		return ErrPolicy
	}
	return validatePrevious(&previous, p, now.Unix())
}
