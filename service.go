package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// TrustedSession holds all data for a single query session.
type TrustedSession struct {
	SessionID        string            `json:"session_id"`
	Task             string            `json:"task"`
	QueryText        string            `json:"query_text"`
	Mode             string            `json:"mode"`
	DisplayRows      []map[string]any  `json:"display_rows"`
	MaskedRows       []map[string]any  `json:"masked_rows"`
	MaskedColumns    []string          `json:"masked_columns"`
	UnmaskedColumns  []string          `json:"unmasked_columns"`
	AliasToOriginal  map[string]string `json:"alias_to_original"`
	RawResultPreview string            `json:"raw_result_preview"`
	ResultIsEmpty    bool              `json:"result_is_empty"`
	Diagnostic       map[string]any    `json:"diagnostic"`
	AnalysisMasked   string            `json:"analysis_masked"`
	AnalysisDisplay  string            `json:"analysis_display"`
	cachedBundle     string            // cached MaskedBundle result
}

// NewTrustedSession creates a new session with a random ID.
func NewTrustedSession() *TrustedSession {
	id := make([]byte, 6)
	_, _ = rand.Read(id)
	return &TrustedSession{
		SessionID:       hex.EncodeToString(id),
		DisplayRows:     make([]map[string]any, 0),
		MaskedRows:      make([]map[string]any, 0),
		MaskedColumns:   make([]string, 0),
		AliasToOriginal: make(map[string]string),
		Diagnostic:      make(map[string]any),
	}
}

// ClearSensitive wipes all sensitive data from the session.
func (s *TrustedSession) ClearSensitive() {
	s.DisplayRows = nil
	s.MaskedRows = nil
	s.MaskedColumns = nil
	s.AliasToOriginal = nil
	s.RawResultPreview = ""
	s.ResultIsEmpty = false
	s.Diagnostic = nil
	s.AnalysisMasked = ""
	s.AnalysisDisplay = ""
}

// TrustedGatewayRuntime provides the business logic for query execution and analysis.
type TrustedGatewayRuntime struct {
	Config *AppConfig
}

// NewTrustedGatewayRuntime creates a new runtime with the given config.
func NewTrustedGatewayRuntime(config *AppConfig) *TrustedGatewayRuntime {
	return &TrustedGatewayRuntime{Config: config}
}

// TestConnection verifies connectivity to the MCP server and returns the tool list.
// It also performs a lightweight tool call with the provided token to verify authentication.
func (rt *TrustedGatewayRuntime) TestConnection(ctx context.Context, url, token string) ([]string, error) {
	client := rt.buildMcpClient(url)
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("ошибка подключения к MCP (%s): %v", url, err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения списка инструментов: %v", err)
	}

	// Verify token by running a lightweight query
	if token != "" {
		args := map[string]any{
			"Key":       token,
			"QueryText": "ВЫБРАТЬ 1 КАК Проверка",
			"param":     []any{},
		}
		result, err := client.CallNamedTool(ctx, "query", args)
		if err != nil {
			return nil, fmt.Errorf("ошибка проверки ключа: %v", err)
		}
		if result.IsError || looksLike1CQueryError(result.Text) {
			return nil, fmt.Errorf("Ключ не принят сервером: %s", result.Preview(200))
		}
	}

	return tools, nil
}

// ExecuteQuery runs a query via MCP and returns a session with results.
func (rt *TrustedGatewayRuntime) ExecuteQuery(
	ctx context.Context,
	url, token, task, queryText, mode string,
	forceMaskFields, allowPlainFields map[string]bool,
	skipNumeric ...bool,
) (*TrustedSession, error) {
	client := rt.buildMcpClient(url)
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("ошибка подключения к MCP (%s): %v", url, err)
	}

	args := map[string]any{
		"QueryText": queryText,
		"param":     []any{},
	}
	if token != "" {
		args["Key"] = token
	}

	result, err := client.CallNamedTool(ctx, "query", args)
	if err != nil {
		return nil, err
	}

	// Remove sensitive key from record
	delete(result.Arguments, "Key")

	if result.IsError {
		return nil, fmt.Errorf("%s", result.Preview(rt.Config.Defaults.ResultPreviewChars))
	}
	if looksLike1CQueryError(result.Text) {
		return nil, fmt.Errorf("%s", strings.TrimSpace(result.Text))
	}

	rows := extractRows(result.Structured, result.Text)

	const maxRows = 5000
	totalParsedRows := len(rows)
	rowsTruncated := false
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		rowsTruncated = true
	}

	session := NewTrustedSession()
	session.Task = task
	session.QueryText = queryText
	session.Mode = mode
	session.RawResultPreview = result.Preview(rt.Config.Defaults.ResultPreviewChars)
	session.ResultIsEmpty = len(rows) == 0
	session.Diagnostic = map[string]any{
		"has_structured":    result.Structured != nil,
		"has_text":          result.Text != "",
		"text_length":       len(result.Text),
		"parsed_row_count":  len(rows),
		"total_parsed_rows": totalParsedRows,
		"rows_truncated":    rowsTruncated,
		"max_rows":          maxRows,
	}

	if mode == "direct" {
		session.DisplayRows = rows
		return session, nil
	}

	// Masked mode
	sanitizer := rt.runtimeSanitizer(token)
	if len(skipNumeric) > 0 && skipNumeric[0] {
		sanitizer.skipNumeric = true
	}
	sanitized := sanitizer.SanitizeRows(rows, forceMaskFields, allowPlainFields)
	session.DisplayRows = sanitized.DisplayRows
	session.MaskedRows = sanitized.MaskedRows
	session.MaskedColumns = sanitized.MaskedColumns
	session.UnmaskedColumns = sanitized.UnmaskedColumns
	session.AliasToOriginal = sanitized.AliasToOriginal
	return session, nil
}

// ApplyAnalysis rehydrates aliases in analysis text using the session's alias map.
func (rt *TrustedGatewayRuntime) ApplyAnalysis(session *TrustedSession, analysisText string) string {
	session.AnalysisMasked = analysisText
	session.AnalysisDisplay = RehydrateText(analysisText, session.AliasToOriginal)
	return session.AnalysisDisplay
}

// MaskedBundle returns the JSON bundle of masked data for the agent.
// The result is cached after first computation.
func MaskedBundle(session *TrustedSession) string {
	if session.cachedBundle != "" {
		return session.cachedBundle
	}
	payload := map[string]any{
		"session_id":     session.SessionID,
		"task":           session.Task,
		"query_text":     session.QueryText,
		"mode":           session.Mode,
		"row_count":      len(session.MaskedRows),
		"masked_columns": session.MaskedColumns,
		"rows":           session.MaskedRows,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	session.cachedBundle = string(data)
	return session.cachedBundle
}

func (rt *TrustedGatewayRuntime) buildMcpClient(url string) *McpClient {
	headers := make(map[string]string, len(rt.Config.Mcp.Headers))
	for k, v := range rt.Config.Mcp.Headers {
		headers[k] = v
	}
	return NewMcpClient(url, headers, rt.Config.Mcp.TimeoutSeconds, rt.Config.Mcp.Tools)
}

func (rt *TrustedGatewayRuntime) runtimeSanitizer(token string) *DataSanitizer {
	var effectiveSalt string
	if rt.Config.Privacy.Salt != "" {
		effectiveSalt = rt.Config.Privacy.Salt
	} else if token != "" {
		// Derive salt from token via HMAC
		mac := hmac.New(sha256.New, []byte(token))
		mac.Write([]byte("onec-gateway-salt"))
		effectiveSalt = hex.EncodeToString(mac.Sum(nil))
	} else {
		// Random salt as fallback
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		effectiveSalt = hex.EncodeToString(b)
	}
	return NewDataSanitizer(effectiveSalt, rt.Config.Privacy.AliasLength, rt.Config.Privacy.NumericThreshold)
}

// looksLike1CQueryError checks if the response text looks like a 1C query error.
func looksLike1CQueryError(text string) bool {
	if text == "" {
		return false
	}
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return false
	}
	errorMarkers := []string{
		"Ошибка выполнения инструмента",
		"ОшибкаВоВремяВыполненияВстроенногоЯзыка",
		"ИсключениеВызванноеИзВстроенногоЯзыка",
		"Строка, не закрывающаяся кавычкой",
		"Неоднозначное поле",
		"Поле не найдено",
		"Синтаксическая ошибка",
		"Неверные параметры",
		"Передан неверный ключ",
		"неверный ключ доступа",
	}
	for _, marker := range errorMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return strings.HasPrefix(normalized, "{(") && strings.Contains(normalized, ")}:")
}


// extractRows extracts row data from structured or text MCP response.
func extractRows(structured any, rawText string) []map[string]any {
	// Try structured content first
	if arr, ok := structured.([]any); ok {
		rows := make([]map[string]any, 0, len(arr))
		allDicts := true
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				rows = append(rows, m)
			} else {
				allDicts = false
				break
			}
		}
		if allDicts && len(rows) > 0 {
			return rows
		}
	}

	if m, ok := structured.(map[string]any); ok {
		for _, key := range []string{"rows", "items", "data", "result"} {
			if val, ok := m[key]; ok {
				if arr, ok := val.([]any); ok {
					rows := make([]map[string]any, 0, len(arr))
					allDicts := true
					for _, item := range arr {
						if rm, ok := item.(map[string]any); ok {
							rows = append(rows, rm)
						} else {
							allDicts = false
							break
						}
					}
					if allDicts && len(rows) > 0 {
						return rows
					}
				}
			}
		}
	}

	// Try parsing tabular text
	if rawText != "" {
		if parsed := parseTabularText(rawText); parsed != nil {
			return parsed
		}
	}

	return nil
}

// parseTabularText parses tab-separated text into rows.
func parseTabularText(rawText string) []map[string]any {
	rawLines := strings.Split(rawText, "\n")
	var lines []string
	for _, line := range rawLines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 2 {
		return nil
	}

	headers := strings.Split(lines[0], "\t")
	rows := make([]map[string]any, 0, len(lines)-1)

	for _, line := range lines[1:] {
		parts := strings.Split(line, "\t")
		for len(parts) < len(headers) {
			parts = append(parts, "")
		}
		if len(parts) > len(headers) {
			extra := strings.Join(parts[len(headers)-1:], "\t")
			parts = append(parts[:len(headers)-1], extra)
		}
		row := make(map[string]any, len(headers))
		for i, header := range headers {
			row[header] = parts[i]
		}
		rows = append(rows, row)
	}
	return rows
}
