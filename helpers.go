package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func getStringFieldDefault(m map[string]any, key, defaultVal string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return defaultVal
}

func getFloat64Field(m map[string]any, key string, defaultVal float64) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case json.Number:
			f, err := n.Float64()
			if err == nil {
				return f
			}
		}
	}
	return defaultVal
}

func getIntField(m map[string]any, key string, defaultVal int) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case json.Number:
			i, err := n.Int64()
			if err == nil {
				return int(i)
			}
		}
	}
	return defaultVal
}

func getBoolField(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func csvFields(raw string) map[string]bool {
	result := make(map[string]bool)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = true
		}
	}
	return result
}

// sanitizeImport strips unknown keys to prevent injection.
var importWhitelist = map[string]map[string]bool{
	"mcp": {
		"url": true, "auth_header_name": true, "auth_scheme": true,
		"timeout_seconds": true, "sse_read_timeout_seconds": true,
		"tools": true, "command": true, "args": true, "cwd": true,
		"env": true, "encoding": true, "headers": true,
	},
	"privacy": {
		"salt": true, "salt_env": true, "alias_length": true,
		"numeric_threshold": true, "show_masked_data_in_viewer": true,
	},
	"defaults": {
		"result_preview_chars": true, "auto_send_to_agent": true,
		"skip_numeric_values": true, "allow_plain_fields": true,
	},
	"auth": {"token": true},
}

func sanitizeImport(data map[string]any) map[string]any {
	clean := make(map[string]any)
	for section, allowedKeys := range importWhitelist {
		raw, ok := data[section].(map[string]any)
		if !ok {
			continue
		}
		filtered := make(map[string]any)
		for k, v := range raw {
			if allowedKeys[k] {
				filtered[k] = v
			}
		}
		clean[section] = filtered
	}
	return clean
}
