package app

import (
	"strings"
	"testing"
	"zongheng-vpn/shared/paths"
)

func privateTestHome(t *testing.T) paths.Context {
	t.Helper()
	root, err := paths.CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := paths.FromRoot(root)
	preparePrivateTestHome(t, home)
	return home
}

func TestLoginTokenInputBoundedAndErrorsDoNotEchoSecret(t *testing.T) {
	secret := "synthetic-secret"
	for _, value := range []string{"", " ", secret + "\nsecond", secret + "\x00", strings.Repeat("x", 4097)} {
		_, err := readLoginToken(strings.NewReader(value))
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("invalid token accepted or exposed")
		}
	}
	if token, err := readLoginToken(strings.NewReader(secret + "\n")); err != nil || token != secret {
		t.Fatal("single line rejected")
	}
}
