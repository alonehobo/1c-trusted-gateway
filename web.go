package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultWebHost = "127.0.0.1"
	DefaultWebPort = 8767
	maxRequestBody = 1 * 1024 * 1024 // 1 MB

	// Brute-force protection
	rateLimitWindow    = 5 * time.Second // sliding window size
	rateLimitMaxPerWin = 3               // max queries per window
	lowRowStreakMax    = 5               // consecutive ≤1-row results before escalation
)

// TrustedWebApp holds all application state and logic.
type TrustedWebApp struct {
	Config             *AppConfig
	Runtime            *TrustedGatewayRuntime
	CurrentSession     *TrustedSession
	ConnectedURL       string
	ConnectedToken     string
	ConnectionVerified bool
	HasSavedSettings   bool
	ResultRows         []map[string]any
	ResultHeaders      []string
	ColumnOrder        []string
	MaskedResultRows    []map[string]any
	MaskedResultHeaders []string
	MaskedColumns       []string
	UnmaskedColumns     []string
	PendingAgentNote    map[string]string
	ThemeName       string
	TaskText        string
	StatusText      string
	QueryState      string
	QueryStateText  string
	BundleText      string
	AnalysisMasked  string
	AnalysisDisplay string
	QueryPreview    string
	RawResponse     string
	RawState        string
	ForceMaskFields        string // per-query tag overrides (reset on each new query)
	AllowPlainFields       string // per-query tag overrides (reset on each new query)
	PersistentForceMask    string // persistent UI whitelist — never reset by queries
	PersistentAllowPlain   string // persistent UI whitelist — never reset by queries
	PersistentTypePolicy   string // JSON-encoded PersistedTypePolicy overrides (see type_policy.go)
	ExcludedFields         string // comma-separated list of columns to exclude before masking
	ActiveTab       string
	PlaceholderText string
	PlaceholderError bool
	RowsTruncated   bool
	MaxRows         int
	TotalRowCount   int

	SessionToken string

	SuggestedFields []string     // fields suggested by agent for whitelisting
	suggestDone     chan struct{} // signaled when all suggested fields are approved

	AutoSendToAgent   bool
	SkipNumericValues bool // when true, real numbers (prices, amounts) are not masked

	PendingCode     string // BSL code awaiting user approval
	PendingCodeTask string // task description for pending code
	CodeMode        bool   // true = editor is in code mode, false = query mode

	// Rate-limiter / brute-force detection
	queryTimestamps    []time.Time // sliding window of agent query times
	lowRowStreakCount  int         // consecutive queries with ≤1 row result
	rateLimitTriggered bool        // true when brute-force was detected
	rateLimitMessage   string      // message shown in UI

	queryCancel  context.CancelFunc
	queryRunning bool
	mu           sync.RWMutex
	stateVersion atomic.Int64
	dataVersion  int64 // incremented when row data changes (remask, new query, etc.)
	queryVersion int64 // incremented only on new queries (not remask) — client uses this to reset tag state
	stateEvent   chan struct{}
}

// NewTrustedWebApp creates a new web app instance.
func NewTrustedWebApp(config *AppConfig, savedToken string) *TrustedWebApp {
	app := &TrustedWebApp{
		Config:           config,
		Runtime:          NewTrustedGatewayRuntime(config),
		ConnectedURL:     config.Mcp.URL,
		ConnectedToken:   savedToken,
		HasSavedSettings: savedToken != "",
		ThemeName:        "dark",
		TaskText:         "Ожидаю задачу от контроллера",
		StatusText:       "Готово.",
		QueryState:       "idle",
		QueryStateText:   "Ожидание",
		RawState:         "neutral",
		PlaceholderText:  "Результат появится здесь после выполнения запроса.",
		SessionToken:     generateToken(24),
		stateEvent:       make(chan struct{}, 1),
	}
	return app
}

// notify increments state version and signals waiters. Caller must hold app.mu (write lock).
func (app *TrustedWebApp) notify() {
	app.stateVersion.Add(1)
	select {
	case app.stateEvent <- struct{}{}:
	default:
	}
}

func (app *TrustedWebApp) waitForChange(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-app.stateEvent:
	case <-timer.C:
	}
}

// checkBridgeRateLimit checks if the bridge query rate is suspicious.
// Must be called with app.mu held (write lock).
// Returns true if the request should be blocked.
func (app *TrustedWebApp) checkBridgeRateLimit() bool {
	now := time.Now()

	// Clean old timestamps outside the window
	cutoff := now.Add(-rateLimitWindow)
	clean := app.queryTimestamps[:0]
	for _, t := range app.queryTimestamps {
		if t.After(cutoff) {
			clean = append(clean, t)
		}
	}
	app.queryTimestamps = clean

	// Check rate
	if len(app.queryTimestamps) >= rateLimitMaxPerWin {
		app.rateLimitTriggered = true
		app.rateLimitMessage = fmt.Sprintf("⚠️ Защита от перебора: более %d запросов за %d сек. Авто-режим отключён.", rateLimitMaxPerWin, int(rateLimitWindow.Seconds()))
		app.AutoSendToAgent = false
		app.notify()
		return true
	}

	// Record this request
	app.queryTimestamps = append(app.queryTimestamps, now)
	return false
}

// recordLowRowResult tracks consecutive queries returning ≤1 row.
// Must be called with app.mu held (write lock).
func (app *TrustedWebApp) recordLowRowResult(rowCount int) {
	if rowCount <= 1 {
		app.lowRowStreakCount++
		if app.lowRowStreakCount >= lowRowStreakMax && app.AutoSendToAgent {
			app.rateLimitTriggered = true
			app.rateLimitMessage = fmt.Sprintf("⚠️ Защита от перебора: %d запросов подряд вернули ≤1 строку. Авто-режим отключён.", app.lowRowStreakCount)
			app.AutoSendToAgent = false
			app.notify()
		}
	} else {
		app.lowRowStreakCount = 0
	}
}

// resetRateLimit clears rate limit state. Called when user manually re-enables auto mode.
// Must be called with app.mu held (write lock).
func (app *TrustedWebApp) resetRateLimit() {
	app.rateLimitTriggered = false
	app.rateLimitMessage = ""
	app.lowRowStreakCount = 0
	app.queryTimestamps = nil
}

// GetState returns the current application state as a map.
func (app *TrustedWebApp) GetState() map[string]any {
	app.mu.RLock()
	defer app.mu.RUnlock()
	session := app.CurrentSession

	var sessionID, mode interface{}
	if session != nil {
		sessionID = session.SessionID
		mode = session.Mode
	}

	resultPlaceholder := interface{}(nil)
	if len(app.ResultRows) == 0 {
		resultPlaceholder = app.PlaceholderText
	}

	return map[string]any{
		"version": app.stateVersion.Load(),
		"theme":   app.ThemeName,
		"connection": map[string]any{
			"url":      app.ConnectedURL,
			"verified": app.ConnectionVerified,
		},
		"task":             app.TaskText,
		"session_id":       sessionID,
		"mode":             mode,
		"status":           app.StatusText,
		"query_state":      app.QueryState,
		"query_state_text": app.QueryStateText,
		"security_hint":    app.securityHint(),
		"bridge_info":      fmt.Sprintf("MCP: http://%s:%d/mcp", DefaultWebHost, DefaultWebPort),
		"result": map[string]any{
			"headers":           app.ResultHeaders,
			"row_count":         len(app.ResultRows),
			"placeholder":       resultPlaceholder,
			"placeholder_error": app.PlaceholderError,
		},
		"masked_result": map[string]any{
			"headers":          app.MaskedResultHeaders,
			"row_count":        len(app.MaskedResultRows),
			"masked_columns":   app.MaskedColumns,
			"unmasked_columns": app.UnmaskedColumns,
		},
		"rows_truncated":         app.RowsTruncated,
		"max_rows":               app.MaxRows,
		"total_row_count":        app.TotalRowCount,
		"bundle_text":            app.BundleText,
		"analysis_masked":        app.AnalysisMasked,
		"analysis_display":       app.AnalysisDisplay,
		"query_preview":          app.QueryPreview,
		"raw_response":           app.RawResponse,
		"raw_state":              app.RawState,
		"active_tab":             app.ActiveTab,
		"query_running":          app.queryRunning,
		"has_saved_settings":     app.HasSavedSettings,
		"has_saved_token":        app.ConnectedToken != "",
		"defaults_allow_plain":   app.Config.Defaults.AllowPlainFields,
		"suggested_fields":       app.SuggestedFields,
		"agent_waiting_approval": app.suggestDone != nil,
		"excluded_fields":        app.ExcludedFields,
		"persistent_allow_plain":   app.PersistentAllowPlain,
		"persistent_force_mask":    app.PersistentForceMask,
		"persistent_type_policy":   app.PersistentTypePolicy,
		"type_policy_effective":    app.Runtime.TypePolicy.Snapshot(),
		"auto_send_to_agent":     app.AutoSendToAgent,
		"skip_numeric_values":    app.SkipNumericValues,
		"approval_pending":       false,
		"pending_code":           app.PendingCode,
		"pending_code_task":      app.PendingCodeTask,
		"code_mode":              app.CodeMode,
		"data_version":           app.dataVersion,
		"query_version":          app.queryVersion,
		"rate_limit_triggered":   app.rateLimitTriggered,
		"rate_limit_message":     app.rateLimitMessage,
		"ner_status":             nerRulesStatus(app.Runtime.NerRules),
	}
}

// ── Handle methods (business logic) ────────────────────────────

// HandleConnect handles a connection attempt to the MCP server.
func (app *TrustedWebApp) HandleConnect(data map[string]any) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	urlStr := strings.TrimSpace(getStringFieldDefault(data, "url", ""))
	token := getStringFieldDefault(data, "token", "")
	useSaved, _ := data["use_saved_token"].(bool)

	if useSaved && token == "" && app.ConnectedToken != "" {
		token = app.ConnectedToken
	}
	if urlStr == "" {
		return map[string]any{"ok": false, "error": "URL is empty."}
	}

	app.StatusText = "Проверяю MCP..."
	app.QueryState = "running"
	app.QueryStateText = "Выполняется"
	app.notify()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tools, err := app.Runtime.TestConnection(ctx, urlStr, token)
	if err != nil {
		errMsg := err.Error()
		var friendly string
		if strings.Contains(errMsg, "500") {
			friendly = "MCP-сервер вернул ошибку 500 (Internal Server Error). Проверьте настройки HTTP-сервиса 1С."
		} else if strings.Contains(errMsg, "Cancelled") || strings.Contains(errMsg, "CancelledError") {
			friendly = "MCP-сессия отменена сервером при инициализации. Проверьте HTTP-сервис 1С."
		} else if strings.Contains(errMsg, "refused") || strings.Contains(strings.ToLower(errMsg), "connection refused") {
			friendly = "Не удалось подключиться: сервер недоступен. Проверьте URL."
		} else if strings.Contains(strings.ToLower(errMsg), "timeout") {
			friendly = "Таймаут подключения к MCP-серверу."
		} else {
			friendly = errMsg
		}
		if len(friendly) > 150 {
			friendly = friendly[:150]
		}
		app.ConnectionVerified = false
		app.StatusText = "Ошибка: " + friendly
		app.QueryState = "error"
		app.QueryStateText = "Ошибка"
		app.notify()
		return map[string]any{"ok": false, "error": friendly}
	}

	app.ConnectedURL = urlStr
	app.ConnectedToken = token
	app.ConnectionVerified = true
	app.StatusText = fmt.Sprintf("MCP доступен. Инструменты: %s", strings.Join(tools, ", "))
	app.QueryState = "idle"
	app.QueryStateText = "Подключено"
	app.notify()
	return map[string]any{"ok": true, "tools": tools}
}

// HandleDisconnect clears the token and saved settings.
func (app *TrustedWebApp) HandleDisconnect() map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ConnectedToken = ""
	app.ConnectionVerified = false
	_ = DeleteSettings()
	app.HasSavedSettings = false
	app.StatusText = "Ключ удален из памяти. Сохранённые настройки очищены."
	app.notify()
	return map[string]any{"ok": true}
}

// HandleGetSettings returns current settings for the UI form.
func (app *TrustedWebApp) HandleGetSettings() map[string]any {
	app.mu.RLock()
	defer app.mu.RUnlock()
	saltDisplay := "(авто: из ключа сервера)"
	if app.Config.Privacy.Salt != "" {
		saltDisplay = "***"
	}
	return map[string]any{
		"mcp_url":                      firstNonEmpty(app.Config.Mcp.URL, app.ConnectedURL),
		"mcp_token_saved":              app.ConnectedToken != "" && app.HasSavedSettings,
		"mcp_timeout_seconds":          app.Config.Mcp.TimeoutSeconds,
		"mcp_sse_read_timeout_seconds": app.Config.Mcp.SSEReadTimeoutSeconds,
		"privacy_salt":                 saltDisplay,
		"privacy_salt_env":             app.Config.Privacy.SaltEnv,
		"privacy_alias_length":         app.Config.Privacy.AliasLength,
		"privacy_numeric_threshold":    app.Config.Privacy.NumericThreshold,
		"privacy_show_masked":          app.Config.Privacy.ShowMaskedDataInViewer,
		"defaults_preview_chars":       app.Config.Defaults.ResultPreviewChars,
		"defaults_auto_send":           app.Config.Defaults.AutoSendToAgent,
		"defaults_skip_numeric":        app.Config.Defaults.SkipNumericValues,
		"defaults_allow_plain_fields":  app.Config.Defaults.AllowPlainFields,
		"defaults_force_mask_fields":   app.Config.Defaults.ForceMaskFields,
		"has_saved_settings":           app.HasSavedSettings,
		"allow_plain_keywords":         AllowPlainKeywordsCSV(),
	}
}

// HandleSaveSettings persists settings to encrypted storage and reloads runtime.
func (app *TrustedWebApp) HandleSaveSettings(data map[string]any) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	mcpURL := strings.TrimSpace(getStringFieldDefault(data, "mcp_url", ""))
	if mcpURL == "" {
		mcpURL = app.ConnectedURL
	}

	incomingSalt := strings.TrimSpace(getStringFieldDefault(data, "privacy_salt", ""))
	var actualSalt string
	if incomingSalt == "***" || incomingSalt == "" {
		actualSalt = app.Config.Privacy.Salt
	} else {
		actualSalt = incomingSalt
	}

	settings := map[string]any{
		"mcp": map[string]any{
			"url":                      mcpURL,
			"timeout_seconds":          getFloat64Field(data, "mcp_timeout_seconds", 30.0),
			"sse_read_timeout_seconds": getFloat64Field(data, "mcp_sse_read_timeout_seconds", 300.0),
			"tools":                    app.Config.Mcp.Tools,
		},
		"privacy": map[string]any{
			"salt":                       actualSalt,
			"salt_env":                   getStringFieldDefault(data, "privacy_salt_env", ""),
			"alias_length":               getIntField(data, "privacy_alias_length", 10),
			"numeric_threshold":          getIntField(data, "privacy_numeric_threshold", 10),
			"show_masked_data_in_viewer": getBoolField(data, "privacy_show_masked"),
		},
		"defaults": map[string]any{
			"result_preview_chars": getIntField(data, "defaults_preview_chars", 4000),
			"auto_send_to_agent":  getBoolField(data, "defaults_auto_send"),
			"skip_numeric_values": getBoolField(data, "defaults_skip_numeric"),
			"allow_plain_fields":  getStringFieldDefault(data, "defaults_allow_plain_fields", ""),
			"force_mask_fields":   getStringFieldDefault(data, "defaults_force_mask_fields", ""),
		},
		"auth": map[string]any{
			"token": firstNonEmpty(getStringFieldDefault(data, "mcp_token", ""), app.ConnectedToken),
		},
	}

	// Preserve type_policy on disk — otherwise saving general settings
	// silently wipes the user's type-aware masking policy (main.go reads
	// this key on startup to restore PersistentTypePolicy).
	if strings.TrimSpace(app.PersistentTypePolicy) != "" {
		settings["type_policy"] = app.PersistentTypePolicy
	}

	if err := SaveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}

	newConfig := ConfigFromDict(settings)
	app.Config = newConfig
	app.Runtime = NewTrustedGatewayRuntime(newConfig)
	app.reapplyPersistentTypePolicyLocked()
	app.ConnectedURL = newConfig.Mcp.URL
	app.AutoSendToAgent = newConfig.Defaults.AutoSendToAgent
	app.SkipNumericValues = newConfig.Defaults.SkipNumericValues
	if authMap, ok := settings["auth"].(map[string]any); ok {
		if tok, ok := authMap["token"].(string); ok && tok != "" {
			app.ConnectedToken = tok
		}
	}
	app.HasSavedSettings = true
	app.StatusText = "Настройки сохранены и зашифрованы."
	app.notify()
	return map[string]any{"ok": true}
}

// HandleResetSettings deletes encrypted storage and reverts to defaults.
func (app *TrustedWebApp) HandleResetSettings() map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	_ = DeleteSettings()
	app.HasSavedSettings = false
	newConfig := ConfigFromDict(map[string]any{})
	app.Config = newConfig
	app.Runtime = NewTrustedGatewayRuntime(newConfig)
	app.ConnectedURL = newConfig.Mcp.URL
	app.ConnectedToken = ""
	app.ConnectionVerified = false
	app.StatusText = "Настройки сброшены. Хранилище удалено."
	app.notify()
	return map[string]any{"ok": true}
}

// HandleExportSettings returns current settings as a JSON-serializable map (without auth token).
func (app *TrustedWebApp) HandleExportSettings() map[string]any {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return map[string]any{
		"mcp": map[string]any{
			"url":                      app.Config.Mcp.URL,
			"timeout_seconds":          app.Config.Mcp.TimeoutSeconds,
			"sse_read_timeout_seconds": app.Config.Mcp.SSEReadTimeoutSeconds,
			"tools":                    app.Config.Mcp.Tools,
		},
		"privacy": map[string]any{
			"alias_length":               app.Config.Privacy.AliasLength,
			"numeric_threshold":          app.Config.Privacy.NumericThreshold,
			"show_masked_data_in_viewer": app.Config.Privacy.ShowMaskedDataInViewer,
		},
		"defaults": map[string]any{
			"result_preview_chars": app.Config.Defaults.ResultPreviewChars,
			"auto_send_to_agent":  app.Config.Defaults.AutoSendToAgent,
			"skip_numeric_values": app.Config.Defaults.SkipNumericValues,
			"allow_plain_fields":  app.Config.Defaults.AllowPlainFields,
			"force_mask_fields":   app.Config.Defaults.ForceMaskFields,
		},
	}
}

// HandleImportSettings imports settings from a config.json-style dict.
func (app *TrustedWebApp) HandleImportSettings(data map[string]any) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	settings := sanitizeImport(data)
	token := ""
	if authMap, ok := settings["auth"].(map[string]any); ok {
		if t, ok := authMap["token"].(string); ok {
			token = t
		}
	}
	// Preserve type_policy on disk on import too. If the import file carries
	// its own type_policy — respect it; otherwise fall back to current.
	if _, ok := settings["type_policy"]; !ok {
		if strings.TrimSpace(app.PersistentTypePolicy) != "" {
			settings["type_policy"] = app.PersistentTypePolicy
		}
	}
	if err := SaveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	newConfig := ConfigFromDict(settings)
	app.Config = newConfig
	app.Runtime = NewTrustedGatewayRuntime(newConfig)
	// If imported settings carried a type_policy, adopt it as persistent;
	// otherwise reapply the previous one.
	if tp, ok := settings["type_policy"].(string); ok {
		app.PersistentTypePolicy = tp
	}
	app.reapplyPersistentTypePolicyLocked()
	app.ConnectedURL = newConfig.Mcp.URL
	app.AutoSendToAgent = newConfig.Defaults.AutoSendToAgent
	app.SkipNumericValues = newConfig.Defaults.SkipNumericValues
	if token != "" {
		app.ConnectedToken = token
	}
	app.HasSavedSettings = true
	app.StatusText = "Настройки импортированы и зашифрованы."
	app.notify()
	return map[string]any{"ok": true}
}

// HandleQuery processes a query from the web UI.
func (app *TrustedWebApp) HandleQuery(data map[string]any) map[string]any {
	task := strings.TrimSpace(getStringFieldDefault(data, "task", ""))
	queryText := strings.TrimSpace(getStringFieldDefault(data, "query_text", ""))
	mode := strings.ToLower(strings.TrimSpace(getStringFieldDefault(data, "mode", "direct")))
	forceMask := getStringFieldDefault(data, "force_mask_fields", "")
	allowPlain := getStringFieldDefault(data, "allow_plain_fields", "")
	app.mu.Lock()
	app.ForceMaskFields = forceMask
	app.AllowPlainFields = allowPlain
	app.mu.Unlock()

	// Strip ПРЕДСТАВЛЕНИЕ()/PRESENTATION() and normalize aliases (symmetric
	// with MCP path) so UI-initiated queries participate in type-aware masking.
	queryText = stripPresentationCalls(queryText)
	queryText = normalizeQueryAliases(queryText)

	return app.bridgeRunQuery(task, queryText, mode, false)
}

// HandleSetWhitelist sets the persistent allow/force-mask lists (from UI textarea) and remasks current session.
func (app *TrustedWebApp) HandleSetWhitelist(forceMask, allowPlain string) map[string]any {
	app.mu.Lock()
	app.PersistentForceMask = forceMask
	app.PersistentAllowPlain = allowPlain
	app.notify()

	session := app.CurrentSession
	if session == nil {
		app.mu.Unlock()
		return map[string]any{"ok": true}
	}
	app.remaskLocked(session)
	app.mu.Unlock()
	return map[string]any{"ok": true}
}

// reapplyPersistentTypePolicyLocked re-merges app.PersistentTypePolicy onto
// app.Runtime.TypePolicy. Must be called after any assignment of a fresh
// Runtime (NewTrustedGatewayRuntime) inside a locked section, otherwise the
// user's custom type-policy overrides get silently lost until app restart.
// Caller must hold app.mu.
func (app *TrustedWebApp) reapplyPersistentTypePolicyLocked() {
	if app.Runtime == nil || app.Runtime.TypePolicy == nil {
		return
	}
	if strings.TrimSpace(app.PersistentTypePolicy) == "" {
		return
	}
	app.Runtime.TypePolicy.MergePersisted(app.PersistentTypePolicy)
}

// HandleSetTypePolicy persists the type-aware masking policy override (JSON
// string matching PersistedTypePolicy) and rebuilds the runtime TypePolicy,
// then remasks the current session.
func (app *TrustedWebApp) HandleSetTypePolicy(policyJSON string) map[string]any {
	// Validate: empty is allowed (resets to defaults).
	policyJSON = strings.TrimSpace(policyJSON)

	app.mu.Lock()
	app.PersistentTypePolicy = policyJSON
	// Rebuild the shared TypePolicy so new queries and remasks honor the change.
	newPolicy := NewDefaultTypePolicy()
	newPolicy.MergePersisted(policyJSON)
	app.Runtime.TypePolicy = newPolicy

	// Persist alongside other settings so the override survives restarts.
	// If settings.bin doesn't exist yet (no main settings saved), we build a
	// minimal settings dict from the current in-memory config so the type
	// policy is still persisted. Without this, HandleSetTypePolicy silently
	// no-ops on disk and the policy vanishes on restart.
	savedSettings, _ := LoadSettings()
	if savedSettings == nil {
		savedSettings = map[string]any{
			"mcp": map[string]any{
				"url":                      app.Config.Mcp.URL,
				"timeout_seconds":          app.Config.Mcp.TimeoutSeconds,
				"sse_read_timeout_seconds": app.Config.Mcp.SSEReadTimeoutSeconds,
				"tools":                    app.Config.Mcp.Tools,
			},
			"privacy": map[string]any{
				"salt":                       app.Config.Privacy.Salt,
				"salt_env":                   app.Config.Privacy.SaltEnv,
				"alias_length":               app.Config.Privacy.AliasLength,
				"numeric_threshold":          app.Config.Privacy.NumericThreshold,
				"show_masked_data_in_viewer": app.Config.Privacy.ShowMaskedDataInViewer,
			},
			"defaults": map[string]any{
				"result_preview_chars": app.Config.Defaults.ResultPreviewChars,
				"auto_send_to_agent":   app.Config.Defaults.AutoSendToAgent,
				"skip_numeric_values":  app.Config.Defaults.SkipNumericValues,
				"allow_plain_fields":   app.Config.Defaults.AllowPlainFields,
				"force_mask_fields":    app.Config.Defaults.ForceMaskFields,
			},
			"auth": map[string]any{
				"token": app.ConnectedToken,
			},
		}
	}
	savedSettings["type_policy"] = policyJSON
	if err := SaveSettings(savedSettings); err == nil {
		app.HasSavedSettings = true
	}

	app.notify()

	session := app.CurrentSession
	if session == nil {
		app.mu.Unlock()
		return map[string]any{"ok": true}
	}
	app.remaskLocked(session)
	app.mu.Unlock()
	return map[string]any{"ok": true}
}

// HandleConfirmSuggestedFields signals the agent that user has finished approving suggested fields.
func (app *TrustedWebApp) HandleConfirmSuggestedFields() map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.suggestDone == nil {
		return map[string]any{"ok": true, "message": "Нет ожидающего запроса от агента."}
	}

	// Signal the waiting agent
	select {
	case app.suggestDone <- struct{}{}:
	default:
	}

	return map[string]any{"ok": true, "message": "Подтверждено."}
}

// HandleRemask re-applies masking to the current session's data without re-querying the MCP server.
func (app *TrustedWebApp) HandleRemask(forceMask, allowPlain string) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()

	session := app.CurrentSession
	if session == nil {
		return map[string]any{"ok": false, "error": "Нет активной сессии."}
	}
	if session.Mode != "masked" {
		return map[string]any{"ok": false, "error": "Ремаскировка доступна только в masked-режиме."}
	}
	if len(session.DisplayRows) == 0 {
		return map[string]any{"ok": false, "error": "Нет данных для ремаскировки."}
	}

	// Update session-level mask fields
	app.ForceMaskFields = forceMask
	app.AllowPlainFields = allowPlain

	app.remaskLocked(session)

	return map[string]any{
		"ok":               true,
		"masked_columns":   session.MaskedColumns,
		"unmasked_columns": session.UnmaskedColumns,
	}
}

// HandleExcludeFields updates excluded fields and re-masks.
func (app *TrustedWebApp) HandleExcludeFields(excluded string) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()

	session := app.CurrentSession
	if session == nil {
		return map[string]any{"ok": false, "error": "Нет активной сессии."}
	}
	if session.Mode != "masked" {
		return map[string]any{"ok": false, "error": "Исключение полей доступно только в masked-режиме."}
	}
	if len(session.DisplayRows) == 0 {
		return map[string]any{"ok": false, "error": "Нет данных."}
	}

	app.ExcludedFields = excluded
	app.remaskLocked(session)

	return map[string]any{
		"ok":               true,
		"excluded_fields":  app.ExcludedFields,
		"masked_columns":   session.MaskedColumns,
		"unmasked_columns": session.UnmaskedColumns,
	}
}

// HandleSuggestFields stores agent-suggested fields for whitelisting.
func (app *TrustedWebApp) HandleSuggestFields(fields []string) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()

	existingAllow := csvFields(app.PersistentAllowPlain)
	var filtered []string
	seen := make(map[string]bool)
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		lc := strings.ToLower(f)
		if seen[lc] || existingAllow[lc] {
			continue
		}
		seen[lc] = true
		filtered = append(filtered, f)
	}

	app.SuggestedFields = filtered
	app.notify()

	return map[string]any{"ok": true, "suggested_fields": filtered}
}

// ── Internal helpers ───────────────────────────────────────────

// remaskLocked re-sanitizes session data with current mask/allow/exclude settings.
// Caller must hold app.mu (write lock).
func (app *TrustedWebApp) remaskLocked(session *TrustedSession) {
	rows := app.filterExcludedColumns(session.DisplayRows)

	sanitizer := app.Runtime.runtimeSanitizer(app.ConnectedToken)
	sanitizer.skipNumeric = app.SkipNumericValues
	sanitizer.typePolicy = app.Runtime.TypePolicy
	sanitizer.columnTypes = session.ColumnTypes
	sanitizer.columnTruncated = session.ColumnTruncated
	sanitized := sanitizer.SanitizeRows(rows, app.mergedForceMask(), app.mergedAllowPlain())

	session.MaskedRows = sanitized.MaskedRows
	session.MaskedColumns = sanitized.MaskedColumns
	session.UnmaskedColumns = sanitized.UnmaskedColumns
	session.AliasToOriginal = sanitized.AliasToOriginal
	session.cachedBundle = "" // invalidate cached bundle

	app.extractMaskedRows(session.MaskedRows, session.MaskedColumns, session.UnmaskedColumns)
	app.BundleText = MaskedBundle(session)
	app.ActiveTab = "result"
	app.StatusText = "Маскировка обновлена. Bundle пересобран."
	app.notify()
}

// filterExcludedColumns returns a copy of rows with excluded columns removed.
func (app *TrustedWebApp) filterExcludedColumns(rows []map[string]any) []map[string]any {
	excluded := csvFields(app.ExcludedFields)
	if len(excluded) == 0 {
		return rows
	}
	normalizedExcluded := make(map[string]bool, len(excluded))
	for f := range excluded {
		normalizedExcluded[strings.ToLower(strings.TrimSpace(f))] = true
	}
	filtered := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		newRow := make(map[string]any, len(row))
		for k, v := range row {
			if !normalizedExcluded[strings.ToLower(strings.TrimSpace(k))] {
				newRow[k] = v
			}
		}
		filtered = append(filtered, newRow)
	}
	return filtered
}

// ── Bridge callbacks ────────────────────────────────────────────

func (app *TrustedWebApp) bridgeStatus() map[string]any {
	app.mu.RLock()
	defer app.mu.RUnlock()
	session := app.CurrentSession

	result := map[string]any{
		"ready":            app.ConnectionVerified && app.ConnectedURL != "",
		"connected_url":    app.ConnectedURL,
		"has_session":      session != nil,
		"has_pending_note": app.PendingAgentNote != nil,
	}
	if session != nil {
		result["session_id"] = session.SessionID
		result["mode"] = session.Mode
	}
	return result
}

func (app *TrustedWebApp) bridgeRunQuery(task, queryText, mode string, fromBridge bool) map[string]any {
	if mode != "direct" && mode != "masked" {
		return map[string]any{"ok": false, "error": "mode must be direct or masked"}
	}

	// Rate limit check for bridge (agent) requests
	if fromBridge {
		app.mu.Lock()
		blocked := app.checkBridgeRateLimit()
		app.mu.Unlock()
		if blocked {
			return map[string]any{
				"ok":      false,
				"error":   "rate_limit",
				"message": "Слишком частые запросы. Авто-режим отключён. Работайте в ручном режиме.",
			}
		}
	}

	app.mu.RLock()
	verified := app.ConnectionVerified
	connURL := app.ConnectedURL
	connToken := app.ConnectedToken
	app.mu.RUnlock()

	if !verified || connURL == "" {
		return map[string]any{"ok": false, "error": "Сначала введите ключ и нажмите 'Подключиться'."}
	}
	if queryText == "" {
		return map[string]any{"ok": false, "error": "QueryText is empty."}
	}

	if task == "" {
		task = "Задача без названия"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)

	app.mu.Lock()
	if fromBridge {
		app.ForceMaskFields = ""
		app.AllowPlainFields = ""
	}
	app.TaskText = task
	app.QueryPreview = queryText
	app.RawResponse = ""
	app.RawState = "neutral"
	app.QueryState = "running"
	app.QueryStateText = "Выполняется"
	app.PlaceholderText = "Запрос выполняется..."
	app.PlaceholderError = false
	app.ResultRows = nil
	app.ResultHeaders = nil
	app.ColumnOrder = nil
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil
	app.UnmaskedColumns = nil
	app.queryCancel = cancel
	app.queryRunning = true
	forceMask := app.mergedForceMask()
	allowPlain := app.mergedAllowPlain()
	skipNumeric := app.SkipNumericValues
	runtime := app.Runtime
	app.notify()
	app.mu.Unlock()

	type queryResult struct {
		session *TrustedSession
		err     error
	}
	ch := make(chan queryResult, 1)
	go func() {
		s, e := runtime.ExecuteQuery(ctx, connURL, connToken, task, queryText, mode, forceMask, allowPlain, skipNumeric)
		ch <- queryResult{session: s, err: e}
	}()

	res := <-ch
	ctxErr := ctx.Err()
	cancel()

	app.mu.Lock()
	app.queryCancel = nil
	app.queryRunning = false
	app.mu.Unlock()

	if res.err != nil {
		errMsg := res.err.Error()
		if ctxErr == context.Canceled {
			errMsg = "Запрос отменён пользователем."
		} else if ctxErr == context.DeadlineExceeded {
			errMsg = "Таймаут выполнения запроса (300 сек)."
		}
		app.onQueryFailed(task, mode, errMsg)
		return map[string]any{
			"ok":      false,
			"mode":    mode,
			"task":    task,
			"error":   "query_failed",
			"message": errMsg,
		}
	}

	session := res.session
	app.onSessionReady(session)

	if fromBridge {
		app.mu.Lock()
		app.recordLowRowResult(len(session.MaskedRows))
		app.mu.Unlock()
	}

	if session.ResultIsEmpty {
		response := map[string]any{
			"ok":         false,
			"session_id": session.SessionID,
			"mode":       mode,
			"task":       task,
			"error":      "no_data",
			"message":    "Запрос выполнился, но не вернул строк.",
			"diagnostic": session.Diagnostic,
		}
		if mode == "masked" {
			response["masked_bundle"] = MaskedBundle(session)
		}
		return response
	}

	if mode == "masked" {
		if fromBridge {
			app.mu.RLock()
			autoSend := app.AutoSendToAgent
			app.mu.RUnlock()

			if autoSend {
				app.mu.RLock()
				bundleText := app.BundleText
				app.mu.RUnlock()
				if bundleText == "" {
					bundleText = MaskedBundle(session)
				}
				return map[string]any{
					"ok":            true,
					"session_id":    session.SessionID,
					"mode":          mode,
					"task":          task,
					"row_count":     len(session.MaskedRows),
					"masked_bundle": bundleText,
				}
			}
			return map[string]any{
				"ok":         true,
				"session_id": session.SessionID,
				"mode":       mode,
				"task":       task,
				"row_count":  len(session.MaskedRows),
				"status":     "awaiting_approval",
				"message":    "Данные показаны в интерфейсе. Пользователь решит, что отправить. Используйте pull_note для получения данных после одобрения.",
			}
		}
		app.mu.RLock()
		bundleText := app.BundleText
		app.mu.RUnlock()
		if bundleText == "" {
			bundleText = MaskedBundle(session)
		}
		return map[string]any{
			"ok":            true,
			"session_id":    session.SessionID,
			"mode":          mode,
			"task":          task,
			"row_count":     len(session.MaskedRows),
			"masked_bundle": bundleText,
		}
	}
	return map[string]any{
		"ok":         true,
		"session_id": session.SessionID,
		"mode":       mode,
		"task":       task,
		"status":     "displayed_locally",
	}
}

func (app *TrustedWebApp) bridgeApplyAnalysis(sessionID *string, analysisText string) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	session := app.CurrentSession

	if session == nil || session.Mode != "masked" {
		return map[string]any{"ok": false, "error": "Нет активной masked-сессии."}
	}
	if sessionID != nil && *sessionID != "" && *sessionID != session.SessionID {
		return map[string]any{"ok": false, "error": "Session mismatch: analysis belongs to another masked-session."}
	}
	if analysisText == "" {
		return map[string]any{"ok": false, "error": "Analysis text is empty."}
	}

	display := app.Runtime.ApplyAnalysis(session, analysisText)
	app.AnalysisMasked = analysisText
	app.AnalysisDisplay = display
	app.ActiveTab = "analysis"
	app.StatusText = "Анализ локально расшифрован и показан только в приложении."
	app.notify()
	return map[string]any{
		"ok":         true,
		"session_id": session.SessionID,
		"status":     "displayed_locally",
	}
}

func (app *TrustedWebApp) bridgeExecuteCode(task, code string, fromBridge bool) map[string]any {
	app.mu.RLock()
	url := app.ConnectedURL
	token := app.ConnectedToken
	verified := app.ConnectionVerified
	app.mu.RUnlock()

	if !verified || url == "" {
		return map[string]any{"ok": false, "message": "Нет подключения к 1С"}
	}

	if fromBridge {
		app.mu.RLock()
		autoSend := app.AutoSendToAgent
		app.mu.RUnlock()

		if autoSend {
			result := app.executeCodeDirect(task, code, url, token)
			okVal, _ := result["ok"].(bool)
			if !okVal {
				return result
			}
			app.mu.RLock()
			session := app.CurrentSession
			app.mu.RUnlock()
			if session != nil && session.Mode == "masked" {
				bundleText := MaskedBundle(session)
				return map[string]any{
					"ok":            true,
					"session_id":    session.SessionID,
					"mode":          session.Mode,
					"task":          task,
					"row_count":     len(session.MaskedRows),
					"masked_bundle": bundleText,
				}
			}
			return result
		}

		// Manual mode: show code in UI for user approval
		app.mu.Lock()
		app.PendingCode = code
		app.PendingCodeTask = task
		app.CodeMode = true
		app.TaskText = task
		if app.TaskText == "" {
			app.TaskText = "Выполнение кода"
		}
		app.QueryPreview = code
		app.RawResponse = ""
		app.RawState = "neutral"
		app.QueryState = "running"
		app.QueryStateText = "Ожидание одобрения кода"
		app.ActiveTab = "raw"
		app.StatusText = "Агент хочет выполнить код. Проверьте и нажмите \"Выполнить\"."
		app.PlaceholderText = ""
		app.PlaceholderError = false
		app.notify()
		app.mu.Unlock()

		return map[string]any{
			"ok":      true,
			"status":  "awaiting_approval",
			"task":    task,
			"message": "Код показан пользователю для проверки. Дождитесь одобрения и заберите результат через gateway_pull_note.",
		}
	}

	return app.executeCodeDirect(task, code, url, token)
}

// executeCodeDirect runs BSL code and updates UI with results.
func (app *TrustedWebApp) executeCodeDirect(task, code, url, token string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	session, err := app.Runtime.ExecuteCode(ctx, url, token, task, code)
	if err != nil {
		app.onQueryFailed(task, "code_masked", err.Error())
		return map[string]any{"ok": false, "message": err.Error()}
	}

	app.mu.Lock()
	app.CurrentSession = session
	app.TaskText = session.Task
	if app.TaskText == "" {
		app.TaskText = "Выполнение кода"
	}
	app.queryVersion++
	app.QueryPreview = session.CodeText
	app.RawResponse = session.RawResultPreview
	app.RawState = "success"
	app.QueryState = "done"
	app.QueryStateText = "Выполнено"
	app.BundleText = ""
	app.PlaceholderText = ""
	app.PlaceholderError = false

	if session.Mode == "masked" {
		app.extractRows(session.DisplayRows, session.ColumnOrder)
		app.extractMaskedRows(session.MaskedRows, session.MaskedColumns, session.UnmaskedColumns)
		app.ActiveTab = "result"
		app.AnalysisMasked = ""
		app.AnalysisDisplay = ""
		app.RowsTruncated = false
		app.StatusText = fmt.Sprintf("Код выполнен (JSON). Строк: %d, замаскировано полей: %d",
			len(session.MaskedRows), len(session.MaskedColumns))
	} else {
		app.AnalysisMasked = session.MaskedResult
		app.AnalysisDisplay = session.RawResultPreview
		app.MaskedColumns = nil
		app.UnmaskedColumns = nil
		app.RowsTruncated = false
		app.ActiveTab = "analysis"
		app.StatusText = fmt.Sprintf("Код выполнен (текст). Замаскировано сущностей: %d", len(session.AliasToOriginal))
	}

	app.notify()
	app.mu.Unlock()

	return map[string]any{
		"ok":              true,
		"session_id":      session.SessionID,
		"mode":            session.Mode,
		"masked_entities": len(session.AliasToOriginal),
	}
}

func (app *TrustedWebApp) bridgeClearSession() map[string]any {
	app.mu.Lock()
	app.clearSessionLocked()
	app.mu.Unlock()
	return map[string]any{"ok": true, "status": "cleared"}
}

func (app *TrustedWebApp) bridgePullNote(clearAfterRead bool) map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	note := app.PendingAgentNote
	if note == nil {
		return map[string]any{"ok": true, "has_note": false}
	}
	response := map[string]any{"ok": true, "has_note": true}
	for k, v := range note {
		response[k] = v
	}
	if clearAfterRead {
		app.PendingAgentNote = nil
		app.notify()
	}
	return response
}

// ── Session lifecycle ──────────────────────────────────────────

func (app *TrustedWebApp) onSessionReady(session *TrustedSession) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.CurrentSession = session

	app.CodeMode = false
	app.PendingCode = ""
	app.PendingCodeTask = ""

	app.TaskText = session.Task
	if app.TaskText == "" {
		app.TaskText = "Задача без названия"
	}
	app.extractRows(session.DisplayRows, session.ColumnOrder)
	app.extractMaskedRows(session.MaskedRows, session.MaskedColumns, session.UnmaskedColumns)
	app.queryVersion++
	app.QueryPreview = session.QueryText
	app.RawResponse = session.RawResultPreview
	app.AnalysisMasked = ""
	app.AnalysisDisplay = ""

	if trunc, ok := session.Diagnostic["rows_truncated"].(bool); ok {
		app.RowsTruncated = trunc
	}
	if mx, ok := session.Diagnostic["max_rows"].(int); ok {
		app.MaxRows = mx
	}
	if total, ok := session.Diagnostic["total_parsed_rows"].(int); ok {
		app.TotalRowCount = total
	}

	if session.Mode == "masked" {
		app.BundleText = MaskedBundle(session)
		app.ActiveTab = "result"
	} else {
		app.BundleText = ""
		app.ActiveTab = "result"
	}

	if session.ResultIsEmpty {
		app.StatusText = "Запрос выполнен, но строки не найдены."
		app.QueryState = "warning"
		app.QueryStateText = "Нет данных"
		app.RawState = "warning"
		app.PlaceholderText = "Запрос выполнен, но строки результата не найдены."
		app.PlaceholderError = false
	} else {
		if session.Mode == "masked" {
			app.StatusText = "Masked bundle готов. Контроллер может передать его для анализа."
		} else {
			app.StatusText = "Результат показан локально. Данные наружу не отправлялись."
		}
		app.QueryState = "success"
		app.QueryStateText = "Успешно"
		app.RawState = "success"
	}
	app.notify()
}

func (app *TrustedWebApp) onQueryFailed(task, mode, message string) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if task == "" {
		task = "Задача без названия"
	}
	app.TaskText = task
	app.ResultRows = nil
	app.ResultHeaders = nil
	app.ColumnOrder = nil
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil
	app.UnmaskedColumns = nil
	app.BundleText = ""
	app.AnalysisMasked = ""
	app.AnalysisDisplay = ""
	app.PlaceholderText = "Запрос завершился ошибкой. Подробности переданы контроллеру."
	app.PlaceholderError = true
	app.StatusText = "Ошибка выполнения запроса."
	app.QueryState = "error"
	app.QueryStateText = "Ошибка"
	if message != "" {
		app.RawResponse = message
	} else {
		app.RawResponse = "Неизвестная ошибка"
	}
	app.RawState = "error"
	app.ActiveTab = "raw"
	app.notify()
}

func (app *TrustedWebApp) clearSessionLocked() {
	if app.CurrentSession != nil {
		app.CurrentSession.ClearSensitive()
	}
	app.CurrentSession = nil
	app.ResultRows = nil
	app.ResultHeaders = nil
	app.ColumnOrder = nil
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil
	app.UnmaskedColumns = nil
	app.BundleText = ""
	app.AnalysisMasked = ""
	app.AnalysisDisplay = ""
	app.QueryPreview = ""
	app.RawResponse = ""
	app.RawState = "neutral"
	app.PendingAgentNote = nil
	app.ExcludedFields = ""
	app.SuggestedFields = nil
	app.resetRateLimit()
	app.TaskText = "Ожидаю задачу от контроллера"
	app.StatusText = "Сессия очищена из памяти."
	app.QueryState = "idle"
	app.QueryStateText = "Ожидание"
	app.PlaceholderText = "Результат появится здесь после выполнения запроса."
	app.PlaceholderError = false
	app.RowsTruncated = false
	app.MaxRows = 0
	app.TotalRowCount = 0
	app.ActiveTab = "result"
	app.notify()
}

func (app *TrustedWebApp) handleCancelQuery() map[string]any {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.queryCancel != nil {
		app.queryCancel()
		app.queryCancel = nil
		app.queryRunning = false
		app.QueryState = "idle"
		app.QueryStateText = "Отменён"
		app.StatusText = "Запрос отменён пользователем."
		app.PlaceholderText = "Запрос был отменён."
		app.PlaceholderError = false
		app.notify()
		return map[string]any{"ok": true, "status": "cancelled"}
	}
	return map[string]any{"ok": false, "error": "Нет активного запроса."}
}

func (app *TrustedWebApp) extractRows(rows []map[string]any, columnOrder []string) {
	app.ResultRows = rows
	app.ColumnOrder = columnOrder
	app.ResultHeaders = extractHeadersFromRows(rows, columnOrder)
}

func (app *TrustedWebApp) extractMaskedRows(rows []map[string]any, maskedCols, unmaskedCols []string) {
	app.MaskedResultRows = rows
	app.MaskedColumns = maskedCols
	app.UnmaskedColumns = unmaskedCols
	app.MaskedResultHeaders = extractHeadersFromRows(rows, app.ColumnOrder)
	app.dataVersion++
}

func (app *TrustedWebApp) applyFilteredBundle(rawIndices []any) {
	app.mu.Lock()
	defer app.mu.Unlock()
	session := app.CurrentSession
	if session == nil {
		return
	}
	indices := make([]int, 0, len(rawIndices))
	for _, v := range rawIndices {
		if f, ok := v.(float64); ok {
			idx := int(f)
			if idx >= 0 && idx < len(session.MaskedRows) {
				indices = append(indices, idx)
			}
		}
	}
	if len(indices) == 0 || len(indices) == len(session.MaskedRows) {
		return
	}
	filteredMasked := make([]map[string]any, len(indices))
	for i, idx := range indices {
		filteredMasked[i] = session.MaskedRows[idx]
	}
	origMasked := session.MaskedRows
	session.MaskedRows = filteredMasked
	session.cachedBundle = ""
	app.BundleText = MaskedBundle(session)
	session.MaskedRows = origMasked
	session.cachedBundle = ""
}

func extractHeadersFromRows(rows []map[string]any, columnOrder []string) []string {
	if len(columnOrder) > 0 {
		seen := make(map[string]bool)
		for _, col := range columnOrder {
			seen[col] = true
		}
		result := append([]string{}, columnOrder...)
		for _, row := range rows {
			for key := range row {
				if !seen[key] {
					seen[key] = true
					result = append(result, key)
				}
			}
		}
		return result
	}
	var headers []string
	seen := make(map[string]bool)
	for _, row := range rows {
		for key := range row {
			if !seen[key] {
				seen[key] = true
				headers = append(headers, key)
			}
		}
	}
	return headers
}

func (app *TrustedWebApp) securityHint() string {
	url := app.ConnectedURL
	if strings.HasPrefix(url, "https://") {
		return "Транспорт: HTTPS"
	}
	if strings.HasPrefix(url, "http://localhost") || strings.HasPrefix(url, "http://127.0.0.1") {
		return "Транспорт: local HTTP"
	}
	if strings.HasPrefix(url, "http://") {
		return "Транспорт: HTTP без TLS"
	}
	return "Транспорт: не задан"
}

func (app *TrustedWebApp) mergedForceMask() map[string]bool {
	combined := make(map[string]bool)
	for k := range csvFields(app.Config.Defaults.ForceMaskFields) {
		combined[k] = true
	}
	for k := range csvFields(app.PersistentForceMask) {
		combined[k] = true
	}
	for k := range csvFields(app.ForceMaskFields) {
		combined[k] = true
	}
	sessionAllow := csvFields(app.AllowPlainFields)
	for k := range sessionAllow {
		delete(combined, k)
	}
	persistAllow := csvFields(app.PersistentAllowPlain)
	for k := range persistAllow {
		delete(combined, k)
	}
	return combined
}

func (app *TrustedWebApp) mergedAllowPlain() map[string]bool {
	combined := csvFields(app.Config.Defaults.AllowPlainFields)
	for k := range csvFields(app.PersistentAllowPlain) {
		combined[k] = true
	}
	for k := range csvFields(app.AllowPlainFields) {
		combined[k] = true
	}
	return combined
}

// Shutdown cleans up all resources.
func (app *TrustedWebApp) Shutdown() {
	app.mu.Lock()
	app.clearSessionLocked()
	app.ConnectedToken = ""
	app.ConnectionVerified = false
	app.mu.Unlock()
}
