package devicecontract

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"unicode/utf8"
)

// The canonical profile is flat: exact property names, all required, no
// duplicate/unknown/null aliases, and exactly one string allowed_ips entry.
// Token validation bounds input and tree allocation before typed decoding.
func DecodeProxyRouteProfile(data []byte) (ProxyRouteProfile, error) {
	var p ProxyRouteProfile
	if len(data) == 0 || len(data) > 32768 || !utf8.Valid(data) {
		return p, ErrProxyProfile
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return p, ErrProxyProfile
	}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(p)
	for i := 0; i < typ.NumField(); i++ {
		allowed[typ.Field(i).Tag.Get("json")] = true
	}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		name, ok := t.(string)
		if err != nil || !ok || !allowed[name] || seen[name] {
			return p, ErrProxyProfile
		}
		seen[name] = true
		t, err = d.Token()
		if err != nil || t == nil {
			return p, ErrProxyProfile
		}
		if name == "allowed_ips" {
			if t != json.Delim('[') || !d.More() {
				return p, ErrProxyProfile
			}
			t, err = d.Token()
			if _, ok := t.(string); err != nil || !ok || d.More() {
				return p, ErrProxyProfile
			}
			if t, err = d.Token(); err != nil || t != json.Delim(']') {
				return p, ErrProxyProfile
			}
		} else if name == "version" || name == "revision" {
			if _, ok := t.(json.Number); !ok {
				return p, ErrProxyProfile
			}
		} else if _, ok := t.(string); !ok {
			return p, ErrProxyProfile
		}
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') || len(seen) != len(allowed) {
		return p, ErrProxyProfile
	}
	if _, err = d.Token(); err != io.EOF {
		return p, ErrProxyProfile
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || ValidateProxyRouteProfile(p) != nil {
		return ProxyRouteProfile{}, ErrProxyProfile
	}
	return p, nil
}
