package proxy

import (
	"encoding/json"
	"reflect"
)

// EngineLogSchema deterministically projects the typed Go record and finite
// event/code pairing into the checked-in schema. Canonical compact encoding
// and LF framing are additionally enforced by DecodeEngineLogRecord.
func EngineLogSchema() []byte {
	stringShape := func(pattern string, max int) map[string]any {
		return map[string]any{"type": "string", "pattern": pattern, "maxLength": max}
	}
	properties := map[string]any{
		"version":        map[string]any{"const": EngineLogVersion},
		"sequence":       map[string]any{"type": "integer", "minimum": 1, "maximum": engineLogMaxSequence},
		"time_unix_nano": map[string]any{"type": "integer", "minimum": 1, "maximum": int64(1<<63 - 1)},
		"event":          map[string]any{"type": "string"}, "code": map[string]any{"type": "string"},
		"build_id":    stringShape(`^([0-9a-f]{40}|[0-9a-f]{64})?$`, 64),
		"instance_id": stringShape(`^([0-9a-f]{32})?$`, 32),
		"generation":  stringShape(`^([0-9a-f]{64})?$`, 64),
		"count":       map[string]any{"type": "integer", "minimum": 0, "maximum": engineLogMaxSequence},
	}
	var required []string
	t := reflect.TypeFor[EngineLogRecord]()
	for i := 0; i < t.NumField(); i++ {
		required = append(required, t.Field(i).Tag.Get("json"))
	}
	var pairs []any
	for _, rule := range engineLogRules {
		count := map[string]any{"const": 0}
		if rule.counted {
			count = map[string]any{"minimum": 1}
		}
		pairs = append(pairs, map[string]any{"properties": map[string]any{"event": map[string]any{"const": rule.event}, "code": map[string]any{"enum": rule.codes}, "count": count}})
	}
	schema := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "title": "ZHVPN dedicated engine event v1", "description": "Exact compact Go struct JSON plus LF, with no unknown, duplicate, missing, null, alias, whitespace or free-text fields; maximum framed record 2048 bytes. Runtime decoder remains authoritative for canonical bytes.", "type": "object", "additionalProperties": false, "required": required, "properties": properties, "oneOf": pairs, "x-storage": map[string]any{"namespace": "logs/engine-events-v1", "slots": EngineLogSlots, "slotMaximumBytes": EngineLogSlotBytes, "namespaceMaximumBytes": EngineLogSlots * EngineLogSlotBytes, "ownerMarkerBudgetBytes": EngineLogMetadataBytes, "effectiveSlotBytes": EngineLogSlotBytes - EngineLogMetadataBytes/EngineLogSlots, "recordMaximumBytes": EngineLogRecordBytes, "writer": "held cross-process owner.v1 lock", "history": "owned v1 slot headers bind namespace ID and slot; incomplete owned event tail can be trimmed, complete invalid lines refuse open; legacy logs never rotated"}, "x-health": map[string]any{"states": []string{"healthy", "degraded", "unknown"}, "stickyCodes": engineLogHealthCodes}}
	b, _ := json.MarshalIndent(schema, "", "  ")
	return append(b, '\n')
}
