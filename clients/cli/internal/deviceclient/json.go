package deviceclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	dc "zongheng-vpn/shared/devicecontract"
)

var errJSON = errors.New("invalid device v2 JSON")

// DecodeReceipt is the strict consumer boundary: no aliases, duplicate fields,
// nulls, extensions, unsupported versions or accepted/effective conflation.
func DecodeReceipt(data []byte) (Receipt, error) {
	var r Receipt
	if strictDecode(data, &r, "contract_version", "command", "ok", "outcome", "pending") != nil || r.ContractVersion != Version || (!validCommand(r.Command) && r.Command != "unknown" && r.Command != StartCommand) {
		return r, errJSON
	}
	if r.OK {
		if r.Command == "unknown" {
			return r, errJSON
		}
		if r.Code != "" || r.Pending {
			return r, errJSON
		}
		compatible := (r.Command == "activate" && r.Outcome == "credential_created") || (r.Command == "rotate-credential" && r.Outcome == "credential_rotated") || ((r.Command == "recover" || r.Command == "cancel-pending") && (r.Outcome == "credential_recovered" || r.Outcome == "operation_recovered")) || (r.Command == "status" && r.Outcome == "status") || ((r.Command == "apply" || r.Command == "bind" || r.Command == "disable" || r.Command == "revoke") && r.Outcome == "accepted") || (r.Command == "cancel-pending" && r.Outcome == "cancelled")
		if !compatible {
			return r, errJSON
		}
		switch r.Outcome {
		case "cancelled":
			if r.RequestID == "" || r.Credential != nil || r.Operation != nil {
				return r, errJSON
			}
		case "credential_created", "credential_rotated", "credential_recovered":
			if r.Credential == nil || r.Operation != nil || !hexID(r.Credential.DeviceId) || !hexID(r.Credential.CredentialId) || r.Credential.ExpiresUnixSeconds < 1 || !validPublic(r.Credential.AuthPublicKey) {
				return r, errJSON
			}
		case "accepted", "status", "operation_recovered":
			if r.Operation == nil || r.Credential != nil || !validateOperation(*r.Operation, r.Operation.DeviceId, "", "") || (r.Outcome == "accepted" && r.Operation.Effective) {
				return r, errJSON
			}
			if r.Command == "bind" && r.Operation.Action != "apply" {
				return r, errJSON
			}
		default:
			return r, errJSON
		}
	} else {
		if !validReceiptCode(r.Code) || r.Credential != nil || r.Operation != nil {
			return r, errJSON
		}
		switch r.Outcome {
		case "rejected", "result_unknown", "local_error":
		default:
			return r, errJSON
		}
		if r.Outcome == "result_unknown" && (!r.Pending || r.RequestID == "" || r.Code != "result_unknown") {
			return r, errJSON
		}
	}
	if r.RequestID != "" && !hexID(r.RequestID) {
		return r, errJSON
	}
	if r.Pending && r.RequestID == "" {
		return r, errJSON
	}
	if r.IdempotencyKey != "" && !identifier(r.IdempotencyKey, 128) {
		return r, errJSON
	}
	return r, nil
}

func validReceiptCode(code string) bool {
	return inStrings(code, receiptErrorCodes)
}

func requiredFields(out any) []string {
	switch out.(type) {
	case *dc.Challenge:
		return []string{"challenge_id", "nonce", "expires_unix_seconds"}
	case *dc.Credential:
		return []string{"device_id", "credential_id", "auth_public_key", "expires_unix_seconds"}
	case *dc.Operation:
		return []string{"operation_id", "device_id", "action", "generation", "state", "accepted", "effective", "last_error", "deadline_unix_seconds"}
	case *dc.ResolveReceipt:
		return []string{"state", "kind", "request_id"}
	case *dc.ProxyBootstrap:
		return []string{"version", "device_id", "credential_id", "wireguard_public_key", "address", "desired_generation", "applied_generation", "issued_unix_seconds", "expires_unix_seconds", "profile", "profile_sha256"}
	case *dc.ProxyRouteProfile:
		return []string{"version", "authority_epoch", "managed_by", "wg_interface", "revision", "wg_endpoint", "wg_public_key", "proxy_address", "egress_id", "egress_name", "allowed_ips"}
	}
	return nil
}

func strictDecode(data []byte, out any, required ...string) error {
	if len(data) == 0 || len(data) > 1<<20 || !utf8.Valid(data) {
		return errJSON
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := jsonValue(d, 0)
	if e != nil {
		return errJSON
	}
	if _, e = d.Token(); e != io.EOF {
		return errJSON
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || !jsonShape(v, t.Elem()) {
		return errJSON
	}
	m, ok := v.(map[string]any)
	if !ok {
		return errJSON
	}
	for _, key := range required {
		if _, ok = m[key]; !ok {
			return errJSON
		}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return errJSON
	}
	return nil
}

func jsonValue(d *json.Decoder, depth int) (any, error) {
	if depth > 8 {
		return nil, errJSON
	}
	t, e := d.Token()
	if e != nil || t == nil {
		return nil, errJSON
	}
	if delim, ok := t.(json.Delim); ok {
		if delim == '[' {
			a := []any{}
			for d.More() {
				if len(a) >= 64 {
					return nil, errJSON
				}
				v, e := jsonValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			if end, e := d.Token(); e != nil || end != json.Delim(']') {
				return nil, errJSON
			}
			return a, nil
		}
		if delim != '{' {
			return nil, errJSON
		}
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			s, ok := k.(string)
			if e != nil || !ok {
				return nil, errJSON
			}
			if _, exists := m[s]; exists {
				return nil, errJSON
			}
			v, e := jsonValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		if end, e := d.Token(); e != nil || end != json.Delim('}') {
			return nil, errJSON
		}
		return m, nil
	}
	return t, nil
}
func jsonShape(v any, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		fields := map[string]reflect.Type{}
		var required []string
		switch t {
		case reflect.TypeOf(dc.Credential{}):
			required = requiredFields(&dc.Credential{})
		case reflect.TypeOf(dc.Operation{}):
			required = requiredFields(&dc.Operation{})
		case reflect.TypeOf(dc.Challenge{}):
			required = requiredFields(&dc.Challenge{})
		case reflect.TypeOf(dc.ProxyRouteProfile{}):
			required = requiredFields(&dc.ProxyRouteProfile{})
		case reflect.TypeOf(localBinding{}):
			required = []string{"private_key", "public_key", "address", "generation", "applied_generation", "operation_id"}
		}
		for _, key := range required {
			if _, ok := m[key]; !ok {
				return false
			}
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			n := strings.Split(f.Tag.Get("json"), ",")[0]
			if n != "" && n != "-" {
				fields[n] = f.Type
			}
		}
		for k, v := range m {
			ft, ok := fields[k]
			if !ok || !jsonShape(v, ft) {
				return false
			}
		}
		return true
	}
	switch t.Kind() {
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, item := range a {
			if !jsonShape(item, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.Int, reflect.Int64:
		_, ok := v.(json.Number)
		return ok
	}
	return false
}
