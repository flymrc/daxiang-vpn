package deviceauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestCredentialSigningKeyBoundary(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	for _, first := range []byte{0, 1} {
		bad := make([]byte, 32)
		bad[0] = first
		if _, err := authKey(base64.StdEncoding.EncodeToString(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid credential key not refused")
		}
		bad[31] = 0x80
		if _, err := authKey(base64.StdEncoding.EncodeToString(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid credential alias not refused")
		}
	}
}
