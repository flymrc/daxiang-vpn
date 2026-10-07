package signingkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestGeneratedSigningKeysRemainValid(t *testing.T) {
	for i := 0; i < 128; i++ {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if !ValidEd25519PublicKey(pub) {
			t.Fatal("standard generated key refused")
		}
	}
}
func TestMalformedPublicKeyEncodingsRefused(t *testing.T) {
	for _, value := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000000",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	} {
		key, err := hex.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		if ValidEd25519PublicKey(key) {
			t.Fatal("malformed key permitted")
		}
		key[31] |= 0x80
		if ValidEd25519PublicKey(key) {
			t.Fatal("malformed sign alias permitted")
		}
	}
	for _, size := range []int{0, 1, 31, 33, 64} {
		if ValidEd25519PublicKey(make([]byte, size)) {
			t.Fatal("invalid key size")
		}
	}
}
