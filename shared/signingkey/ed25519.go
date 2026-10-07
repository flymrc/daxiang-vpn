// Package signingkey validates the public-key encoding accepted at signing authority boundaries.
package signingkey

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
)

// Authority inputs must not accidentally contain a point with no signing-key
// ownership. Go's classic Ed25519 verification can accept the identity point
// with a trivial signature, so policy additionally rejects small-order points
// and noncanonical Y encodings. These public mathematical encodings are the
// order-8 Y coordinates documented in libsodium's ge25519_has_small_order:
// https://github.com/jedisct1/libsodium/blob/1.0.18/src/libsodium/crypto_core/ed25519/ref10/ed25519_ref10.c#L966
// ValidEd25519PublicKey rejects noncanonical and known small-order encodings.
// It is an input check, not a replacement signature verifier or a custom curve implementation.
func ValidEd25519PublicKey(pub []byte) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	var y, prime, zero, one, minusOne [32]byte
	copy(y[:], pub)
	y[31] &= 0x7f
	prime[0], minusOne[0], one[0] = 0xed, 0xec, 1
	for i := 1; i < 32; i++ {
		prime[i], minusOne[i] = 0xff, 0xff
	}
	prime[31], minusOne[31] = 0x7f, 0x7f
	// Little-endian Y must be strictly less than 2^255-19.
	canonical := false
	for i := 31; i >= 0; i-- {
		if y[i] != prime[i] {
			canonical = y[i] < prime[i]
			break
		}
	}
	if !canonical || y == zero || y == one || y == minusOne {
		return false
	}
	for _, encoded := range []string{
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	} {
		point, _ := hex.DecodeString(encoded)
		if bytes.Equal(y[:], point) {
			return false
		}
	}
	return true
}
