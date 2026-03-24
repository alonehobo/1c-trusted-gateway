package main

import (
	"encoding/json"
	"io"
	"net/http"
)

// ── MCP Streamable HTTP Server ──────────────────────────────────────
// Implements MCP protocol (JSON-RPC 2.0) over streamable HTTP transport.
// Single endpoint: POST /mcp — receives JSON-RPC request, returns JSON-RPC response.

type McpServer struct {
	app *TrustedWebApp
}

func NewMcpServer(app *TrustedWebApp) *McpServer {
	return &McpServer{app: app}
}

// handleMcp handles POST /mcp — streamable HTTP MCP endpoint.
func (ms *McpServer) handleMcp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// SSE fallback: some clients probe with GET first
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(200)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hold connection open until client disconnects
		<-r.Context().Done()
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		http.Error(w, "Read error", 400)
		return
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	// Notifications (no "id" field) — accept silently
	if _, hasID := req["id"]; !hasID {
		w.WriteHeader(202)
		return
	}

	response := ms.dispatch(req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (ms *McpServer) dispatch(req map[string]any) map[string]any {
	id := req["id"]
	method, _ := req["method"].(string)
	params, _ := req["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}

	switch method {
	case "initialize":
		return ms.rpcResult(id, map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "trusted-gateway",
				"version": "1.0.0",
			},
		})

	case "tools/list":
		return ms.rpcResult(id, map[string]any{
			"tools": ms.toolDefinitions(),
		})

	case "tools/call":
		toolName, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		return ms.callTool(id, toolName, args)

	default:
		return ms.rpcError(id, -32601, "Method not found: "+method)
	}
}

func (ms *McpServer) toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name": "gateway_status",
			"description": "Возвращает текущий статус Trusted Gateway: подключение к 1С, наличие сессии, готовность к запросам. " +
				"Если has_session=true и mode='awaiting_approval', пользователь проверяет данные в ручном режиме — " +
				"дождись одобрения и забери результат через gateway_pull_note.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name": "gateway_query",
			"description": "Выполняет запрос к базе 1С через Trusted Gateway. Данные автоматически маскируются: " +
				"имена, названия, текст заменяются псевдонимами (Контрагент_a1b2c3 и т.п.), " +
				"числовые значения (суммы, количества, цены) передаются открыто.\n\n" +
				"ПРАВИЛА ЗАПРОСОВ:\n" +
				"- Используй литеральные даты: ДАТАВРЕМЯ(2025,1,1), НЕ параметры &НачалоПериода.\n" +
				"- НЕ используй ВЫБРАТЬ * — бери только нужные для анализа поля. Минимум полей = меньше рисков и быстрее.\n" +
				"- НЕ пытайся обойти маскировку или получить реальные данные — это нарушает политику безопасности. Работай только с псевдонимами.\n\n" +
				"БЕЛЫЙ СПИСОК ПОЛЕЙ:\n" +
				"Если для анализа критически важно видеть незашифрованные значения полей (например, Статус, ВидДвижения, " +
				"Проведен и другие перечисления/булевы), а они замаскированы — попроси пользователя добавить эти поля " +
				"в белый список через интерфейс шлюза. Не пытайся угадывать значения по псевдонимам.\n\n" +
				"РУЧНОЙ РЕЖИМ:\n" +
				"Если ответ содержит status='awaiting_approval', пользователь проверяет данные перед отправкой. " +
				"Жди одобрения и забирай данные через gateway_pull_note.\n\n" +
				"Возвращает замаскированный bundle (JSON) с результатами запроса.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "Краткое описание задачи/цели запроса",
					},
					"query_text": map[string]any{
						"type": "string",
						"description": "Текст запроса на языке запросов 1С. " +
							"Используй ДАТАВРЕМЯ(год,месяц,день) для дат. Указывай только нужные поля, не ВЫБРАТЬ *.",
					},
				},
				"required": []string{"task", "query_text"},
			},
		},
		{
			"name": "gateway_apply_analysis",
			"description": "Отправляет текст анализа обратно в шлюз для расшифровки псевдонимов. " +
				"Шлюз заменит маскированные идентификаторы (Менеджер_abc123 и т.п.) на реальные значения " +
				"и покажет результат пользователю в UI.\n\n" +
				"ФОРМАТ: Оформляй анализ в Markdown — используй заголовки (## / ###), таблицы, списки, " +
				"**жирный** текст для акцентов, `код` для имён полей. UI поддерживает полный рендеринг Markdown.\n\n" +
				"ВАЖНО: Используй в тексте анализа именно те псевдонимы, которые вернул gateway_query. " +
				"НЕ пытайся угадать или подставить реальные значения — ты их не знаешь, " +
				"шлюз расшифрует псевдонимы автоматически.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"analysis_text": map[string]any{
						"type":        "string",
						"description": "Текст анализа в формате Markdown с замаскированными псевдонимами из результата gateway_query. Используй заголовки, таблицы, списки и форматирование.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "ID сессии из результата gateway_query (опционально)",
					},
				},
				"required": []string{"analysis_text"},
			},
		},
		{
			"name": "gateway_pull_note",
			"description": "Забирает одобренные пользователем данные в ручном режиме. " +
				"Вызывай после того, как gateway_query вернул status='awaiting_approval' и пользователь нажал 'Отправить агенту' в UI. " +
				"Если данные ещё не одобрены, вернёт пустой результат — подожди и попробуй позже.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name": "gateway_execute_code",
			"description": "Выполняет BSL-код в 1С через Trusted Gateway (только чтение — транзакция всегда откатывается). " +
				"Результат автоматически маскируется NER-правилами шлюза: ФИО, ИНН, названия организаций, телефоны, email " +
				"заменяются псевдонимами. Числовые значения проходят открыто.\n\n" +
				"Результат возвращай через переменную Результат (строка) или РезультатJSON (JSON-строка).\n\n" +
				"ФОРМАТ: Оформляй анализ результата в Markdown — заголовки, таблицы, списки. " +
				"Используй псевдонимы из результата в gateway_apply_analysis.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "Краткое описание задачи/цели выполнения кода",
					},
					"code": map[string]any{
						"type": "string",
						"description": "BSL-код для выполнения. Результат верни через Результат или РезультатJSON.",
					},
				},
				"required": []string{"task", "code"},
			},
		},
		{
			"name": "gateway_clear_session",
			"description": "Очищает текущую сессию шлюза: удаляет результаты запросов, маскировку и анализ из памяти.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (ms *McpServer) callTool(id any, toolName string, args map[string]any) map[string]any {
	switch toolName {
	case "gateway_status":
		return ms.toolStatus(id)
	case "gateway_query":
		return ms.toolQuery(id, args)
	case "gateway_apply_analysis":
		return ms.toolApplyAnalysis(id, args)
	case "gateway_execute_code":
		return ms.toolExecuteCode(id, args)
	case "gateway_pull_note":
		return ms.toolPullNote(id)
	case "gateway_clear_session":
		return ms.toolClearSession(id)
	default:
		return ms.rpcError(id, -32602, "Unknown tool: "+toolName)
	}
}

func (ms *McpServer) toolStatus(id any) map[string]any {
	ms.app.mu.RLock()
	status := map[string]any{
		"connected_url":    ms.app.ConnectedURL,
		"ready":            ms.app.ConnectionVerified,
		"has_session":      ms.app.CurrentSession != nil,
		"has_pending_note": ms.app.PendingAgentNote != nil,
	}
	if ms.app.CurrentSession != nil {
		status["session_id"] = ms.app.CurrentSession.SessionID
		status["row_count"] = len(ms.app.CurrentSession.MaskedRows)
	}
	ms.app.mu.RUnlock()

	text, _ := json.MarshalIndent(status, "", "  ")
	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(text)},
		},
	})
}

func (ms *McpServer) toolQuery(id any, args map[string]any) map[string]any {
	task, _ := args["task"].(string)
	queryText, _ := args["query_text"].(string)
	if queryText == "" {
		return ms.toolError(id, "query_text is required")
	}
	if task == "" {
		task = "MCP query"
	}

	// Always masked mode for MCP queries
	result := ms.app.bridgeRunQuery(task, queryText, "masked", true)

	okVal, _ := result["ok"].(bool)
	if !okVal {
		errMsg, _ := result["message"].(string)
		if errMsg == "" {
			errMsg, _ = result["error"].(string)
		}
		if errMsg == "" {
			errMsg = "Query failed"
		}
		return ms.toolError(id, errMsg)
	}

	bundle, _ := result["masked_bundle"].(string)
	if bundle == "" {
		data, _ := json.MarshalIndent(result, "", "  ")
		bundle = string(data)
	}

	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": bundle},
		},
	})
}

func (ms *McpServer) toolApplyAnalysis(id any, args map[string]any) map[string]any {
	analysisText, _ := args["analysis_text"].(string)
	if analysisText == "" {
		return ms.toolError(id, "analysis_text is required")
	}

	var sessionID *string
	if sid, ok := args["session_id"].(string); ok && sid != "" {
		sessionID = &sid
	}

	result := ms.app.bridgeApplyAnalysis(sessionID, analysisText)

	okVal, _ := result["ok"].(bool)
	if !okVal {
		errMsg, _ := result["error"].(string)
		if errMsg == "" {
			errMsg = "Apply analysis failed"
		}
		return ms.toolError(id, errMsg)
	}

	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "Анализ отправлен в шлюз и расшифрован. Пользователь видит результат в UI."},
		},
	})
}

func (ms *McpServer) toolExecuteCode(id any, args map[string]any) map[string]any {
	task, _ := args["task"].(string)
	code, _ := args["code"].(string)
	if code == "" {
		return ms.toolError(id, "code is required")
	}
	if task == "" {
		task = "MCP execute_code"
	}

	result := ms.app.bridgeExecuteCode(task, code)

	okVal, _ := result["ok"].(bool)
	if !okVal {
		errMsg, _ := result["message"].(string)
		if errMsg == "" {
			errMsg, _ = result["error"].(string)
		}
		if errMsg == "" {
			errMsg = "Execute code failed"
		}
		return ms.toolError(id, errMsg)
	}

	maskedResult, _ := result["masked_result"].(string)
	if maskedResult == "" {
		maskedResult = "(пустой результат)"
	}

	// Build response with metadata
	payload := map[string]any{
		"session_id":      result["session_id"],
		"task":            task,
		"mode":            "code_masked",
		"masked_entities": result["masked_entities"],
		"result":          maskedResult,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")

	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(data)},
		},
	})
}

func (ms *McpServer) toolPullNote(id any) map[string]any {
	result := ms.app.bridgePullNote(true)

	okVal, _ := result["ok"].(bool)
	if !okVal {
		errMsg, _ := result["error"].(string)
		if errMsg == "" {
			errMsg = "No approved data available"
		}
		return ms.toolError(id, errMsg)
	}

	text, _ := json.MarshalIndent(result, "", "  ")
	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(text)},
		},
	})
}

func (ms *McpServer) toolClearSession(id any) map[string]any {
	result := ms.app.bridgeClearSession()
	text, _ := json.Marshal(result)
	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(text)},
		},
	})
}

// ── JSON-RPC helpers ────────────────────────────────────────────────

func (ms *McpServer) rpcResult(id any, result map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

func (ms *McpServer) rpcError(id any, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func (ms *McpServer) toolError(id any, message string) map[string]any {
	return ms.rpcResult(id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": message},
		},
		"isError": true,
	})
}
