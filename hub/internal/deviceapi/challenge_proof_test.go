package deviceapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	generated "zongheng-vpn/shared/devicecontract"
)

func TestTLSChallengeProofExactRawBodyAndReplayRetainsOneRow(t *testing.T) {
	store, db := apiStore(t)
	handler, err := NewServer(store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &wireClient{t: t, url: server.URL, client: server.Client(), pub: base64.StdEncoding.EncodeToString(pub), priv: priv}
	a, err := store.IssueActivation(context.Background(), "owner", time.Now().Add(time.Hour), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	activationBody := encoded(t, generated.ActivationRequest{ActivationCredential: a.Credential, AuthPublicKey: c.pub})
	body := encoded(t, map[string]string{"purpose": "activate", "method": "POST", "path": "/api/v2/activate", "body_sha256": deviceauth.BodyDigest(activationBody), "activation_credential": a.Credential, "auth_public_key": c.pub})
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	clientNonce := fmt.Sprintf("%x", nonce)
	expires := time.Now().Add(45 * time.Second).Unix()
	proof := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, generated.ChallengeProofBytes(deviceauth.BodyDigest(body), clientNonce, expires)))
	headers := map[string]string{"X-ZH-Challenge-Proof": proof, "X-ZH-Client-Nonce": clientNonce, "X-ZH-Client-Expires": strconv.FormatInt(expires, 10)}
	altered := append(append([]byte(nil), body...), '\n')
	if code, _ := c.call("POST", "/api/v2/challenges", altered, nil, headers); code != 401 {
		t.Fatal("challenge proof ignored exact raw body bytes")
	}
	code, first := c.call("POST", "/api/v2/challenges", body, nil, headers)
	if code != 200 {
		t.Fatal("correct exact-body proof rejected")
	}
	code, replay := c.call("POST", "/api/v2/challenges", body, nil, headers)
	if code != 200 || !bytes.Equal(first, replay) {
		t.Fatal("same proof created another nonce")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deviceauth_challenges`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unsigned/tampered/replayed proof grew retained rows")
	}
	var ch generated.Challenge
	if err := json.Unmarshal(first, &ch); err != nil {
		t.Fatal(err)
	}
	q := deviceauth.SignedRequest{Purpose: "activate", Method: "POST", Path: "/api/v2/activate", BodyDigest: deviceauth.BodyDigest(activationBody), ChallengeID: ch.ChallengeId, Nonce: ch.Nonce, Expires: ch.ExpiresUnixSeconds, RequestID: "proof-exact-activation"}
	q.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, deviceauth.SigningBytes(q)))
	if code, _ := c.call("POST", q.Path, activationBody, &q, nil); code != 201 {
		t.Fatal("bound proof could not activate")
	}
	if code, _ := c.call("POST", "/api/v2/challenges", body, nil, headers); code != 401 {
		t.Fatal("consumed activation/challenge proof replay accepted")
	}
}
