package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ToolCallResult holds the result of a single MCP tool call.
type ToolCallResult struct {
	ToolName   string         `json:"tool_name"`
	Arguments  map[string]any `json:"arguments"`
	Text       string         `json:"text"`
	Structured any            `json:"structured"`
	IsError    bool           `json:"is_error"`
}

// Preview returns a truncated preview of the result.
func (r *ToolCallResult) Preview(limit int) string {
	var raw string
	if r.Structured != nil {
		data, err := json.MarshalIndent(r.Structured, "", "  ")
		if err == nil {
			raw = string(data)
		} else {
			raw = r.Text
		}
	} else {
		raw = r.Text
	}
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit] + "\n... [truncated]"
}

// McpClient is an HTTP client for communicating with a 1C MCP server (Streamable HTTP).
type McpClient struct {
	URL            string
	Headers        map[string]string
	TimeoutSeconds float64
	Tools          map[string]string
	httpClient     *http.Client
	sessionID      string
	nextID         int
}

// NewMcpClient creates a new MCP client.
func NewMcpClient(url string, headers map[string]string, timeoutSeconds float64, tools map[string]string) *McpClient {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	if tools == nil {
		tools = map[string]string{
			"query":                  "query",
			"get_metadata_structure": "get_metadata_structure",
		}
	}
	return &McpClient{
		URL:            url,
		Headers:        headers,
		TimeoutSeconds: timeoutSeconds,
		Tools:          tools,
		httpClient: &http.Client{
			// No global Timeout — each request uses context for deadline control.
			// This prevents premature kills on long-running 1C queries.
		},
		nextID: 1,
	}
}

// Initialize sends an initialize request to the MCP server and stores the session ID.
func (c *McpClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "trusted-gateway-go",
			"version": "1.0.0",
		},
	}
	resp, err := c.sendRequest(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("MCP initialize failed: %w", err)
	}
	_ = resp // We just need the session ID from headers, already captured

	// Send initialized notification
	return c.sendNotification("notifications/initialized", nil)
}

// ListTools returns the list of available tool names.
func (c *McpClient) ListTools(ctx context.Context) ([]string, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected tools/list response format")
	}
	toolsRaw, ok := result["tools"].([]any)
	if !ok {
		return nil, fmt.Errorf("tools field missing or not array")
	}
	names := make([]string, 0, len(toolsRaw))
	for _, t := range toolsRaw {
		if tm, ok := t.(map[string]any); ok {
			if name, ok := tm["name"].(string); ok {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// CallTool invokes an MCP tool by name and returns the result.
func (c *McpClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (*ToolCallResult, error) {
	params := map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}
	resp, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		// Check for error
		if errObj, ok := resp["error"].(map[string]any); ok {
			return &ToolCallResult{
				ToolName:  toolName,
				Arguments: arguments,
				Text:      fmt.Sprintf("MCP error: %v", errObj["message"]),
				IsError:   true,
			}, nil
		}
		return nil, fmt.Errorf("unexpected tools/call response format")
	}

	isError := false
	if ie, ok := result["isError"].(bool); ok {
		isError = ie
	}

	// Extract text content
	var textParts []string
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			if itemMap, ok := item.(map[string]any); ok {
				if itemType, _ := itemMap["type"].(string); itemType == "text" {
					if text, ok := itemMap["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
	}
	text := strings.TrimSpace(strings.Join(textParts, "\n"))

	// Extract structured content
	structured := result["structuredContent"]

	return &ToolCallResult{
		ToolName:   toolName,
		Arguments:  arguments,
		Text:       text,
		Structured: structured,
		IsError:    isError,
	}, nil
}

// CallNamedTool resolves a logical tool name and calls it.
func (c *McpClient) CallNamedTool(ctx context.Context, logicalName string, arguments map[string]any) (*ToolCallResult, error) {
	toolName := logicalName
	if mapped, ok := c.Tools[logicalName]; ok {
		toolName = mapped
	}
	return c.CallTool(ctx, toolName, arguments)
}

func (c *McpClient) sendRequest(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	id := c.nextID
	c.nextID++

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.URL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Capture session ID from response headers
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	const maxResponseBody = 10 * 1024 * 1024 // 10 MB

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return nil, fmt.Errorf("MCP server returned status %d: %s", resp.StatusCode, string(body))
	}

	contentType := resp.Header.Get("Content-Type")

	// Handle SSE response
	if strings.Contains(contentType, "text/event-stream") {
		return c.readSSEResponse(resp.Body)
	}

	// Handle JSON response
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse MCP response: %w", err)
	}
	return result, nil
}

func (c *McpClient) readSSEResponse(reader io.Reader) (map[string]any, error) {
	const maxSSEBody = 10 * 1024 * 1024 // 10 MB
	body, err := io.ReadAll(io.LimitReader(reader, maxSSEBody))
	if err != nil {
		return nil, err
	}

	// Parse SSE events — find the last "data:" line that contains our JSON-RPC response
	lines := strings.Split(string(body), "\n")
	var lastData string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			lastData = strings.TrimPrefix(line, "data: ")
		} else if strings.HasPrefix(line, "data:") {
			lastData = strings.TrimPrefix(line, "data:")
		}
	}

	if lastData == "" {
		return nil, fmt.Errorf("no data events in SSE response")
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(lastData), &result); err != nil {
		return nil, fmt.Errorf("failed to parse SSE data: %w", err)
	}
	return result, nil
}

func (c *McpClient) sendNotification(method string, params map[string]any) error {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		reqBody["params"] = params
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.URL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Capture session ID
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	return nil
}
