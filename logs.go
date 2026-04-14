package main

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// McpCallLog is a single recorded MCP tool invocation.
// Contents are for local UI diagnostics only — this must NEVER be exposed
// to the agent via MCP (no gateway_* tool reads from here).
type McpCallLog struct {
	ID            int64     `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	Tool          string    `json:"tool"`
	URL           string    `json:"url,omitempty"`
	Args          map[string]any `json:"args"`
	DurationMs    int64     `json:"duration_ms"`
	IsError       bool      `json:"is_error"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	TextLen       int       `json:"text_len"`
	Text          string    `json:"text"`                     // truncated to maxLogTextSize
	TextTruncated bool      `json:"text_truncated,omitempty"`
	HasStructured bool      `json:"has_structured"`
	Structured    any       `json:"structured,omitempty"`
	HasSchema     bool      `json:"has_schema"` // true when text parses as {columns:[...], rows:[...]}
}

// maxLogTextSize caps the size of the raw response text stored per entry
// so that one huge response doesn't blow memory. 128 KB is enough to see
// typical 1C MCP replies while still being bounded.
const maxLogTextSize = 128 * 1024

// maxLogArgSize caps the length of any single string value stored inside
// args (e.g. QueryText, Code) so that long BSL scripts don't bloat logs.
const maxLogArgSize = 32 * 1024

// McpLogger is a fixed-capacity ring buffer of recent MCP calls.
// It is goroutine-safe and lives for the process lifetime.
type McpLogger struct {
	mu       sync.RWMutex
	capacity int
	entries  []McpCallLog
	next     atomic.Int64
}

// NewMcpLogger creates a new logger with the given capacity.
// Capacity < 1 is coerced to 1.
func NewMcpLogger(capacity int) *McpLogger {
	if capacity < 1 {
		capacity = 1
	}
	return &McpLogger{
		capacity: capacity,
		entries:  make([]McpCallLog, 0, capacity),
	}
}

// Add records a single MCP call. All sensitive fields in args ("Key", "key")
// are redacted; long string values are truncated. Safe for concurrent use.
func (l *McpLogger) Add(
	tool string,
	url string,
	args map[string]any,
	result *ToolCallResult,
	err error,
	startedAt time.Time,
) {
	if l == nil {
		return
	}
	entry := McpCallLog{
		ID:         l.next.Add(1),
		Timestamp:  startedAt,
		Tool:       tool,
		URL:        url,
		Args:       sanitizeArgs(args),
		DurationMs: time.Since(startedAt).Milliseconds(),
	}

	if err != nil {
		entry.IsError = true
		entry.ErrorMessage = err.Error()
	}

	if result != nil {
		if result.IsError {
			entry.IsError = true
			if entry.ErrorMessage == "" {
				entry.ErrorMessage = strings.TrimSpace(result.Text)
				if len(entry.ErrorMessage) > 2000 {
					entry.ErrorMessage = entry.ErrorMessage[:2000] + " ... [truncated]"
				}
			}
		}
		entry.TextLen = len(result.Text)
		if len(result.Text) > maxLogTextSize {
			entry.Text = result.Text[:maxLogTextSize]
			entry.TextTruncated = true
		} else {
			entry.Text = result.Text
		}
		entry.HasStructured = result.Structured != nil
		if entry.HasStructured {
			// Keep structured small — serialize and truncate.
			if data, mErr := json.Marshal(result.Structured); mErr == nil {
				if len(data) > maxLogTextSize {
					entry.Structured = string(data[:maxLogTextSize]) + "... [truncated]"
				} else {
					entry.Structured = json.RawMessage(data)
				}
			}
		}
		entry.HasSchema = looksLikeSchemaEnvelope(result.Text)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) < l.capacity {
		l.entries = append(l.entries, entry)
		return
	}
	// Shift left (oldest out) and append. Capacity is small (100) so this
	// is cheap; avoids the index arithmetic of a true circular buffer.
	copy(l.entries, l.entries[1:])
	l.entries[len(l.entries)-1] = entry
}

// All returns a copy of all stored entries, newest last.
func (l *McpLogger) All() []McpCallLog {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]McpCallLog, len(l.entries))
	copy(out, l.entries)
	return out
}

// Get returns a single entry by ID, or nil if not found.
func (l *McpLogger) Get(id int64) *McpCallLog {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := range l.entries {
		if l.entries[i].ID == id {
			e := l.entries[i]
			return &e
		}
	}
	return nil
}

// Clear removes all stored entries.
func (l *McpLogger) Clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

// sanitizeArgs returns a shallow copy of args with sensitive fields redacted
// and long string values truncated.
func sanitizeArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		lk := strings.ToLower(k)
		if lk == "key" || lk == "token" || lk == "password" {
			out[k] = "***"
			continue
		}
		switch val := v.(type) {
		case string:
			if len(val) > maxLogArgSize {
				out[k] = val[:maxLogArgSize] + "... [truncated]"
			} else {
				out[k] = val
			}
		default:
			out[k] = v
		}
	}
	return out
}

// looksLikeSchemaEnvelope reports whether the response text is a JSON
// envelope with "columns" and "rows" — the format 1C returns when
// IncludeSchema=true is honored. Used purely as a diagnostic hint.
func looksLikeSchemaEnvelope(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return false
	}
	// Cheap substring sniff is good enough; we don't want to fully parse.
	return strings.Contains(trimmed, "\"columns\"") && strings.Contains(trimmed, "\"rows\"")
}

// Global logger instance. Referenced by McpClient after each tool call.
var mcpLog = NewMcpLogger(100)
