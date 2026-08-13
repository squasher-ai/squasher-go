package squasher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

type eventPayload struct {
	Message   string            `json:"message"`
	Level     string            `json:"level"`
	Timestamp string            `json:"timestamp"`
	Attrs     map[string]any    `json:"attrs"`
	Tags      map[string]string `json:"tags,omitempty"`
	Extra     map[string]any    `json:"extra,omitempty"`
	SDK       sdkInfo           `json:"sdk"`
}

type sdkInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func buildPayload(record slog.Record, baseAttrs map[string]any, groups []string) eventPayload {
	attrs := cloneAttrs(baseAttrs)
	record.Attrs(func(attr slog.Attr) bool {
		applyAttr(attrs, groups, attr)
		return true
	})

	timestamp := record.Time.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	payload := eventPayload{
		Message:   record.Message,
		Level:     mapLevel(record.Level),
		Timestamp: timestamp.Format(time.RFC3339Nano),
		Attrs:     attrs,
		SDK: sdkInfo{
			Name:    SDKName,
			Version: SDKVersion,
		},
	}
	if len(attrs) > 0 {
		payload.Tags = flattenAttrs(attrs)
		payload.Extra = attrs
	}
	return payload
}

func applyAttr(target map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := groups
		if attr.Key != "" {
			nextGroups = append(append([]string(nil), groups...), attr.Key)
		}
		for _, nested := range attr.Value.Group() {
			applyAttr(target, nextGroups, nested)
		}
		return
	}
	if attr.Key == "" {
		return
	}
	setNestedValue(target, groups, attr.Key, normalizeValue(attr.Value))
}

func normalizeValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindAny:
		return normalizeAny(value.Any())
	default:
		return normalizeAny(value.Any())
	}
}

func normalizeAny(value any) any {
	switch typed := value.(type) {
	case nil, string, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return typed.String()
	case error:
		return typed.Error()
	case fmt.Stringer:
		return typed.String()
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return string(encoded)
	}
	return normalized
}

func mapLevel(level slog.Level) string {
	switch {
	case level >= slog.LevelError+4:
		return "fatal"
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warning"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

func setNestedValue(target map[string]any, groups []string, key string, value any) {
	current := target
	for _, group := range groups {
		if group == "" {
			continue
		}
		child, ok := current[group].(map[string]any)
		if !ok {
			child = map[string]any{}
			current[group] = child
		}
		current = child
	}
	current[key] = value
}

func flattenAttrs(attrs map[string]any) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	flattened := make(map[string]string, len(attrs))
	flattenInto(flattened, "", attrs)
	return flattened
}

func flattenInto(target map[string]string, prefix string, value any) {
	if nested, ok := value.(map[string]any); ok {
		for key, child := range nested {
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}
			flattenInto(target, nextPrefix, child)
		}
		return
	}
	target[prefix] = stringifyValue(value)
}

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		if len(encoded) >= 2 && encoded[0] == '"' && encoded[len(encoded)-1] == '"' {
			var unquoted string
			if err := json.Unmarshal(encoded, &unquoted); err == nil {
				return unquoted
			}
		}
		return string(encoded)
	}
}

func cloneAttrs(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(attrs))
	for key, value := range attrs {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAttrs(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return typed
	}
}
