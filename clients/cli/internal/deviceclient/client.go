// Package deviceclient is the explicit opt-in consumer of the isolated v2
// device authority. It never imports legacy tokens or changes proxy/WG state.
package deviceclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	dc "zongheng-vpn/shared/devicecontract"
)

const Version = 2

// Receipt is deliberately separate from the legacy CLI v1 result. Credential
// is public metadata only. Pending means local durable intent needs resolution.
type Receipt struct {
	ContractVersion int            `json:"contract_version"`
	Command         string         `json:"command"`
	OK              bool           `json:"ok"`
	Outcome         string         `json:"outcome"`
	Pending         bool           `json:"pending"`
	Code            string         `json:"code,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	IdempotencyKey  string         `json:"idempotency_key,omitempty"`
	Credential      *dc.Credential `json:"credential,omitempty"`
	Operation       *dc.Operation  `json:"operation,omitempty"`
}

type failure struct {
	code    string
	unknown bool
}

func (e *failure) Error() string { return "device v2: " + e.code }

// Client owns a direct, normally verified TLS transport. RootCAs may contain a
// private authority CA; it never disables hostname or certificate verification.
type Client struct {
	server string
	http   *http.Client
	now    func() time.Time
}

func New(server string, roots *x509.CertPool) (*Client, error) {
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || u.Fragment != "" || strings.TrimSpace(server) != server {
		return nil, &failure{code: "invalid_server"}
	}
	u.Path = ""
	t := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second}
	return &Client{server: u.String(), now: time.Now, http: &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// SigningBytes mirrors the canonical OpenAPI byte contract. Do not sign a
// re-serialized body; digest must describe the exact bytes subsequently sent.
func SigningBytes(purpose, method, path, digest, device, credential string, ch dc.Challenge, requestID string) []byte {
	b, _ := json.Marshal([]string{"zhvpn-device-request", "v2", purpose, method, path, digest, device, credential, ch.ChallengeId, ch.Nonce, requestID, strconv.FormatInt(ch.ExpiresUnixSeconds, 10)})
	return b
}
func bodyDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func opaque() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func hexID(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == s
}

func (c *Client) call(ctx context.Context, method, path string, body []byte, headers http.Header, expected int, out any, mutation bool) error {
	// This check is before any network attempt. Cancellation after Do starts
	// remains unknown for mutations because the server may already have committed.
	if ctx.Err() != nil {
		return &failure{code: "command_timeout"}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, bytes.NewReader(body))
	if err != nil {
		return &failure{code: "invalid_request"}
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &failure{code: "connection_failure", unknown: mutation}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(data) > 32768 {
		return &failure{code: "invalid_response", unknown: mutation}
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return &failure{code: "invalid_response", unknown: mutation}
	}
	if resp.StatusCode != expected {
		var e dc.Error
		if strictDecode(data, &e, "code") != nil || !validServerCode(e.Code) {
			return &failure{code: "invalid_response", unknown: mutation}
		}
		// 5xx/redirect/unrecognized statuses cannot prove a mutation was rolled
		// back. No automatic retry is performed, including transport errors.
		known := resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 404 || resp.StatusCode == 409 || resp.StatusCode == 429
		return &failure{code: string(e.Code), unknown: mutation && !known}
	}
	if err := strictDecode(data, out, requiredFields(out)...); err != nil {
		return &failure{code: "invalid_response", unknown: mutation}
	}
	return nil
}
func validServerCode(c dc.ErrorCode) bool {
	switch c {
	case dc.InvalidRequest, dc.Unauthorized, dc.Conflict, dc.NotFound, dc.Unavailable, dc.RateLimited:
		return true
	}
	return false
}

func (c *Client) challenge(ctx context.Context, purpose, method, path string, body []byte, credential *dc.Credential, activation string, key ed25519.PrivateKey) (dc.Challenge, error) {
	r := dc.ChallengeRequest{Purpose: dc.ChallengeRequestPurpose(purpose), Method: dc.ChallengeRequestMethod(method), Path: path, BodySha256: bodyDigest(body)}
	if credential != nil {
		r.DeviceId = &credential.DeviceId
		r.CredentialId = &credential.CredentialId
	} else {
		r.ActivationCredential = &activation
		pub := publicKey(key)
		r.AuthPublicKey = &pub
	}
	return c.issueChallenge(ctx, r, key)
}
func (c *Client) issueChallenge(ctx context.Context, r dc.ChallengeRequest, key ed25519.PrivateKey) (dc.Challenge, error) {
	return c.issueChallengeWithOwner(ctx, r, key, nil)
}
func (c *Client) issueChallengeWithOwner(ctx context.Context, r dc.ChallengeRequest, key, ownerKey ed25519.PrivateKey) (dc.Challenge, error) {
	b, _ := json.Marshal(r)
	nonce, e := opaque()
	if e != nil {
		return dc.Challenge{}, e
	}
	expires := c.now().Add(45 * time.Second).Unix()
	payload := dc.ChallengeProofBytes(bodyDigest(b), nonce, expires)
	h := http.Header{}
	h.Set("X-ZH-Client-Nonce", nonce)
	h.Set("X-ZH-Client-Expires", strconv.FormatInt(expires, 10))
	h.Set("X-ZH-Challenge-Proof", base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)))
	if ownerKey != nil {
		h.Set("X-ZH-Resolve-Owner-Proof", base64.StdEncoding.EncodeToString(ed25519.Sign(ownerKey, payload)))
	}
	var ch dc.Challenge
	if e := c.call(ctx, "POST", "/api/v2/challenges", b, h, 200, &ch, false); e != nil {
		return ch, e
	}
	now := c.now().Unix()
	if !hexID(ch.ChallengeId) || !hexID(ch.Nonce) || ch.ExpiresUnixSeconds <= now || ch.ExpiresUnixSeconds > now+120 {
		return ch, &failure{code: "invalid_response"}
	}
	return ch, nil
}

func headers(purpose, method, path string, body []byte, cr *dc.Credential, ch dc.Challenge, requestID string, key ed25519.PrivateKey) (http.Header, []byte) {
	did, cid := "", ""
	if cr != nil {
		did, cid = cr.DeviceId, cr.CredentialId
	}
	payload := SigningBytes(purpose, method, path, bodyDigest(body), did, cid, ch, requestID)
	h := http.Header{}
	h.Set("X-ZH-Challenge", ch.ChallengeId)
	h.Set("X-ZH-Nonce", ch.Nonce)
	h.Set("X-ZH-Expires", strconv.FormatInt(ch.ExpiresUnixSeconds, 10))
	h.Set("X-ZH-Request-ID", requestID)
	h.Set("X-ZH-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)))
	if cr != nil {
		h.Set("X-ZH-Device", did)
		h.Set("X-ZH-Credential", cid)
	}
	return h, payload
}

func validateCredential(cr dc.Credential, public, device string, now int64) bool {
	return hexID(cr.DeviceId) && hexID(cr.CredentialId) && cr.AuthPublicKey == public && (device == "" || cr.DeviceId == device) && cr.ExpiresUnixSeconds > now && cr.ExpiresUnixSeconds < 7289654400
}
func validateOperation(op dc.Operation, device, action, id string) bool {
	if !hexID(op.OperationId) || !hexID(op.DeviceId) || op.DeviceId != device || !op.Accepted || op.Generation < 1 || op.DeadlineUnixSeconds < 1 || len(op.LastError) > 1024 || (action != "" && op.Action != action) || (id != "" && op.OperationId != id) {
		return false
	}
	switch op.Action {
	case "apply", "disable", "revoke", "expire":
	default:
		return false
	}
	switch op.State {
	case dc.Pending, dc.Degraded, dc.Done, dc.Superseded:
	default:
		return false
	}
	return !op.Effective || op.State == dc.Done
}

func resultError(command string, err error, pending *intent) Receipt {
	r := Receipt{ContractVersion: Version, Command: command, Outcome: "local_error", Code: "local_storage_failure"}
	var f *failure
	if errors.As(err, &f) {
		r.Code = f.code
		r.Outcome = "rejected"
		if f.unknown {
			r.Outcome = "result_unknown"
			r.Code = "result_unknown"
		}
	}
	if pending != nil {
		r.Pending = true
		r.RequestID = pending.RequestID
		r.IdempotencyKey = pending.IdempotencyKey
	}
	return r
}

var ErrReported = errors.New("device v2 result already reported")

func encodeReceipt(out io.Writer, r Receipt) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(r); e != nil {
		return fmt.Errorf("device v2 output failed")
	}
	if !r.OK {
		return ErrReported
	}
	return nil
}
