package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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
	mux.HandleFunc("/ui.css", ws.handleUICSS)
	mux.HandleFunc("/ui_core.js", ws.handleUICoreJS)
	mux.HandleFunc("/ui_results.js", ws.handleUIResultsJS)
	mux.HandleFunc("/ui_actions.js", ws.handleUIActionsJS)
	mux.HandleFunc("/ui_settings.js", ws.handleUISettingsJS)
	mux.HandleFunc("/ui_onboarding.js", ws.handleUIOnboardingJS)
	mux.HandleFunc("/ui_editor.js", ws.handleUIEditorJS)
	mux.HandleFunc("/api/state", ws.handleAPIState)
	mux.HandleFunc("/api/rows", ws.handleAPIRows)
	mux.HandleFunc("/api/events", ws.handleAPIEvents)
	mux.HandleFunc("/api/settings", ws.handleAPISettings)
	mux.HandleFunc("/api/connect", ws.handleAPIConnect)
	mux.HandleFunc("/api/disconnect", ws.handleAPIDisconnect)
	mux.HandleFunc("/api/query", ws.handleAPIQuery)
	mux.HandleFunc("/api/cancel_query", ws.handleAPICancelQuery)
	mux.HandleFunc("/api/apply_analysis", ws.handleAPIApplyAnalysis)
	mux.HandleFunc("/api/clear_session", ws.handleAPIClearSession)
	mux.HandleFunc("/api/execute_code", ws.handleAPIExecuteCode)
	mux.HandleFunc("/api/approve_code", ws.handleAPIApproveCode)
	mux.HandleFunc("/api/reject_code", ws.handleAPIRejectCode)
	mux.HandleFunc("/api/code_mode", ws.handleAPICodeMode)
	mux.HandleFunc("/api/ner/export_template", ws.handleAPINerExportTemplate)
	mux.HandleFunc("/api/ner/reload", ws.handleAPINerReload)
	mux.HandleFunc("/api/ner/status", ws.handleAPINerStatus)
	mux.HandleFunc("/api/submit_note", ws.handleAPISubmitNote)
	mux.HandleFunc("/api/clear_note", ws.handleAPIClearNote)
	mux.HandleFunc("/api/theme", ws.handleAPITheme)
	mux.HandleFunc("/api/settings/reset", ws.handleAPISettingsReset)
	mux.HandleFunc("/api/settings/import", ws.handleAPISettingsImport)
	mux.HandleFunc("/api/settings/export", ws.handleAPISettingsExport)
	mux.HandleFunc("/api/remask", ws.handleAPIRemask)
	mux.HandleFunc("/api/set_whitelist", ws.handleAPISetWhitelist)
	mux.HandleFunc("/api/set_type_policy", ws.handleAPISetTypePolicy)
	mux.HandleFunc("/api/exclude_fields", ws.handleAPIExcludeFields)
	mux.HandleFunc("/api/suggest_fields", ws.handleAPISuggestFields)
	mux.HandleFunc("/api/confirm_suggested_fields", ws.handleAPIConfirmSuggestedFields)
	mux.HandleFunc("/api/approve_send", ws.handleAPIApproveSend)
	mux.HandleFunc("/api/auto_send", ws.handleAPIAutoSend)
	mux.HandleFunc("/api/skip_numeric", ws.handleAPISkipNumeric)
	mux.HandleFunc("/api/logs", ws.handleAPILogs)
	mux.HandleFunc("/api/logs/clear", ws.handleAPILogsClear)
	mux.HandleFunc("/api/logs/entry", ws.handleAPILogsEntry)
	mux.HandleFunc("/api/icon", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(embeddedIcon)
	})

	// MCP server (streamable HTTP) — no auth, localhost only
	mcpSrv := NewMcpServer(app)
	mux.HandleFunc("/mcp", mcpSrv.handleMcp)

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

// ShutdownServer gracefully stops the HTTP server.
func (ws *WebHTTPServer) ShutdownServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws.server.Shutdown(ctx)
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

func respondCSS(w http.ResponseWriter, status int, css string) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(css))
}

func respondJavaScript(w http.ResponseWriter, status int, js string) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write([]byte(js))
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

func safeSlice(rows []map[string]any, offset, limit int) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	if offset >= len(rows) {
		return []map[string]any{}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}

// ── Handlers ───────────────────────────────────────────────────

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

func (ws *WebHTTPServer) handleUICSS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondCSS(w, 200, uiCSS)
}

func (ws *WebHTTPServer) handleUICoreJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, RenderUICoreJS(ws.App.SessionToken))
}

func (ws *WebHTTPServer) handleUIResultsJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, uiResultsJS)
}

func (ws *WebHTTPServer) handleUIActionsJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, uiActionsJS)
}

func (ws *WebHTTPServer) handleUISettingsJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, uiSettingsJS)
}

func (ws *WebHTTPServer) handleUIOnboardingJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, uiOnboardingJS)
}

func (ws *WebHTTPServer) handleUIEditorJS(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJavaScript(w, 200, uiEditorJS)
}

func (ws *WebHTTPServer) handleAPIState(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.GetState())
}

func (ws *WebHTTPServer) handleAPIRows(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	app := ws.App
	app.mu.RLock()
	resultSlice := safeSlice(app.ResultRows, offset, limit)
	maskedSlice := safeSlice(app.MaskedResultRows, offset, limit)
	result := map[string]any{
		"offset":           offset,
		"limit":            limit,
		"total":            len(app.ResultRows),
		"rows":             resultSlice,
		"masked_rows":      maskedSlice,
		"headers":          app.ResultHeaders,
		"masked_headers":   app.MaskedResultHeaders,
		"masked_columns":   app.MaskedColumns,
		"unmasked_columns": app.UnmaskedColumns,
	}
	app.mu.RUnlock()
	respondJSON(w, 200, result)
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

	var lastVersion int64
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ws.App.waitForChange(15 * time.Second)

		current := ws.App.stateVersion.Load()
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
	ws.App.mu.Lock()
	session := ws.App.CurrentSession
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
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPIClearNote(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	ws.App.mu.Lock()
	ws.App.PendingAgentNote = nil
	ws.App.notify()
	ws.App.mu.Unlock()
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
	ws.App.mu.Lock()
	ws.App.ThemeName = theme
	ws.App.notify()
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPISettingsReset(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.HandleResetSettings())
}

func (ws *WebHTTPServer) handleAPISettingsExport(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	result := ws.App.HandleExportSettings()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=config.json")
	json.NewEncoder(w).Encode(result)
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

func (ws *WebHTTPServer) handleAPIRemask(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	forceMask := getStringFieldDefault(data, "force_mask_fields", "")
	allowPlain := getStringFieldDefault(data, "allow_plain_fields", "")
	respondJSON(w, 200, ws.App.HandleRemask(forceMask, allowPlain))
}

func (ws *WebHTTPServer) handleAPISetWhitelist(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	allowPlain := getStringFieldDefault(data, "allow_plain_fields", "")
	forceMask := getStringFieldDefault(data, "force_mask_fields", "")
	respondJSON(w, 200, ws.App.HandleSetWhitelist(forceMask, allowPlain))
}

func (ws *WebHTTPServer) handleAPISetTypePolicy(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	// Accept either a pre-serialized JSON string, or a structured object that
	// matches PersistedTypePolicy. IMPORTANT: check the map/object case FIRST —
	// getStringFieldDefault uses fmt.Sprintf("%v", ...) which stringifies a
	// map as Go's own "map[key:value]" form, not JSON, producing unparseable
	// garbage on disk. Only fall back to the string form when the value is
	// genuinely a string.
	var policyJSON string
	raw := data["type_policy"]
	switch v := raw.(type) {
	case map[string]any:
		if b, err := json.Marshal(v); err == nil {
			policyJSON = string(b)
		}
	case string:
		policyJSON = v
	case nil:
		// empty → reset
	}
	respondJSON(w, 200, ws.App.HandleSetTypePolicy(policyJSON))
}

func (ws *WebHTTPServer) handleAPIExcludeFields(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	excluded := getStringFieldDefault(data, "excluded_fields", "")
	respondJSON(w, 200, ws.App.HandleExcludeFields(excluded))
}

func (ws *WebHTTPServer) handleAPISuggestFields(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	fieldsRaw, _ := data["fields"].([]any)
	var fields []string
	for _, f := range fieldsRaw {
		if s, ok := f.(string); ok {
			fields = append(fields, s)
		}
	}
	respondJSON(w, 200, ws.App.HandleSuggestFields(fields))
}

func (ws *WebHTTPServer) handleAPIConfirmSuggestedFields(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	respondJSON(w, 200, ws.App.HandleConfirmSuggestedFields())
}

func (ws *WebHTTPServer) handleAPIApproveSend(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, _ := ws.readJSON(r)

	// Apply filtered indices if provided (user filtered rows in Result tab)
	if data != nil {
		if rawIndices, ok := data["filtered_indices"].([]any); ok && len(rawIndices) > 0 {
			ws.App.applyFilteredBundle(rawIndices)
		}
	}

	// Store the current bundle in PendingAgentNote for pull_note retrieval
	ws.App.mu.Lock()
	session := ws.App.CurrentSession
	bundleText := ws.App.BundleText
	sessionID := ""
	task := ws.App.TaskText
	if session != nil {
		sessionID = session.SessionID
		if bundleText == "" {
			bundleText = MaskedBundle(session)
		}
	}
	ws.App.PendingAgentNote = map[string]string{
		"message":    bundleText,
		"session_id": sessionID,
		"task":       task,
	}
	ws.App.QueryState = "success"
	ws.App.QueryStateText = "Отправлено"
	ws.App.StatusText = "Данные готовы для агента. Агент может забрать через pull_note."
	ws.App.notify()
	ws.App.mu.Unlock()

	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPIAutoSend(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	enabled, _ := data["enabled"].(bool)
	ws.App.mu.Lock()
	ws.App.AutoSendToAgent = enabled
	if enabled {
		// User consciously re-enables auto mode — reset brute-force counters
		ws.App.resetRateLimit()
	}
	ws.App.notify()
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (ws *WebHTTPServer) handleAPISkipNumeric(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	enabled, _ := data["enabled"].(bool)
	ws.App.mu.Lock()
	ws.App.SkipNumericValues = enabled
	session := ws.App.CurrentSession
	if session != nil && session.Mode == "masked" && len(session.DisplayRows) > 0 {
		ws.App.remaskLocked(session)
	} else {
		ws.App.notify()
	}
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

// ── Execute Code API ────────────────────────────────────────────

func (ws *WebHTTPServer) handleAPIExecuteCode(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	task := getStringFieldDefault(data, "task", "")
	code := getStringFieldDefault(data, "code", "")
	if code == "" {
		respondJSON(w, 400, map[string]any{"error": "code is required"})
		return
	}
	result := ws.App.bridgeExecuteCode(task, code, false)
	respondJSON(w, 200, result)
}

// handleAPIApproveCode — user approved pending code execution
func (ws *WebHTTPServer) handleAPIApproveCode(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	ws.App.mu.RLock()
	code := ws.App.PendingCode
	task := ws.App.PendingCodeTask
	url := ws.App.ConnectedURL
	token := ws.App.ConnectedToken
	ws.App.mu.RUnlock()

	if code == "" {
		respondJSON(w, 400, map[string]any{"error": "Нет кода для выполнения"})
		return
	}

	result := ws.App.executeCodeDirect(task, code, url, token)
	okVal, _ := result["ok"].(bool)
	if !okVal {
		respondJSON(w, 200, result)
		return
	}

	// Prepare masked bundle for agent (via pull_note)
	ws.App.mu.Lock()
	session := ws.App.CurrentSession
	if session != nil && session.Mode == "masked" {
		bundleText := MaskedBundle(session)
		ws.App.PendingAgentNote = map[string]string{
			"session_id": session.SessionID,
			"bundle":     bundleText,
		}
	} else if session != nil && session.Mode == "code_masked" {
		ws.App.PendingAgentNote = map[string]string{
			"session_id": session.SessionID,
			"bundle":     session.MaskedResult,
		}
	}
	ws.App.mu.Unlock()

	respondJSON(w, 200, result)
}

// handleAPIRejectCode — user rejected pending code execution
func (ws *WebHTTPServer) handleAPIRejectCode(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	ws.App.mu.Lock()
	ws.App.PendingCode = ""
	ws.App.PendingCodeTask = ""
	// CodeMode stays — user can still switch manually
	ws.App.QueryState = "idle"
	ws.App.QueryStateText = "Отклонено"
	ws.App.StatusText = "Выполнение кода отклонено пользователем."
	ws.App.PendingAgentNote = map[string]string{
		"session_id": "",
		"bundle":     "Пользователь отклонил выполнение кода.",
	}
	ws.App.notify()
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

// handleAPICodeMode — toggle between query and code editor mode
func (ws *WebHTTPServer) handleAPICodeMode(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	data, err := ws.readJSON(r)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid JSON"})
		return
	}
	enabled, _ := data["enabled"].(bool)
	ws.App.mu.Lock()
	ws.App.CodeMode = enabled
	if !enabled {
		ws.App.PendingCode = ""
		ws.App.PendingCodeTask = ""
	}
	ws.App.notify()
	ws.App.mu.Unlock()
	respondJSON(w, 200, map[string]any{"ok": true})
}

// ── NER Rules API ──────────────────────────────────────────────

func (ws *WebHTTPServer) handleAPINerExportTemplate(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	path := NerRulesPath()
	if err := ExportNerTemplate(path); err != nil {
		respondJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "path": path})
}

func (ws *WebHTTPServer) handleAPINerReload(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	path := NerRulesPath()
	rules, err := LoadNerRules(path)
	if err != nil {
		respondJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ws.App.Runtime.NerRules = rules
	status := nerRulesStatus(rules)
	respondJSON(w, 200, map[string]any{"ok": true, "status": status})
}

func (ws *WebHTTPServer) handleAPINerStatus(w http.ResponseWriter, r *http.Request) {
	rules := ws.App.Runtime.NerRules
	respondJSON(w, 200, map[string]any{"ok": true, "status": nerRulesStatus(rules)})
}

func nerRulesStatus(rules *NerRules) string {
	if rules == nil {
		return "Файл ner_rules.json не найден"
	}
	return fmt.Sprintf("Загружено: %d контекстных правил, %d keyword-масок, %d custom regex",
		len(rules.ContextPatterns), len(rules.AlwaysMaskKeywords), len(rules.CustomRegex))
}

// ── MCP call logs (UI-only) ─────────────────────────────────────
//
// These endpoints are token-protected and only registered on the local
// UI HTTP server. They are NOT exposed as MCP tools, so the agent
// cannot read the log contents.

// handleAPILogs returns a summary list of recorded MCP calls (no full text,
// only metadata + preview) so the table loads quickly.
func (ws *WebHTTPServer) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	entries := mcpLog.All()
	// Return newest first so the UI can render without reversing.
	summaries := make([]map[string]any, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		preview := e.Text
		const previewLen = 500
		if len(preview) > previewLen {
			preview = preview[:previewLen] + "…"
		}
		summaries = append(summaries, map[string]any{
			"id":             e.ID,
			"timestamp":      e.Timestamp,
			"tool":           e.Tool,
			"url":            e.URL,
			"duration_ms":    e.DurationMs,
			"is_error":       e.IsError,
			"error_message":  e.ErrorMessage,
			"text_len":       e.TextLen,
			"text_preview":   preview,
			"text_truncated": e.TextTruncated,
			"has_structured": e.HasStructured,
			"has_schema":     e.HasSchema,
		})
	}
	respondJSON(w, 200, map[string]any{
		"ok":      true,
		"count":   len(entries),
		"entries": summaries,
	})
}

// handleAPILogsEntry returns one full entry (with the full Text payload)
// so the UI can show the raw MCP response for a specific call.
func (ws *WebHTTPServer) handleAPILogsEntry(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		respondJSON(w, 400, map[string]any{"error": "Invalid id"})
		return
	}
	entry := mcpLog.Get(id)
	if entry == nil {
		respondJSON(w, 404, map[string]any{"error": "Not found"})
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "entry": entry})
}

// handleAPILogsClear wipes all stored log entries.
func (ws *WebHTTPServer) handleAPILogsClear(w http.ResponseWriter, r *http.Request) {
	if !ws.checkToken(r) {
		respondJSON(w, 403, map[string]any{"error": "Forbidden"})
		return
	}
	mcpLog.Clear()
	respondJSON(w, 200, map[string]any{"ok": true})
}
