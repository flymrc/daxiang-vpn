package devicecontract

import (
	"encoding/json"
	"strconv"
)

// ChallengeProofBytes binds proof to the exact HTTP challenge body and a short
// client nonce/deadline. The signature itself is carried in a header, not body.
func ChallengeProofBytes(rawBodySHA256, clientNonce string, expiresUnixSeconds int64) []byte {
	b, _ := json.Marshal([]string{"zhvpn-device-challenge", "v2", "POST", "/api/v2/challenges", rawBodySHA256, clientNonce, strconv.FormatInt(expiresUnixSeconds, 10)})
	return b
}
