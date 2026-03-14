package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWebHost     = "127.0.0.1"
	DefaultWebPort     = 8767
	maxRequestBody     = 1 * 1024 * 1024 // 1 MB
)

// TrustedWebApp holds all application state and logic.
type TrustedWebApp struct {
	Config          *AppConfig
	Runtime         *TrustedGatewayRuntime
	CurrentSession  *TrustedSession
	ConnectedURL    string
	ConnectedToken  string
	ConnectionVerified bool
	HasSavedSettings   bool
	ResultRows      []map[string]any
	ResultHeaders   []string
	MaskedResultRows    []map[string]any
	MaskedResultHeaders []string
	MaskedColumns       []string
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
	ForceMaskFields string
	AllowPlainFields string
	ActiveTab       string
	PlaceholderText string
	PlaceholderError bool

	SessionToken string
	Bridge       *TrustedBridgeServer

	queryCancel  context.CancelFunc
	queryRunning bool
	sessionLock  sync.Mutex
	stateVersion int
	stateEvent   chan struct{}
}

// NewTrustedWebApp creates a new web app instance.
func NewTrustedWebApp(config *AppConfig, savedToken string) *TrustedWebApp {
	app := &TrustedWebApp{
		Config:          config,
		Runtime:         NewTrustedGatewayRuntime(config),
		ConnectedURL:    config.Mcp.URL,
		ConnectedToken:  savedToken,
		HasSavedSettings: savedToken != "",
		ThemeName:       "dark",
		TaskText:        "Ожидаю задачу от контроллера",
		StatusText:      "Готово.",
		QueryState:      "idle",
		QueryStateText:  "Ожидание",
		RawState:        "neutral",
		PlaceholderText: "Результат появится здесь после выполнения запроса.",
		SessionToken:    generateToken(24),
		stateEvent:      make(chan struct{}, 1),
	}

	app.Bridge = NewTrustedBridgeServer(
		BridgeCallbacks{
			Status:       app.bridgeStatus,
			RunQuery:     app.bridgeRunQuery,
			ApplyAnalysis: app.bridgeApplyAnalysis,
			ClearSession: app.bridgeClearSession,
			PullNote:     app.bridgePullNote,
		},
		DefaultBridgeHost,
		DefaultBridgePort,
	)

	return app
}

func (app *TrustedWebApp) notify() {
	app.stateVersion++
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

// GetState returns the current application state as a map.
func (app *TrustedWebApp) GetState() map[string]any {
	app.sessionLock.Lock()
	session := app.CurrentSession
	app.sessionLock.Unlock()

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
		"version": app.stateVersion,
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
		"bridge_info":      fmt.Sprintf("Контроллер: %s:%d", DefaultBridgeHost, DefaultBridgePort),
		"bridge_secret":    app.Bridge.BridgeSecret,
		"result": map[string]any{
			"headers":           app.ResultHeaders,
			"rows":              app.ResultRows,
			"placeholder":       resultPlaceholder,
			"placeholder_error": app.PlaceholderError,
		},
		"masked_result": map[string]any{
			"headers":        app.MaskedResultHeaders,
			"rows":           app.MaskedResultRows,
			"masked_columns": app.MaskedColumns,
		},
		"bundle_text":         app.BundleText,
		"analysis_masked":     app.AnalysisMasked,
		"analysis_display":    app.AnalysisDisplay,
		"query_preview":       app.QueryPreview,
		"raw_response":        app.RawResponse,
		"raw_state":           app.RawState,
		"active_tab":          app.ActiveTab,
		"query_running":       app.queryRunning,
		"has_saved_settings":  app.HasSavedSettings,
		"has_saved_token":     app.ConnectedToken != "",
		"defaults_force_mask": app.Config.Defaults.ForceMaskFields,
		"defaults_allow_plain": app.Config.Defaults.AllowPlainFields,
	}
}

// HandleConnect handles a connection attempt to the MCP server.
func (app *TrustedWebApp) HandleConnect(data map[string]any) map[string]any {
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
	saltDisplay := "(авто: из ключа сервера)"
	if app.Config.Privacy.Salt != "" {
		saltDisplay = "***"
	}
	return map[string]any{
		"mcp_url":                     firstNonEmpty(app.Config.Mcp.URL, app.ConnectedURL),
		"mcp_token_saved":             app.ConnectedToken != "" && app.HasSavedSettings,
		"mcp_timeout_seconds":         app.Config.Mcp.TimeoutSeconds,
		"mcp_sse_read_timeout_seconds": app.Config.Mcp.SSEReadTimeoutSeconds,
		"privacy_salt":                saltDisplay,
		"privacy_salt_env":            app.Config.Privacy.SaltEnv,
		"privacy_alias_length":        app.Config.Privacy.AliasLength,
		"privacy_show_masked":         app.Config.Privacy.ShowMaskedDataInViewer,
		"defaults_preview_chars":      app.Config.Defaults.ResultPreviewChars,
		"defaults_auto_execute":       app.Config.Defaults.ExecuteWithoutConfirmation,
		"defaults_force_mask_fields":  app.Config.Defaults.ForceMaskFields,
		"defaults_allow_plain_fields": app.Config.Defaults.AllowPlainFields,
		"has_saved_settings":          app.HasSavedSettings,
	}
}

// HandleSaveSettings persists settings to encrypted storage and reloads runtime.
func (app *TrustedWebApp) HandleSaveSettings(data map[string]any) map[string]any {
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
			"show_masked_data_in_viewer": getBoolField(data, "privacy_show_masked"),
		},
		"defaults": map[string]any{
			"result_preview_chars":         getIntField(data, "defaults_preview_chars", 4000),
			"execute_without_confirmation": getBoolField(data, "defaults_auto_execute"),
			"force_mask_fields":            getStringFieldDefault(data, "defaults_force_mask_fields", ""),
			"allow_plain_fields":           getStringFieldDefault(data, "defaults_allow_plain_fields", ""),
		},
		"auth": map[string]any{
			"token": firstNonEmpty(getStringFieldDefault(data, "mcp_token", ""), app.ConnectedToken),
		},
	}

	if err := SaveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}

	newConfig := ConfigFromDict(settings)
	app.Config = newConfig
	app.Runtime = NewTrustedGatewayRuntime(newConfig)
	app.ConnectedURL = newConfig.Mcp.URL
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

// HandleImportSettings imports settings from a config.json-style dict.
func (app *TrustedWebApp) HandleImportSettings(data map[string]any) map[string]any {
	settings := sanitizeImport(data)
	token := ""
	if authMap, ok := settings["auth"].(map[string]any); ok {
		if t, ok := authMap["token"].(string); ok {
			token = t
		}
	}
	if err := SaveSettings(settings); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	newConfig := ConfigFromDict(settings)
	app.Config = newConfig
	app.Runtime = NewTrustedGatewayRuntime(newConfig)
	app.ConnectedURL = newConfig.Mcp.URL
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
	app.ForceMaskFields = forceMask
	app.AllowPlainFields = allowPlain
	return app.bridgeRunQuery(task, queryText, mode)
}

// ── Bridge callbacks ────────────────────────────────────────────

func (app *TrustedWebApp) bridgeStatus() map[string]any {
	app.sessionLock.Lock()
	session := app.CurrentSession
	app.sessionLock.Unlock()

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

func (app *TrustedWebApp) bridgeRunQuery(task, queryText, mode string) map[string]any {
	if mode != "direct" && mode != "masked" {
		return map[string]any{"ok": false, "error": "mode must be direct or masked"}
	}
	if !app.ConnectionVerified || app.ConnectedURL == "" {
		return map[string]any{"ok": false, "error": "Сначала введите ключ и нажмите 'Подключиться'."}
	}
	if queryText == "" {
		return map[string]any{"ok": false, "error": "QueryText is empty."}
	}

	if task == "" {
		task = "Задача без названия"
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
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	app.queryCancel = cancel
	app.queryRunning = true
	app.notify()

	type queryResult struct {
		session *TrustedSession
		err     error
	}
	ch := make(chan queryResult, 1)
	go func() {
		s, e := app.Runtime.ExecuteQuery(
			ctx,
			app.ConnectedURL,
			app.ConnectedToken,
			task,
			queryText,
			mode,
			app.mergedForceMask(),
			app.mergedAllowPlain(),
		)
		ch <- queryResult{session: s, err: e}
	}()

	res := <-ch
	// Save ctx state BEFORE calling cancel(), otherwise ctx.Err() always returns Canceled
	ctxErr := ctx.Err()
	cancel()
	app.queryCancel = nil
	app.queryRunning = false

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
		return map[string]any{
			"ok":            true,
			"session_id":    session.SessionID,
			"mode":          mode,
			"task":          task,
			"row_count":     len(session.MaskedRows),
			"masked_bundle": MaskedBundle(session),
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
	app.sessionLock.Lock()
	session := app.CurrentSession
	app.sessionLock.Unlock()

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

func (app *TrustedWebApp) bridgeClearSession() map[string]any {
	app.clearSession()
	return map[string]any{"ok": true, "status": "cleared"}
}

func (app *TrustedWebApp) bridgePullNote(clearAfterRead bool) map[string]any {
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

// ── Internal ────────────────────────────────────────────────────

func (app *TrustedWebApp) onSessionReady(session *TrustedSession) {
	app.sessionLock.Lock()
	app.CurrentSession = session
	app.sessionLock.Unlock()

	app.TaskText = session.Task
	if app.TaskText == "" {
		app.TaskText = "Задача без названия"
	}
	app.extractRows(session.DisplayRows)
	app.extractMaskedRows(session.MaskedRows, session.MaskedColumns)
	app.QueryPreview = session.QueryText
	app.RawResponse = session.RawResultPreview
	app.AnalysisMasked = ""
	app.AnalysisDisplay = ""

	if session.Mode == "masked" {
		app.BundleText = MaskedBundle(session)
		app.ActiveTab = "bundle"
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
	if task == "" {
		task = "Задача без названия"
	}
	app.TaskText = task
	app.ResultRows = nil
	app.ResultHeaders = nil
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil
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

func (app *TrustedWebApp) clearSession() {
	app.sessionLock.Lock()
	if app.CurrentSession != nil {
		app.CurrentSession.ClearSensitive()
	}
	app.CurrentSession = nil
	app.sessionLock.Unlock()

	app.ResultRows = nil
	app.ResultHeaders = nil
	app.MaskedResultRows = nil
	app.MaskedResultHeaders = nil
	app.MaskedColumns = nil
	app.BundleText = ""
	app.AnalysisMasked = ""
	app.AnalysisDisplay = ""
	app.QueryPreview = ""
	app.RawResponse = ""
	app.RawState = "neutral"
	app.PendingAgentNote = nil
	app.TaskText = "Ожидаю задачу от контроллера"
	app.StatusText = "Сессия очищена из памяти."
	app.QueryState = "idle"
	app.QueryStateText = "Ожидание"
	app.PlaceholderText = "Результат появится здесь после выполнения запроса."
	app.PlaceholderError = false
	app.ActiveTab = "result"
	app.notify()
}

func (app *TrustedWebApp) handleCancelQuery() map[string]any {
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

func (app *TrustedWebApp) extractRows(rows []map[string]any) {
	app.ResultRows = rows
	app.ResultHeaders = extractHeadersFromRows(rows)
}

func (app *TrustedWebApp) extractMaskedRows(rows []map[string]any, maskedCols []string) {
	app.MaskedResultRows = rows
	app.MaskedColumns = maskedCols
	app.MaskedResultHeaders = extractHeadersFromRows(rows)
}

func extractHeadersFromRows(rows []map[string]any) []string {
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

func (app *TrustedWebApp) mergedForceMask() map[string]bool {
	combined := csvFields(app.Config.Defaults.ForceMaskFields)
	for k := range csvFields(app.ForceMaskFields) {
		combined[k] = true
	}
	sessionAllow := csvFields(app.AllowPlainFields)
	for k := range sessionAllow {
		delete(combined, k)
	}
	return combined
}

func (app *TrustedWebApp) mergedAllowPlain() map[string]bool {
	combined := csvFields(app.Config.Defaults.AllowPlainFields)
	for k := range csvFields(app.AllowPlainFields) {
		combined[k] = true
	}
	return combined
}

// Shutdown cleans up all resources.
func (app *TrustedWebApp) Shutdown() {
	app.clearSession()
	app.ConnectedToken = ""
	app.ConnectionVerified = false
	app.Bridge.Stop()
}

// ── HTTP Server ─────────────────────────────────────────────────

// WebHTTPServer wraps http.Server with the app reference.
type WebHTTPServer struct {
	App    *TrustedWebApp
	server *http.Server
}

// NewWebHTTPServer creates a new HTTP server for the web UI.
func NewWebHTTPServer(host string, port int, app *TrustedWebApp) *WebHTTPServer {
	ws := &WebHTTPServer{App: app}
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.handleRoot)
	mux.HandleFunc("/favicon.ico", ws.handleFavicon)
	mux.HandleFunc("/api/state", ws.handleAPIState)
	mux.HandleFunc("/api/events", ws.handleAPIEvents)
	mux.HandleFunc("/api/settings", ws.handleAPISettings)
	mux.HandleFunc("/api/connect", ws.handleAPIConnect)
	mux.HandleFunc("/api/disconnect", ws.handleAPIDisconnect)
	mux.HandleFunc("/api/query", ws.handleAPIQuery)
	mux.HandleFunc("/api/cancel_query", ws.handleAPICancelQuery)
	mux.HandleFunc("/api/apply_analysis", ws.handleAPIApplyAnalysis)
	mux.HandleFunc("/api/clear_session", ws.handleAPIClearSession)
	mux.HandleFunc("/api/submit_note", ws.handleAPISubmitNote)
	mux.HandleFunc("/api/clear_note", ws.handleAPIClearNote)
	mux.HandleFunc("/api/theme", ws.handleAPITheme)
	mux.HandleFunc("/api/settings/reset", ws.handleAPISettingsReset)
	mux.HandleFunc("/api/settings/import", ws.handleAPISettingsImport)

	ws.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: mux,
	}
	return ws
}

// ListenAndServe starts the HTTP server.
func (ws *WebHTTPServer) ListenAndServe() error {
	return ws.server.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (ws *WebHTTPServer) ShutdownServer() {
	ws.server.Close()
}

func (ws *WebHTTPServer) checkToken(r *http.Request) bool {
	parsed, _ := url.Parse(r.RequestURI)
	qs := parsed.Query()
	tokenFromQS := qs.Get("token")
	tokenFromHeader := r.Header.Get("X-Session-Token")
	return tokenFromQS == ws.App.SessionToken || tokenFromHeader == ws.App.SessionToken
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(data)
}

func respondHTML(w http.ResponseWriter, status int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(html))
}

func (ws *WebHTTPServer) readJSON(r *http.Request) (map[string]any, error) {
	if r.ContentLength > maxRequestBody {
		return nil, fmt.Errorf("request body too large")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (ws *WebHTTPServer) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(204)
}

func (ws *WebHTTPServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		respondJSON(w, 404, map[string]any{"error": "Not found"})
		return
	}
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	html := RenderAppHTML(ws.App.SessionToken)
	respondHTML(w, 200, html)
}

func (ws *WebHTTPServer) handleAPIState(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.GetState())
}

func (ws *WebHTTPServer) handleAPIEvents(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	flusher.Flush()

	lastVersion := 0
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ws.App.waitForChange(15 * time.Second)

		current := ws.App.stateVersion
		if current != lastVersion {
			lastVersion = current
			fmt.Fprintf(w, "data: {\"version\": %d}\n\n", current)
		} else {
			fmt.Fprintf(w, ": keepalive\n\n")
		}
		flusher.Flush()
	}
}

func (ws *WebHTTPServer) handleAPISettings(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	if r.Method == "GET" {
		respondJSON(w, 200, ws.App.HandleGetSettings())
		return
	}
	if r.Method == "POST" {
		data, err := ws.readJSON(r)
		if err != nil {
			respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
			return
		}
		respondJSON(w, 200, ws.App.HandleSaveSettings(data))
		return
	}
	respondJSON(w, 405, map[string]any{"error": "Method not allowed"})
}

func (ws *WebHTTPServer) handleAPIConnect(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	respondJSON(w, 200, ws.App.HandleConnect(data))
}

func (ws *WebHTTPServer) handleAPIDisconnect(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.HandleDisconnect())
}

func (ws *WebHTTPServer) handleAPIQuery(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	respondJSON(w, 200, ws.App.HandleQuery(data))
}

func (ws *WebHTTPServer) handleAPICancelQuery(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.handleCancelQuery())
}

func (ws *WebHTTPServer) handleAPIApplyAnalysis(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	var sessionID *string
	if v, ok := data["session_id"]; ok && v != nil {
		s := fmt.Sprintf("%v", v)
		sessionID = &s
	}
	analysisText := getStringFieldDefault(data, "analysis_text", "")
	result := ws.App.bridgeApplyAnalysis(sessionID, analysisText)
	respondJSON(w, 200, result)
}

func (ws *WebHTTPServer) handleAPIClearSession(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.bridgeClearSession())
}

func (ws *WebHTTPServer) handleAPISubmitNote(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	message := strings.TrimSpace(getStringFieldDefault(data, "message", ""))
	if message == "" {
		respondJSON(w, 200, map[string]any{"ok": false, "error": "Message is empty."})
		return
	}
	ws.App.sessionLock.Lock()
	session := ws.App.CurrentSession
	ws.App.sessionLock.Unlock()

	sessionID := ""
	if session != nil {
		sessionID = session.SessionID
	}
	ws.App.PendingAgentNote = map[string]string{
		"message":    message,
		"session_id": sessionID,
		"task":       ws.App.TaskText,
	}
	ws.App.StatusText = "Сообщение для агента сохранено в мосте."
	ws.App.notify()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPIClearNote(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	ws.App.PendingAgentNote = nil
	ws.App.notify()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPITheme(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	theme := getStringFieldDefault(data, "theme", "dark")
	if theme != "dark" {
		theme = "light"
	}
	ws.App.ThemeName = theme
	ws.App.notify()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPISettingsReset(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.HandleResetSettings())
}

func (ws *WebHTTPServer) handleAPISettingsImport(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	respondJSON(w, 200, ws.App.HandleImportSettings(data))
}

// ── Helpers ─────────────────────────────────────────────────────

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
		"show_masked_data_in_viewer": true,
	},
	"defaults": {
		"result_preview_chars": true, "execute_without_confirmation": true,
		"force_mask_fields": true, "allow_plain_fields": true,
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

