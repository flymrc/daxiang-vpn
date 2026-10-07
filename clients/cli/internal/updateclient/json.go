package updateclient

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

func exactHex(s string, bytes int) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == bytes && hex.EncodeToString(decoded) == s
}

// All nested fields have exact casing, required non-omitempty members and no
// nulls. Both depth and total members are bounded before typed allocation.
func strictDecode(data []byte, out any, limit int) error {
	if len(data) == 0 || len(data) > limit || !utf8.Valid(data) {
		return fail("corrupt_state")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	members := 0
	v, err := jsonValue(d, 0, &members)
	if err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return fail("corrupt_state")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || !jsonShape(v, t.Elem()) {
		return fail("corrupt_state")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return fail("corrupt_state")
	}
	return nil
}

func jsonValue(d *json.Decoder, depth int, members *int) (any, error) {
	if depth > 8 || *members > 4096 {
		return nil, fail("corrupt_state")
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return nil, fail("corrupt_state")
	}
	if delimiter, ok := t.(json.Delim); ok {
		switch delimiter {
		case '{':
			m := map[string]any{}
			for d.More() {
				*members++
				key, e := d.Token()
				k, ok := key.(string)
				if e != nil || !ok {
					return nil, fail("corrupt_state")
				}
				if _, exists := m[k]; exists {
					return nil, fail("corrupt_state")
				}
				v, e := jsonValue(d, depth+1, members)
				if e != nil {
					return nil, e
				}
				m[k] = v
			}
			if end, e := d.Token(); e != nil || end != json.Delim('}') {
				return nil, fail("corrupt_state")
			}
			return m, nil
		case '[':
			values := []any{}
			for d.More() {
				*members++
				v, e := jsonValue(d, depth+1, members)
				if e != nil {
					return nil, e
				}
				values = append(values, v)
			}
			if end, e := d.Token(); e != nil || end != json.Delim(']') {
				return nil, fail("corrupt_state")
			}
			return values, nil
		}
		return nil, fail("corrupt_state")
	}
	return t, nil
}

func jsonShape(v any, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] != "" && tag[0] != "-" {
				fields[tag[0]] = f
				if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
					if _, exists := m[tag[0]]; !exists {
						return false
					}
				}
			}
		}
		for k, value := range m {
			f, exists := fields[k]
			if !exists || !jsonShape(value, f.Type) {
				return false
			}
		}
		return true
	case reflect.Slice:
		values, ok := v.([]any)
		if !ok || len(values) > 16 {
			return false
		}
		for _, value := range values {
			if !jsonShape(value, t.Elem()) {
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
	case reflect.Int, reflect.Int64, reflect.Uint64:
		_, ok := v.(json.Number)
		return ok
	}
	return false
}
