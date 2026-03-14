package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

const (
	DefaultBridgeHost = "127.0.0.1"
	DefaultBridgePort = 8766
)

// BridgeCallbacks holds the functions the bridge dispatches to.
type BridgeCallbacks struct {
	Status       func() map[string]any
	RunQuery     func(task, queryText, mode string) map[string]any
	ApplyAnalysis func(sessionID *string, analysisText string) map[string]any
	ClearSession func() map[string]any
	PullNote     func(clearAfterRead bool) map[string]any
}

// TrustedBridgeServer is a TCP server for CLI agent communication.
type TrustedBridgeServer struct {
	Callbacks    BridgeCallbacks
	Host         string
	Port         int
	BridgeSecret string

	listener net.Listener
	mu       sync.Mutex
	done     chan struct{}
}

// NewTrustedBridgeServer creates a new bridge server with a random secret.
func NewTrustedBridgeServer(callbacks BridgeCallbacks, host string, port int) *TrustedBridgeServer {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &TrustedBridgeServer{
		Callbacks:    callbacks,
		Host:         host,
		Port:         port,
		BridgeSecret: base64.URLEncoding.EncodeToString(secret),
		done:         make(chan struct{}),
	}
}

// Start begins listening for TCP connections.
func (b *TrustedBridgeServer) Start() error {
	addr := fmt.Sprintf("%s:%d", b.Host, b.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	b.listener = ln
	go b.acceptLoop()
	return nil
}

// Stop shuts down the bridge server.
func (b *TrustedBridgeServer) Stop() {
	if b.listener != nil {
		close(b.done)
		b.listener.Close()
	}
}

func (b *TrustedBridgeServer) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			select {
			case <-b.done:
				return
			default:
				continue
			}
		}
		go b.handleConnection(conn)
	}
}

func (b *TrustedBridgeServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	raw, err := io.ReadAll(conn)
	if err != nil || len(raw) == 0 {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		resp := map[string]any{"ok": false, "error": err.Error()}
		data, _ := json.Marshal(resp)
		conn.Write(data)
		return
	}

	response := b.dispatch(payload)
	data, _ := json.Marshal(response)
	conn.Write(data)
}

func (b *TrustedBridgeServer) dispatch(payload map[string]any) map[string]any {
	// Authentication
	providedSecret := ""
	if v, ok := payload["secret"]; ok && v != nil {
		providedSecret = fmt.Sprintf("%v", v)
	}
	if providedSecret == "" || providedSecret != b.BridgeSecret {
		return map[string]any{"ok": false, "error": "Bridge authentication failed."}
	}

	command := ""
	if v, ok := payload["command"]; ok && v != nil {
		command = strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", v)))
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch command {
	case "status":
		result := b.Callbacks.Status()
		result["ok"] = true
		return result

	case "run_query":
		task := getStringField(payload, "task")
		queryText := getStringField(payload, "query_text")
		// Bridge always forces masked mode
		mode := "masked"
		return b.Callbacks.RunQuery(task, queryText, mode)

	case "apply_analysis":
		var sessionID *string
		if v, ok := payload["session_id"]; ok && v != nil {
			s := fmt.Sprintf("%v", v)
			sessionID = &s
		}
		analysisText := getStringField(payload, "analysis_text")
		return b.Callbacks.ApplyAnalysis(sessionID, analysisText)

	case "clear_session":
		return b.Callbacks.ClearSession()

	case "pull_note":
		clearAfterRead := true
		if v, ok := payload["clear_after_read"]; ok {
			if bv, ok := v.(bool); ok {
				clearAfterRead = bv
			}
		}
		return b.Callbacks.PullNote(clearAfterRead)

	default:
		return map[string]any{"ok": false, "error": fmt.Sprintf("Unknown bridge command: %s", command)}
	}
}

func getStringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return ""
}

// SendBridgeCommand sends a command to a running bridge server (used by CLI).
func SendBridgeCommand(payload map[string]any, secret, host string, port int, timeoutSeconds float64) (map[string]any, error) {
	enriched := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		enriched[k] = v
	}
	enriched["secret"] = secret

	addr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	data, err := json.Marshal(enriched)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}
	// Signal that we're done writing
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}

	respData, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	if len(respData) == 0 {
		return nil, fmt.Errorf("trusted bridge returned an empty response")
	}

	var result map[string]any
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, err
	}
	return result, nil
}
