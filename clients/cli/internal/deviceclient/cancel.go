package deviceclient

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	dc "zongheng-vpn/shared/devicecontract"
)

// cancelPending explicitly resolves under the Hub's mutation fence. A negative
// tombstone, not an absent receipt or HTTP error, is required before discarding
// any pending key. If committed, this recovers the original receipt instead.
func (c *Client) cancelPending(ctx context.Context, store storage, st state, o options, stdin io.Reader) Receipt {
	p := st.Pending
	if p == nil {
		return resultError(o.command, &failure{code: "no_pending_intent"}, nil)
	}
	key, e := privateKey(st.PrivateKey)
	if e != nil {
		return resultError(o.command, e, p)
	}
	var ownerKey ed25519.PrivateKey
	kind := p.Kind
	q := dc.ChallengeRequest{Purpose: dc.ChallengeRequestPurpose("request.resolve"), Method: dc.POST, Path: "/api/v2/requests/resolve", ReceiptRequestId: &p.RequestID}
	body := dc.ResolveRequest{RequestId: p.RequestID}
	credential := st.Credential
	if p.Kind == "activate" {
		if !o.activationStdin || stdin == nil {
			return resultError(o.command, &failure{code: "invalid_activation_input"}, p)
		}
		data, re := io.ReadAll(io.LimitReader(stdin, 258))
		material := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		if re != nil || len(data) > 257 || !identifier(material, 256) || strings.ContainsAny(material, " \t\r\n") {
			return resultError(o.command, &failure{code: "invalid_activation_input"}, p)
		}
		body.ActivationCredential = &material
		q.ActivationCredential = &material
		credential = nil
	} else {
		if o.activationStdin {
			return resultError(o.command, &failure{code: "invalid_arguments"}, p)
		}
		if st.Credential == nil {
			return resultError(o.command, errJSON, p)
		}
		q.DeviceId = &st.Credential.DeviceId
		q.CredentialId = &st.Credential.CredentialId
		if p.Kind == "rotate-credential" {
			kind = "credential.rotate"
			ownerKey = key
			key, e = privateKey(p.NewPrivateKey)
			if e != nil {
				return resultError(o.command, e, p)
			}
		} else {
			kind = "command." + p.Kind
			body.IdempotencyKey = &p.IdempotencyKey
			q.ReceiptIdempotencyKey = &p.IdempotencyKey
		}
	}
	pub := publicKey(key)
	if p.Kind == "activate" || p.Kind == "rotate-credential" {
		body.AuthPublicKey = &pub
		q.AuthPublicKey = &pub
	}
	body.Kind = dc.ResolveRequestKind(kind)
	receiptKind := dc.ChallengeRequestReceiptKind(kind)
	q.ReceiptKind = &receiptKind
	raw, _ := json.Marshal(body)
	q.BodySha256 = bodyDigest(raw)
	ch, e := c.issueChallengeWithOwner(ctx, q, key, ownerKey)
	if e != nil {
		return resultError(o.command, e, p)
	}
	rid, e := opaque()
	if e != nil {
		return resultError(o.command, e, p)
	}
	h, payload := headers("request.resolve", "POST", q.Path, raw, credential, ch, rid, key)
	if ownerKey != nil {
		h.Set("X-ZH-Resolve-Owner-Proof", base64.StdEncoding.EncodeToString(ed25519.Sign(ownerKey, payload)))
	}
	var resolved dc.ResolveReceipt
	if e = c.call(ctx, "POST", q.Path, raw, h, 200, &resolved, true); e != nil {
		return resultError(o.command, e, p)
	}
	if string(resolved.Kind) != kind || resolved.RequestId != p.RequestID || (resolved.IdempotencyKey != nil && *resolved.IdempotencyKey != p.IdempotencyKey) || (resolved.AuthPublicKey != nil && *resolved.AuthPublicKey != pub) {
		return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
	}
	if p.Kind == "activate" || p.Kind == "rotate-credential" {
		if resolved.AuthPublicKey == nil || *resolved.AuthPublicKey != pub || resolved.IdempotencyKey != nil {
			return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
		}
	} else if resolved.AuthPublicKey != nil || resolved.IdempotencyKey == nil || *resolved.IdempotencyKey != p.IdempotencyKey {
		return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
	}
	r := Receipt{ContractVersion: Version, Command: o.command, OK: true, RequestID: p.RequestID, IdempotencyKey: p.IdempotencyKey}
	switch string(resolved.State) {
	case "cancelled":
		if resolved.Credential != nil || resolved.Operation != nil {
			return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
		}
		if p.Kind == "activate" {
			st.PrivateKey = ""
		}
		r.Outcome = "cancelled"
	case "committed":
		if p.Kind == "activate" || p.Kind == "rotate-credential" {
			cr := resolved.Credential
			device := ""
			if st.Credential != nil {
				device = st.Credential.DeviceId
			}
			// This endpoint returns an existing historical credential. Expiry is
			// preserved, never represented as currently usable authorization.
			if cr == nil || resolved.Operation != nil || !validateCredential(*cr, pub, device, 0) || (st.Credential != nil && (cr.ExpiresUnixSeconds > st.Credential.ExpiresUnixSeconds || cr.CredentialId == st.Credential.CredentialId)) {
				return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
			}
			st.PrivateKey = base64.StdEncoding.EncodeToString(key)
			st.Credential = cr
			r.Credential = cr
			r.Outcome = "credential_recovered"
		} else {
			op := resolved.Operation
			if op == nil || resolved.Credential != nil || st.Credential == nil || p.ExpectedGeneration == nil || !validateOperation(*op, st.Credential.DeviceId, p.Kind, "") || op.Generation != *p.ExpectedGeneration+1 {
				return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
			}
			r.Operation = op
			r.Outcome = "operation_recovered"
			if e = acceptBinding(&st, p, *op); e != nil {
				return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
			}
		}
	default:
		return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
	}
	st.Pending = nil
	if e = store.save(st); e != nil {
		return resultError(o.command, e, p)
	}
	return r
}
