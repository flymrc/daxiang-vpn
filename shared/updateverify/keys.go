package updateverify

import (
	"crypto/ed25519"
	"zongheng-vpn/shared/signingkey"
)

func allowedPublicKey(pub ed25519.PublicKey) bool { return signingkey.ValidEd25519PublicKey(pub) }
