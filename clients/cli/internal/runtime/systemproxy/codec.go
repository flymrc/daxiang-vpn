package systemproxy

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"zongheng-vpn/shared/contracts"

	osproxy "zongheng-vpn/shared/systemproxy"
)

const journalVersion = 2
const maxJournalBytes = 1 << 20
const manualRecovery = "记录已保留。请先手动核对并恢复 Windows 代理设置，再将记录重命名归档，最后重试断开或退出"

// NumericBytes deliberately does not use encoding/json's []byte base64
// representation. Both readers and writers accept numeric arrays only.
type NumericBytes []byte

func (b NumericBytes) MarshalJSON() ([]byte, error) {
	values := make([]uint16, len(b))
	for i, value := range b {
		values[i] = uint16(value)
	}
	return json.Marshal(values)
}

func (b *NumericBytes) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '[' {
		return fmt.Errorf("registry bytes must be a numeric array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	result := make(NumericBytes, len(values))
	for i, raw := range values {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
			return fmt.Errorf("registry byte must be an unsigned integer")
		}
		var value uint16
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if value > 255 {
			return fmt.Errorf("registry byte exceeds 255")
		}
		result[i] = byte(value)
	}
	*b = result
	return nil
}

type rawValue struct {
	Kind  uint32       `json:"kind"`
	Bytes NumericBytes `json:"bytes"`
}
type fieldChange struct {
	Name     string    `json:"name"`
	Original *rawValue `json:"original"`
	Written  *rawValue `json:"written"`
}
type journal struct {
	SchemaVersion int           `json:"schema_version"`
	Owner         string        `json:"owner"`
	Scope         string        `json:"scope"`
	LeaseID       string        `json:"lease_id"`
	LeaseOwner    Owner         `json:"lease_owner"`
	Fields        []fieldChange `json:"fields"`
}

func exactHex(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(value) == size*2 && len(decoded) == size && value == strings.ToLower(value)
}

func validOwner(owner Owner) bool {
	return owner.UserScope != "" && owner.Home != "" && owner.Engine.Home == owner.Home &&
		exactHex(owner.Engine.InstanceID, 16) && exactHex(owner.Engine.Generation, 32) &&
		owner.Engine.ProtocolVersion == contracts.ControlProtocolVersion && owner.Engine.PID > 0
}

func incompatible(code, reason string) error {
	return failure(code, reason+"；"+manualRecovery, nil)
}

func decode(data []byte) (journal, error) {
	var result journal
	if len(data) == 0 || len(data) > maxJournalBytes {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录大小无效")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录 JSON 无效或字段重复")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil || envelope == nil {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录格式无效")
	}
	var version int
	if json.Unmarshal(envelope["schema_version"], &version) != nil {
		return result, incompatible("system_proxy_legacy_migration_required", "记录缺少可验证的 CLI 租约归属，不能自动迁移")
	}
	if version == 1 {
		return result, incompatible("system_proxy_legacy_migration_required", "GUI v1 记录没有 SID、home 与引擎身份，不能认领或自动迁移")
	}
	if version != journalVersion {
		return result, incompatible("system_proxy_journal_incompatible", "代理恢复记录版本不兼容")
	}
	if !exactKeys(envelope, "schema_version", "owner", "scope", "lease_id", "lease_owner", "fields") {
		return result, incompatible("system_proxy_journal_invalid", "记录字段名称必须使用合同中的精确大小写")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录字段格式无效")
	}
	if result.Owner != "cli-runtime" || result.Scope != "wininet-hkcu" || !exactHex(result.LeaseID, 16) || !validOwner(result.LeaseOwner) {
		return result, incompatible("system_proxy_journal_incompatible", "代理恢复记录归属无效")
	}
	var ownerFields, engineFields map[string]json.RawMessage
	if json.Unmarshal(envelope["lease_owner"], &ownerFields) != nil || !exactKeys(ownerFields, "user_scope", "home", "engine_identity") ||
		json.Unmarshal(ownerFields["engine_identity"], &engineFields) != nil || !exactKeys(engineFields, "instance_id", "home", "config_generation", "control_protocol_version", "pid") {
		return result, incompatible("system_proxy_journal_invalid", "归属或引擎字段名称无效")
	}
	if len(result.Fields) != len(osproxy.Fields) {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录字段数量无效")
	}
	// Missing and null are different in the raw registry value contract. The
	// optional original/written values must still be explicitly present.
	var rawFields []map[string]json.RawMessage
	if json.Unmarshal(envelope["fields"], &rawFields) != nil || len(rawFields) != len(result.Fields) {
		return result, incompatible("system_proxy_journal_invalid", "代理恢复记录字段无效")
	}
	for i, field := range result.Fields {
		if field.Name != osproxy.Fields[i] || !exactKeys(rawFields[i], "name", "original", "written") {
			return result, incompatible("system_proxy_journal_invalid", "代理恢复记录字段名称或存在性无效")
		}
		for _, key := range []string{"original", "written"} {
			var raw map[string]json.RawMessage
			if bytes.Equal(bytes.TrimSpace(rawFields[i][key]), []byte("null")) {
				continue
			}
			if json.Unmarshal(rawFields[i][key], &raw) != nil || !exactKeys(raw, "kind", "bytes") || bytes.Equal(bytes.TrimSpace(raw["kind"]), []byte("null")) {
				return result, incompatible("system_proxy_journal_invalid", "原始注册表值缺少类型或字节")
			}
		}
		for _, value := range []*rawValue{field.Original, field.Written} {
			if value != nil && value.Kind > 11 {
				return result, incompatible("system_proxy_journal_invalid", "原始注册表类型无效")
			}
		}
	}
	return result, nil
}

func exactKeys(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if fields[name] == nil {
			return false
		}
	}
	return true
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds journal bound")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[strings.ToLower(name)] {
					return fmt.Errorf("duplicate or invalid object field")
				}
				seen[strings.ToLower(name)] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func toRaw(value *osproxy.Value) *rawValue {
	if value == nil {
		return nil
	}
	return &rawValue{Kind: value.Kind, Bytes: append(NumericBytes{}, value.Bytes...)}
}
func fromRaw(value *rawValue) *osproxy.Value {
	if value == nil {
		return nil
	}
	return &osproxy.Value{Kind: value.Kind, Bytes: append([]byte{}, value.Bytes...)}
}
