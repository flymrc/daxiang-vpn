package deviceclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dc "zongheng-vpn/shared/devicecontract"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

type options struct {
	command, server, caFile, operationID, key, address, idempotency, reason string
	generation                                                              int64
	activationStdin                                                         bool
	timeout                                                                 time.Duration
}

func validCommand(c string) bool {
	switch c {
	case "activate", "status", "apply", "bind", "disable", "revoke", "rotate-credential", "recover", "cancel-pending":
		return true
	}
	return false
}
func identifier(v string, max int) bool {
	return v != "" && len(v) <= max && utf8.ValidString(v) && strings.TrimSpace(v) == v && !strings.ContainsFunc(v, unicode.IsControl)
}
func parse(args []string) (options, error) {
	o := options{command: "unknown"}
	if len(args) == 0 || !validCommand(args[0]) {
		return o, &failure{code: "invalid_arguments"}
	}
	o.command = args[0]
	f := flag.NewFlagSet("device", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.server, "server", "", "explicit isolated v2 authority URL")
	f.StringVar(&o.caFile, "ca-file", "", "normal certificate verification with this CA PEM")
	f.StringVar(&o.operationID, "operation-id", "", "accepted operation identity")
	f.StringVar(&o.key, "wg-public-key", "", "local WireGuard public key only")
	f.StringVar(&o.address, "address", "", "assigned host prefix")
	f.StringVar(&o.idempotency, "idempotency-key", "", "stable mutation identity")
	f.StringVar(&o.reason, "reason", "", "bounded public audit reason")
	f.Int64Var(&o.generation, "expected-generation", -1, "explicit current desired generation")
	f.BoolVar(&o.activationStdin, "activation-stdin", false, "read activation credential from stdin")
	f.DurationVar(&o.timeout, "timeout", 15*time.Second, "whole command timeout")
	jsonOut := f.Bool("json", true, "v2 JSON receipt")
	if !uniqueFlags(f, args[1:]) || f.Parse(args[1:]) != nil || f.NArg() != 0 || !*jsonOut || o.timeout <= 0 || o.timeout > 30*time.Second {
		return o, &failure{code: "invalid_arguments"}
	}
	switch o.command {
	case "activate":
		if o.server == "" || !o.activationStdin {
			return o, &failure{code: "invalid_arguments"}
		}
	case "status":
		if !hexID(o.operationID) {
			return o, &failure{code: "invalid_arguments"}
		}
	case "apply", "bind", "disable", "revoke":
		if o.generation < 0 || o.generation == math.MaxInt64 || !identifier(o.idempotency, 128) || len(o.reason) > 1024 || !utf8.ValidString(o.reason) {
			return o, &failure{code: "invalid_arguments"}
		}
		if o.command == "bind" {
			if o.key != "" || !hostAddress(o.address) || o.generation >= dc.ProxySafeInteger {
				return o, &failure{code: "invalid_arguments"}
			}
		} else if o.command == "apply" {
			p, e := netip.ParsePrefix(o.address)
			if !validPublic(o.key) || e != nil || p.Bits() != p.Addr().BitLen() || p.String() != o.address {
				return o, &failure{code: "invalid_arguments"}
			}
		} else if o.key != "" || o.address != "" {
			return o, &failure{code: "invalid_arguments"}
		}
	}
	// Reject irrelevant options rather than silently accepting credentials or
	// generation inputs on a different operation.
	visited := map[string]bool{}
	f.Visit(func(v *flag.Flag) { visited[v.Name] = true })
	for name := range visited {
		if name == "server" || name == "ca-file" || name == "timeout" || name == "json" {
			continue
		}
		allowed := ((o.command == "activate" || o.command == "cancel-pending") && name == "activation-stdin") || (o.command == "status" && name == "operation-id") || ((o.command == "apply" || o.command == "bind" || o.command == "disable" || o.command == "revoke") && (name == "expected-generation" || name == "idempotency-key" || name == "reason")) || (o.command == "apply" && (name == "address" || name == "wg-public-key")) || (o.command == "bind" && name == "address")
		if !allowed {
			return o, &failure{code: "invalid_arguments"}
		}
	}
	return o, nil
}

// Run emits exactly one separate v2 JSON receipt. It consumes activation
// material only from bounded stdin, never argv; all errors are redacted. Root
// app maps ErrReported to its silent nonzero exit convention.
func Run(ctx context.Context, home paths.Context, args []string, stdin io.Reader, out, errOut io.Writer) error {
	_ = errOut // no sensitive parser/transport diagnostics are printed
	o, e := parse(args)
	if e != nil {
		return encodeReceipt(out, resultError(o.command, e, nil))
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	roots, e := loadRoots(ctx, o.caFile)
	if e != nil {
		return encodeReceipt(out, resultError(o.command, e, nil))
	}
	var r Receipt
	e = proxy.WithOperationLockContext(ctx, home, func() error {
		if ctx.Err() != nil {
			r = resultError(o.command, &failure{code: "command_timeout"}, nil)
			return nil
		}
		s := privateStore{home}
		st, e := s.load()
		if e != nil {
			r = resultError(o.command, e, nil)
			return nil
		}
		server := o.server
		if server == "" {
			server = st.Server
		}
		c, e := New(server, roots)
		if e != nil {
			r = resultError(o.command, e, st.Pending)
			return nil
		}
		defer c.Close()
		if st.Server != "" && c.server != st.Server {
			r = resultError(o.command, &failure{code: "server_mismatch"}, st.Pending)
			return nil
		}
		r = c.execute(ctx, s, st, o, stdin)
		return nil
	})
	if e != nil {
		r = resultError(o.command, e, nil)
	}
	return encodeReceipt(out, r)
}

func (c *Client) execute(ctx context.Context, store storage, st state, o options, stdin io.Reader) Receipt {
	if st.Pending != nil && o.command != "recover" && o.command != "cancel-pending" {
		return resultError(o.command, &failure{code: "pending_resolution_required"}, st.Pending)
	}
	if o.command == "activate" {
		if st.Credential != nil {
			return resultError(o.command, &failure{code: "already_activated"}, nil)
		}
		if stdin == nil {
			return resultError(o.command, &failure{code: "invalid_activation_input"}, nil)
		}
		material, e := io.ReadAll(io.LimitReader(stdin, 258))
		if e != nil || len(material) > 257 {
			return resultError(o.command, &failure{code: "invalid_activation_input"}, nil)
		}
		activation := strings.TrimSuffix(strings.TrimSuffix(string(material), "\n"), "\r")
		if !identifier(activation, 256) || strings.ContainsAny(activation, " \t\r\n") {
			return resultError(o.command, &failure{code: "invalid_activation_input"}, nil)
		}
		if st.PrivateKey == "" {
			_, k, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				return resultError(o.command, e, nil)
			}
			st = state{Version: Version, Server: c.server, PrivateKey: base64.StdEncoding.EncodeToString(k)}
			if e = store.save(st); e != nil {
				return resultError(o.command, e, nil)
			}
		}
		return c.activate(ctx, store, st, activation)
	}
	if o.command == "recover" {
		return c.recover(ctx, store, st)
	}
	if o.command == "cancel-pending" {
		return c.cancelPending(ctx, store, st, o, stdin)
	}
	if st.Credential == nil {
		return resultError(o.command, &failure{code: "not_activated"}, st.Pending)
	}
	if o.command == "bind" {
		if st.Credential.ExpiresUnixSeconds <= c.now().Unix() {
			return resultError(o.command, &failure{code: "credential_expired"}, nil)
		}
		if st.WireGuard != nil && o.generation < st.WireGuard.Generation {
			return resultError(o.command, &failure{code: "stale_generation"}, nil)
		}
		if st.WireGuard == nil {
			material := make([]byte, 32)
			if _, e := rand.Read(material); e != nil {
				return resultError(o.command, e, nil)
			}
			material[0] &= 248
			material[31] = (material[31] & 127) | 64
			private := base64.StdEncoding.EncodeToString(material)
			_, pub, e := wireGuardKey(private)
			if e != nil {
				return resultError(o.command, e, nil)
			}
			st.WireGuardInitialized = true
			st.WireGuard = &localBinding{PrivateKey: private, PublicKey: pub}
			// Persist before challenge or mutation; unknown results must never
			// cause a replacement key to be generated.
			if e = store.save(st); e != nil {
				return resultError(o.command, e, nil)
			}
		}
		o.key = st.WireGuard.PublicKey
	}
	key, e := privateKey(st.PrivateKey)
	if e != nil {
		return resultError(o.command, e, st.Pending)
	}
	if o.command == "status" {
		path := "/api/v2/operations/" + o.operationID
		ch, e := c.challenge(ctx, "operation.status", "GET", path, nil, st.Credential, "", key)
		if e != nil {
			return resultError(o.command, e, st.Pending)
		}
		rid, e := opaque()
		if e != nil {
			return resultError(o.command, e, st.Pending)
		}
		h, _ := headers("operation.status", "GET", path, nil, st.Credential, ch, rid, key)
		var op dc.Operation
		if e = c.call(ctx, "GET", path, nil, h, 200, &op, false); e != nil {
			return resultError(o.command, e, st.Pending)
		}
		if !validateOperation(op, st.Credential.DeviceId, "", o.operationID) {
			return resultError(o.command, &failure{code: "invalid_response"}, st.Pending)
		}
		if st.WireGuard != nil && op.OperationId == st.WireGuard.OperationID {
			if op.Action != "apply" || op.Generation != st.WireGuard.Generation {
				return resultError(o.command, &failure{code: "invalid_response"}, nil)
			}
			if op.Effective {
				st.WireGuard.AppliedGeneration = op.Generation
				if e = store.save(st); e != nil {
					return resultError(o.command, e, nil)
				}
			}
		}
		return Receipt{ContractVersion: Version, Command: o.command, OK: true, Outcome: "status", Operation: &op}
	}
	if o.command == "rotate-credential" {
		return c.rotate(ctx, store, st, key)
	}
	action := o.command
	if action == "bind" {
		action = "apply"
	}
	cmd := dc.Command{Action: dc.CommandAction(action), ExpectedGeneration: o.generation, IdempotencyKey: o.idempotency}
	if action == "apply" {
		cmd.WgPublicKey = &o.key
		cmd.Address = &o.address
	}
	if o.reason != "" {
		cmd.Reason = &o.reason
	}
	body, _ := json.Marshal(cmd)
	purpose := "command." + action
	ch, e := c.challenge(ctx, purpose, "POST", "/api/v2/commands", body, st.Credential, "", key)
	if e != nil {
		return resultError(o.command, e, st.Pending)
	}
	rid, e := opaque()
	if e != nil {
		return resultError(o.command, e, nil)
	}
	st.Pending = &intent{Kind: action, RequestID: rid, IdempotencyKey: o.idempotency, ExpectedGeneration: &o.generation}
	if o.command == "bind" {
		st.Pending.BindAddress = o.address
	}
	if e = store.save(st); e != nil {
		return resultError(o.command, e, nil)
	}
	h, _ := headers(purpose, "POST", "/api/v2/commands", body, st.Credential, ch, rid, key)
	var op dc.Operation
	e = c.call(ctx, "POST", "/api/v2/commands", body, h, 202, &op, true)
	if e == nil && (!validateOperation(op, st.Credential.DeviceId, action, "") || op.Effective || op.Generation != o.generation+1) {
		e = &failure{code: "invalid_response", unknown: true}
	}
	if e != nil {
		return c.failedMutation(store, st, e)
	}
	p := st.Pending
	if e = acceptBinding(&st, p, op); e != nil {
		return resultError(o.command, &failure{code: "invalid_response", unknown: true}, p)
	}
	st.Pending = nil
	if e = store.save(st); e != nil {
		return resultError(o.command, e, p)
	}
	return Receipt{ContractVersion: Version, Command: o.command, OK: true, Outcome: "accepted", RequestID: rid, IdempotencyKey: o.idempotency, Operation: &op}
}

func (c *Client) failedMutation(store storage, st state, e error) Receipt {
	p := st.Pending
	var f *failure
	if errorsAsFailure(e, &f) && !f.unknown {
		st.Pending = nil
		if store.save(st) == nil {
			return resultError(intentCommand(p), e, nil)
		}
		return resultError(intentCommand(p), fmt.Errorf("device intent persistence failed"), p)
	}
	return resultError(intentCommand(p), e, p)
}
func errorsAsFailure(e error, p **failure) bool {
	f, ok := e.(*failure)
	if ok {
		*p = f
	}
	return ok
}

func (c *Client) activate(ctx context.Context, store storage, st state, activation string) Receipt {
	key, e := privateKey(st.PrivateKey)
	if e != nil {
		return resultError("activate", e, nil)
	}
	pub := publicKey(key)
	body, _ := json.Marshal(dc.ActivationRequest{ActivationCredential: activation, AuthPublicKey: pub})
	ch, e := c.challenge(ctx, "activate", "POST", "/api/v2/activate", body, nil, activation, key)
	if e != nil {
		return resultError("activate", e, nil)
	}
	rid, e := opaque()
	if e != nil {
		return resultError("activate", e, nil)
	}
	st.Pending = &intent{Kind: "activate", RequestID: rid}
	if e = store.save(st); e != nil {
		return resultError("activate", e, nil)
	}
	h, _ := headers("activate", "POST", "/api/v2/activate", body, nil, ch, rid, key)
	var cr dc.Credential
	e = c.call(ctx, "POST", "/api/v2/activate", body, h, 201, &cr, true)
	if e == nil && !validateCredential(cr, pub, "", c.now().Unix()) {
		e = &failure{code: "invalid_response", unknown: true}
	}
	if e != nil {
		return c.failedMutation(store, st, e)
	}
	st.Credential = &cr
	st.Pending = nil
	if e = store.save(st); e != nil {
		return resultError("activate", e, &intent{Kind: "activate", RequestID: rid})
	}
	return Receipt{ContractVersion: Version, Command: "activate", OK: true, Outcome: "credential_created", Credential: &cr, RequestID: rid}
}

func (c *Client) rotate(ctx context.Context, store storage, st state, key ed25519.PrivateKey) Receipt {
	_, newKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return resultError("rotate-credential", e, nil)
	}
	pub := publicKey(newKey)
	body, _ := json.Marshal(dc.RotationRequest{AuthPublicKey: pub})
	ch, e := c.challenge(ctx, "credential.rotate", "POST", "/api/v2/credentials/rotate", body, st.Credential, "", key)
	if e != nil {
		return resultError("rotate-credential", e, nil)
	}
	rid, e := opaque()
	if e != nil {
		return resultError("rotate-credential", e, nil)
	}
	st.Pending = &intent{Kind: "rotate-credential", RequestID: rid, NewPrivateKey: base64.StdEncoding.EncodeToString(newKey)}
	if e = store.save(st); e != nil {
		return resultError("rotate-credential", e, nil)
	}
	h, payload := headers("credential.rotate", "POST", "/api/v2/credentials/rotate", body, st.Credential, ch, rid, key)
	h.Set("X-ZH-New-Key-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(newKey, payload)))
	var cr dc.Credential
	e = c.call(ctx, "POST", "/api/v2/credentials/rotate", body, h, 201, &cr, true)
	if e == nil && (!validateCredential(cr, pub, st.Credential.DeviceId, c.now().Unix()) || cr.ExpiresUnixSeconds > st.Credential.ExpiresUnixSeconds || cr.CredentialId == st.Credential.CredentialId) {
		e = &failure{code: "invalid_response", unknown: true}
	}
	if e != nil {
		return c.failedMutation(store, st, e)
	}
	st.PrivateKey = st.Pending.NewPrivateKey
	st.Credential = &cr
	st.Pending = nil
	if e = store.save(st); e != nil {
		return resultError("rotate-credential", e, &intent{Kind: "rotate-credential", RequestID: rid})
	}
	return Receipt{ContractVersion: Version, Command: "rotate-credential", OK: true, Outcome: "credential_rotated", Credential: &cr, RequestID: rid}
}

// recover never resubmits a mutation. The original key/request/idempotency
// intent is the authority for a read of the already committed receipt.
func (c *Client) recover(ctx context.Context, store storage, st state) Receipt {
	p := st.Pending
	if p == nil {
		return resultError("recover", &failure{code: "no_pending_intent"}, nil)
	}
	key, e := privateKey(st.PrivateKey)
	if e != nil {
		return resultError("recover", e, p)
	}
	purpose, path := "operation.receipt", "/api/v2/operations/receipt"
	var body []byte
	q := dc.ChallengeRequest{Purpose: dc.ChallengeRequestPurpose(purpose), Method: dc.POST, Path: path, ReceiptRequestId: &p.RequestID}
	credential := st.Credential
	if p.Kind == "activate" || p.Kind == "rotate-credential" {
		purpose, path = "credential.receipt", "/api/v2/credentials/receipt"
		kind := dc.CredentialReceiptRequestKind("activate")
		if p.Kind == "rotate-credential" {
			kind = dc.CredentialReceiptRequestKind("credential.rotate")
			key, e = privateKey(p.NewPrivateKey)
			if e != nil {
				return resultError("recover", e, p)
			}
		}
		pub := publicKey(key)
		body, _ = json.Marshal(dc.CredentialReceiptRequest{Kind: kind, RequestId: p.RequestID, AuthPublicKey: pub})
		challengeKind := dc.ChallengeRequestReceiptKind(kind)
		q.Purpose = dc.ChallengeRequestPurpose(purpose)
		q.Path = path
		q.AuthPublicKey = &pub
		q.ReceiptKind = &challengeKind
		credential = nil
	} else {
		if st.Credential == nil || p.ExpectedGeneration == nil {
			return resultError("recover", errJSON, p)
		}
		body, _ = json.Marshal(dc.OperationReceiptRequest{RequestId: p.RequestID, IdempotencyKey: p.IdempotencyKey})
		q.DeviceId = &st.Credential.DeviceId
		q.CredentialId = &st.Credential.CredentialId
		q.ReceiptIdempotencyKey = &p.IdempotencyKey
	}
	q.BodySha256 = bodyDigest(body)
	ch, e := c.issueChallenge(ctx, q, key)
	if e != nil {
		return resultError("recover", e, p)
	}
	rid, e := opaque()
	if e != nil {
		return resultError("recover", e, p)
	}
	h, _ := headers(purpose, "POST", path, body, credential, ch, rid, key)
	r := Receipt{ContractVersion: Version, Command: "recover", OK: true, RequestID: p.RequestID, IdempotencyKey: p.IdempotencyKey}
	if credential == nil {
		var cr dc.Credential
		if e = c.call(ctx, "POST", path, body, h, 200, &cr, false); e != nil {
			return resultError("recover", e, p)
		}
		expectedDevice := ""
		if st.Credential != nil {
			expectedDevice = st.Credential.DeviceId
		}
		if !validateCredential(cr, publicKey(key), expectedDevice, c.now().Unix()) || (st.Credential != nil && (cr.ExpiresUnixSeconds > st.Credential.ExpiresUnixSeconds || cr.CredentialId == st.Credential.CredentialId)) {
			return resultError("recover", &failure{code: "invalid_response"}, p)
		}
		st.PrivateKey = base64.StdEncoding.EncodeToString(key)
		st.Credential = &cr
		r.Outcome = "credential_recovered"
		r.Credential = &cr
	} else {
		var op dc.Operation
		if e = c.call(ctx, "POST", path, body, h, 200, &op, false); e != nil {
			return resultError("recover", e, p)
		}
		if !validateOperation(op, credential.DeviceId, p.Kind, "") || op.Generation != *p.ExpectedGeneration+1 {
			return resultError("recover", &failure{code: "invalid_response"}, p)
		}
		r.Outcome = "operation_recovered"
		r.Operation = &op
		if e = acceptBinding(&st, p, op); e != nil {
			return resultError("recover", &failure{code: "invalid_response"}, p)
		}
	}
	st.Pending = nil
	if e = store.save(st); e != nil {
		return resultError("recover", e, p)
	}
	return r
}
