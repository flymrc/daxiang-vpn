// Package deviceapi exposes an explicit opt-in TLS listener. It never trusts
// forwarded headers or connects this authority to legacy tokens/privileged APIs.
package deviceapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"zongheng-vpn/hub/internal/deviceauth"
	generated "zongheng-vpn/shared/devicecontract"
)

type Server struct {
	store  *deviceauth.Store
	mux    *http.ServeMux
	slots  chan struct{}
	rateMu sync.Mutex
	window time.Time
	count  int
}

func NewServer(store *deviceauth.Store) (*Server, error) {
	if store == nil {
		return nil, deviceauth.ErrInvalid
	}
	s := &Server{store: store, mux: http.NewServeMux(), slots: make(chan struct{}, 32)}
	s.mux.HandleFunc("POST /api/v2/challenges", s.challenge)
	s.mux.HandleFunc("POST /api/v2/activate", s.activate)
	s.mux.HandleFunc("POST /api/v2/commands", s.command)
	s.mux.HandleFunc("POST /api/v2/credentials/rotate", s.rotate)
	s.mux.HandleFunc("POST /api/v2/credentials/receipt", s.credentialReceipt)
	s.mux.HandleFunc("POST /api/v2/operations/receipt", s.operationReceipt)
	s.mux.HandleFunc("POST /api/v2/requests/resolve", s.resolve)
	s.mux.HandleFunc("GET /api/v2/operations/{operation_id}", s.operation)
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.TLS == nil {
		replyError(w, http.StatusUnauthorized, generated.Unauthorized)
		return
	}
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || strings.Contains(r.URL.Path, "//") {
		replyError(w, 400, generated.InvalidRequest)
		return
	}
	s.rateMu.Lock()
	now := time.Now()
	if now.Sub(s.window) >= time.Second {
		s.window = now
		s.count = 0
	}
	s.count++
	limited := s.count > 100
	s.rateMu.Unlock()
	if limited {
		replyError(w, 429, generated.RateLimited)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		replyError(w, 429, generated.RateLimited)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	s.mux.ServeHTTP(w, r.WithContext(ctx))
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func replyError(w http.ResponseWriter, status int, code generated.ErrorCode) {
	reply(w, status, generated.Error{Code: code})
}
func failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deviceauth.ErrUnauthorized), errors.Is(err, deviceauth.ErrExpired):
		replyError(w, 401, generated.Unauthorized)
	case errors.Is(err, deviceauth.ErrInvalid), errors.Is(err, deviceauth.ErrProtected):
		replyError(w, 400, generated.InvalidRequest)
	case errors.Is(err, deviceauth.ErrGeneration), errors.Is(err, deviceauth.ErrConflict):
		replyError(w, 409, generated.Conflict)
	case deviceauth.IsNotFound(err):
		replyError(w, 404, generated.NotFound)
	case errors.Is(err, deviceauth.ErrOverdue), errors.Is(err, deviceauth.ErrQuota):
		replyError(w, 429, generated.RateLimited)
	default:
		replyError(w, 503, generated.Unavailable)
	}
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Reject duplicate keys, nulls, omitted required fields and unknown fields. The
// generated Go type alone does not enforce those OpenAPI constraints.
func decode(w http.ResponseWriter, r *http.Request, out any, required ...string) ([]byte, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, deviceauth.ErrInvalid
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		return nil, deviceauth.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, deviceauth.ErrInvalid
	}
	fields := map[string]bool{}
	// encoding/json accepts case-insensitive aliases even with
	// DisallowUnknownFields. OpenAPI property names are exact; reject aliases
	// before typed decoding so a second spelling cannot overwrite a command.
	allowed := map[string]bool{}
	target := reflect.TypeOf(out)
	if target == nil || target.Kind() != reflect.Pointer || target.Elem().Kind() != reflect.Struct {
		return nil, deviceauth.ErrInvalid
	}
	target = target.Elem()
	for i := 0; i < target.NumField(); i++ {
		name := strings.Split(target.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			allowed[name] = true
		}
	}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return nil, deviceauth.ErrInvalid
		}
		key, ok := tok.(string)
		if !ok || !allowed[key] || fields[key] {
			return nil, deviceauth.ErrInvalid
		}
		fields[key] = true
		var v any
		if err = d.Decode(&v); err != nil || v == nil {
			return nil, deviceauth.ErrInvalid
		}
		switch v.(type) {
		case map[string]any, []any:
			return nil, deviceauth.ErrInvalid
		}
	}
	if _, err = d.Token(); err != nil {
		return nil, deviceauth.ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, deviceauth.ErrInvalid
	}
	for _, key := range required {
		if !fields[key] {
			return nil, deviceauth.ErrInvalid
		}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return nil, deviceauth.ErrInvalid
	}
	return data, nil
}
func header(r *http.Request, name string) (string, error) {
	v := r.Header.Values(name)
	if len(v) != 1 || v[0] == "" || len(v[0]) > 256 || strings.TrimSpace(v[0]) != v[0] {
		return "", deviceauth.ErrUnauthorized
	}
	return v[0], nil
}
func signed(r *http.Request, purpose string, body []byte, activation bool) (deviceauth.SignedRequest, error) {
	q := deviceauth.SignedRequest{Purpose: purpose, Method: r.Method, Path: r.URL.Path, BodyDigest: deviceauth.BodyDigest(body)}
	var err error
	for name, p := range map[string]*string{"X-ZH-Challenge": &q.ChallengeID, "X-ZH-Nonce": &q.Nonce, "X-ZH-Request-ID": &q.RequestID, "X-ZH-Signature": &q.Signature} {
		*p, err = header(r, name)
		if err != nil {
			return q, err
		}
	}
	expires, err := header(r, "X-ZH-Expires")
	if err != nil {
		return q, err
	}
	q.Expires, err = strconv.ParseInt(expires, 10, 64)
	if err != nil || strconv.FormatInt(q.Expires, 10) != expires {
		return q, deviceauth.ErrUnauthorized
	}
	if !activation {
		q.DeviceID, err = header(r, "X-ZH-Device")
		if err != nil {
			return q, err
		}
		q.CredentialID, err = header(r, "X-ZH-Credential")
		if err != nil {
			return q, err
		}
	} else if r.Header.Get("X-ZH-Device") != "" || r.Header.Get("X-ZH-Credential") != "" {
		return q, deviceauth.ErrUnauthorized
	}
	return q, nil
}
func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	var req generated.ChallengeRequest
	body, err := decode(w, r, &req, "purpose", "method", "path", "body_sha256")
	if err != nil {
		failure(w, err)
		return
	}
	proof, err := header(r, "X-ZH-Challenge-Proof")
	if err != nil {
		failure(w, err)
		return
	}
	clientNonce, err := header(r, "X-ZH-Client-Nonce")
	if err != nil {
		failure(w, err)
		return
	}
	expiresText, err := header(r, "X-ZH-Client-Expires")
	if err != nil {
		failure(w, err)
		return
	}
	clientExpires, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || strconv.FormatInt(clientExpires, 10) != expiresText {
		failure(w, deviceauth.ErrUnauthorized)
		return
	}
	if len(req.Path) > 256 || len(value(req.ActivationCredential)) > 256 || len(value(req.DeviceId)) > 64 || len(value(req.CredentialId)) > 64 {
		failure(w, deviceauth.ErrInvalid)
		return
	}
	var receiptKind string
	if req.ReceiptKind != nil {
		receiptKind = string(*req.ReceiptKind)
	}
	var ownerProof string
	if len(r.Header.Values("X-ZH-Resolve-Owner-Proof")) > 0 {
		ownerProof, err = header(r, "X-ZH-Resolve-Owner-Proof")
		if err != nil {
			failure(w, err)
			return
		}
	}
	ch, err := s.store.Challenge(r.Context(), deviceauth.ChallengeRequest{Purpose: string(req.Purpose), Method: string(req.Method), Path: req.Path, BodyDigest: req.BodySha256, DeviceID: value(req.DeviceId), CredentialID: value(req.CredentialId), ActivationCredential: value(req.ActivationCredential), PublicKey: value(req.AuthPublicKey), ProofDigest: deviceauth.BodyDigest(body), Proof: proof, ClientNonce: clientNonce, ClientExpires: clientExpires, ReceiptRequestID: value(req.ReceiptRequestId), ReceiptKind: receiptKind, ReceiptIdempotencyKey: value(req.ReceiptIdempotencyKey), ResolveOwnerProof: ownerProof})
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, generated.Challenge{ChallengeId: ch.ID, Nonce: ch.Nonce, ExpiresUnixSeconds: ch.Expires})
}
func credential(cr deviceauth.Credential) generated.Credential {
	return generated.Credential{DeviceId: cr.DeviceID, CredentialId: cr.ID, AuthPublicKey: cr.PublicKey, ExpiresUnixSeconds: cr.ValidUntil.Unix()}
}
func (s *Server) activate(w http.ResponseWriter, r *http.Request) {
	var req generated.ActivationRequest
	body, err := decode(w, r, &req, "activation_credential", "auth_public_key")
	if err != nil {
		failure(w, err)
		return
	}
	q, err := signed(r, "activate", body, true)
	if err != nil {
		failure(w, err)
		return
	}
	cr, err := s.store.Activate(r.Context(), q, req.ActivationCredential, req.AuthPublicKey)
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 201, credential(cr))
}
func operation(op deviceauth.Operation, effective bool) generated.Operation {
	return generated.Operation{OperationId: op.ID, DeviceId: op.DeviceID, Action: op.Action, Generation: op.Generation, State: generated.OperationState(op.State), Accepted: true, Effective: effective, LastError: op.LastError, DeadlineUnixSeconds: op.Deadline.Unix()}
}
func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var req generated.Command
	body, err := decode(w, r, &req, "action", "idempotency_key", "expected_generation")
	if err != nil {
		failure(w, err)
		return
	}
	if len(req.IdempotencyKey) > 128 || len(value(req.Address)) > 64 || len(value(req.Reason)) > 1024 {
		failure(w, deviceauth.ErrInvalid)
		return
	}
	q, err := signed(r, "command."+string(req.Action), body, false)
	if err != nil {
		failure(w, err)
		return
	}
	op, err := s.store.SubmitSigned(r.Context(), q, deviceauth.Command{Action: string(req.Action), IdempotencyKey: req.IdempotencyKey, ExpectedGeneration: req.ExpectedGeneration, PublicKey: value(req.WgPublicKey), Address: value(req.Address), Reason: value(req.Reason)})
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 202, operation(op, false))
}
func (s *Server) operation(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > 0 || r.TransferEncoding != nil {
		failure(w, deviceauth.ErrInvalid)
		return
	}
	id := r.PathValue("operation_id")
	if len(id) != 32 {
		failure(w, deviceauth.ErrInvalid)
		return
	}
	q, err := signed(r, "operation.status", nil, false)
	if err != nil {
		failure(w, err)
		return
	}
	op, effective, err := s.store.OperationSigned(r.Context(), q, id)
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, operation(op, effective))
}
func (s *Server) rotate(w http.ResponseWriter, r *http.Request) {
	var req generated.RotationRequest
	body, err := decode(w, r, &req, "auth_public_key")
	if err != nil {
		failure(w, err)
		return
	}
	q, err := signed(r, "credential.rotate", body, false)
	if err != nil {
		failure(w, err)
		return
	}
	proof, err := header(r, "X-ZH-New-Key-Signature")
	if err != nil {
		failure(w, err)
		return
	}
	cr, err := s.store.RotateCredential(r.Context(), q, req.AuthPublicKey, proof)
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 201, credential(cr))
}

func (s *Server) credentialReceipt(w http.ResponseWriter, r *http.Request) {
	var req generated.CredentialReceiptRequest
	body, err := decode(w, r, &req, "kind", "request_id", "auth_public_key")
	if err != nil {
		failure(w, err)
		return
	}
	q, err := signed(r, "credential.receipt", body, true)
	if err != nil {
		failure(w, err)
		return
	}
	cr, err := s.store.CredentialReceipt(r.Context(), q, string(req.Kind), req.RequestId, req.AuthPublicKey)
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, credential(cr))
}

func (s *Server) operationReceipt(w http.ResponseWriter, r *http.Request) {
	var req generated.OperationReceiptRequest
	body, err := decode(w, r, &req, "request_id", "idempotency_key")
	if err != nil {
		failure(w, err)
		return
	}
	q, err := signed(r, "operation.receipt", body, false)
	if err != nil {
		failure(w, err)
		return
	}
	op, effective, err := s.store.OperationReceipt(r.Context(), q, req.RequestId, req.IdempotencyKey)
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, 200, operation(op, effective))
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	var req generated.ResolveRequest
	body, err := decode(w, r, &req, "kind", "request_id")
	if err != nil {
		failure(w, err)
		return
	}
	if len(value(req.ActivationCredential)) > 256 {
		failure(w, deviceauth.ErrInvalid)
		return
	}
	kind := string(req.Kind)
	q, err := signed(r, "request.resolve", body, kind == "activate")
	if err != nil {
		failure(w, err)
		return
	}
	var ownerProof string
	if kind == "credential.rotate" || len(r.Header.Values("X-ZH-Resolve-Owner-Proof")) > 0 {
		ownerProof, err = header(r, "X-ZH-Resolve-Owner-Proof")
		if err != nil {
			failure(w, err)
			return
		}
	}
	got, err := s.store.ResolvePending(r.Context(), q, deviceauth.ResolveRequest{Kind: kind, RequestID: req.RequestId, IdempotencyKey: value(req.IdempotencyKey), PublicKey: value(req.AuthPublicKey), ActivationCredential: value(req.ActivationCredential)}, ownerProof)
	if err != nil {
		failure(w, err)
		return
	}
	result := generated.ResolveReceipt{State: generated.ResolveReceiptState(got.State), Kind: generated.ResolveReceiptKind(got.Kind), RequestId: got.RequestID}
	if got.PublicKey != "" {
		result.AuthPublicKey = &got.PublicKey
	}
	if got.IdempotencyKey != "" {
		result.IdempotencyKey = &got.IdempotencyKey
	}
	if got.Credential != nil {
		cr := credential(*got.Credential)
		result.Credential = &cr
	}
	if got.Operation != nil {
		op := operation(*got.Operation, got.Effective)
		result.Operation = &op
	}
	reply(w, 200, result)
}
