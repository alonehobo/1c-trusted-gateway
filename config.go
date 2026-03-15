package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// McpConfig holds MCP server connection parameters.
type McpConfig struct {
	URL                   string            `json:"url,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	TimeoutSeconds        float64           `json:"timeout_seconds,omitempty"`
	SSEReadTimeoutSeconds float64           `json:"sse_read_timeout_seconds,omitempty"`
	Tools                 map[string]string `json:"tools,omitempty"`
}

// PrivacyConfig holds privacy/masking parameters.
type PrivacyConfig struct {
	Salt                   string `json:"salt,omitempty"`
	SaltEnv                string `json:"salt_env,omitempty"`
	AliasLength            int    `json:"alias_length,omitempty"`
	NumericThreshold       int    `json:"numeric_threshold,omitempty"` // kept for config compat, unused
	ShowMaskedDataInViewer bool   `json:"show_masked_data_in_viewer,omitempty"`
}

// DefaultsConfig holds default runtime parameters.
type DefaultsConfig struct {
	ResultPreviewChars         int    `json:"result_preview_chars,omitempty"`
	ExecuteWithoutConfirmation bool   `json:"execute_without_confirmation,omitempty"`
	ForceMaskFields            string `json:"force_mask_fields,omitempty"`
	AllowPlainFields           string `json:"allow_plain_fields,omitempty"`
}

// AppConfig is the top-level application configuration.
type AppConfig struct {
	Mcp      McpConfig      `json:"mcp"`
	Privacy  PrivacyConfig  `json:"privacy"`
	Defaults DefaultsConfig `json:"defaults"`
	WebPort  int            `json:"web_port,omitempty"`
}

// DefaultAppConfig returns an AppConfig with sensible defaults.
func DefaultAppConfig() *AppConfig {
	return &AppConfig{
		Mcp: McpConfig{
			TimeoutSeconds:        30.0,
			SSEReadTimeoutSeconds: 300.0,
			Headers:               make(map[string]string),
			Tools: map[string]string{
				"query":                  "query",
				"get_metadata_structure": "get_metadata_structure",
			},
		},
		Privacy: PrivacyConfig{
			SaltEnv:     "ONEC_GATEWAY_SALT",
			AliasLength: 10,
		},
		Defaults: DefaultsConfig{
			ResultPreviewChars: 4000,
		},
		WebPort: 8767,
	}
}

// LoadConfig reads an AppConfig from a JSON file at the given path.
func LoadConfig(path string) (*AppConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	cfg := DefaultAppConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	applyConfigDefaults(cfg)
	return cfg, nil
}

// ConfigFromDict creates an AppConfig from a raw map (e.g. from encrypted settings).
func ConfigFromDict(data map[string]any) *AppConfig {
	cfg := DefaultAppConfig()
	raw, err := json.Marshal(data)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, cfg)
	applyConfigDefaults(cfg)
	return cfg
}

func applyConfigDefaults(cfg *AppConfig) {
	if cfg.Mcp.TimeoutSeconds <= 0 {
		cfg.Mcp.TimeoutSeconds = 30.0
	}
	if cfg.Mcp.SSEReadTimeoutSeconds <= 0 {
		cfg.Mcp.SSEReadTimeoutSeconds = 300.0
	}
	if cfg.Mcp.Headers == nil {
		cfg.Mcp.Headers = make(map[string]string)
	}
	if cfg.Mcp.Tools == nil {
		cfg.Mcp.Tools = map[string]string{
			"query":                  "query",
			"get_metadata_structure": "get_metadata_structure",
		}
	}
	if cfg.Privacy.SaltEnv == "" && cfg.Privacy.Salt == "" {
		cfg.Privacy.SaltEnv = "ONEC_GATEWAY_SALT"
	}
	if cfg.Privacy.AliasLength <= 0 {
		cfg.Privacy.AliasLength = 10
	}
	if cfg.Privacy.NumericThreshold <= 0 {
		cfg.Privacy.NumericThreshold = 10
	}
	if cfg.Defaults.ResultPreviewChars <= 0 {
		cfg.Defaults.ResultPreviewChars = 4000
	}
	if cfg.WebPort <= 0 {
		cfg.WebPort = 8767
	}
}

// ResolvedSalt returns the effective salt value (from config or environment).
func (p *PrivacyConfig) ResolvedSalt() string {
	if p.Salt != "" {
		return p.Salt
	}
	if p.SaltEnv != "" {
		if v := os.Getenv(p.SaltEnv); v != "" {
			return v
		}
	}
	return ""
}
