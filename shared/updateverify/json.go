package updateverify

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

const signingDomain = "zhvpn/update-metadata/v1\x00"

type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	KeyID         string          `json:"key_id"`
	Metadata      json.RawMessage `json:"metadata"`
	Signature     string          `json:"signature"`
}

func fieldNames(t reflect.Type) []string {
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Tag.Get("json")
	}
	return names
}

func strictObject(data []byte, names []string) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 4 {
			return ErrMetadata
		}
		t, err := d.Token()
		if err != nil || t == nil {
			return ErrMetadata
		}
		if delimiter, ok := t.(json.Delim); ok {
			if delimiter != '{' {
				return ErrMetadata
			}
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				name, ok := t.(string)
				if err != nil || !ok || len(name) > 64 || seen[name] || len(seen) >= 32 {
					return ErrMetadata
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			if t, err := d.Token(); err != nil || t != json.Delim('}') {
				return ErrMetadata
			}
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrMetadata
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != len(names) {
		return ErrMetadata
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return ErrMetadata
		}
	}
	return nil
}

func decodeMetadata(raw []byte) (Metadata, error) {
	var m Metadata
	if len(raw) == 0 || len(raw) > MaxMetadataBytes || strictObject(raw, fieldNames(reflect.TypeFor[Metadata]())) != nil {
		return m, ErrMetadata
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, ErrMetadata
	}
	canonical, err := CanonicalMetadata(m)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Metadata{}, fmt.Errorf("%w: signed metadata must be canonical", ErrMetadata)
	}
	return m, nil
}

// CanonicalMetadata serializes validated metadata in the documented struct field
// order with Go encoding/json compact UTF-8 rules. All strings are bounded ASCII.
func CanonicalMetadata(m Metadata) ([]byte, error) {
	if err := validateMetadata(m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// SigningMessage frames domain || uint32BE(key_id byte length) || key_id ||
// uint32BE(canonical metadata byte length) || canonical metadata. It contains no
// private key and does not sign/publish. Exact framing is shared by publishers.
func SigningMessage(keyID string, rawMetadata []byte) ([]byte, error) {
	if !validKeyID(keyID) {
		return nil, ErrMetadata
	}
	if _, err := decodeMetadata(rawMetadata); err != nil {
		return nil, err
	}
	return signingBytes(keyID, rawMetadata), nil
}

func signingBytes(keyID string, raw []byte) []byte {
	result := make([]byte, 0, len(signingDomain)+8+len(keyID)+len(raw))
	result = append(result, signingDomain...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(keyID)))
	result = append(result, keyID...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(raw)))
	return append(result, raw...)
}
