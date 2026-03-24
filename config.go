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
	ResultPreviewChars int    `json:"result_preview_chars,omitempty"`
	AutoSendToAgent    bool   `json:"auto_send_to_agent,omitempty"`
	SkipNumericValues  bool   `json:"skip_numeric_values,omitempty"`
	AllowPlainFields   string `json:"allow_plain_fields,omitempty"`
	ForceMaskFields    string `json:"force_mask_fields,omitempty"`
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
				"execute_code":           "execute_code",
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
			"execute_code":           "execute_code",
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

// ─── NER Rules for execute_code masking ─────────────────────────

// NerContextPattern describes a keyword-based masking rule.
// When "keyword" appears before a value in text, that value is masked with alias_prefix.
type NerContextPattern struct {
	Keyword     string `json:"keyword"`      // e.g. "Контрагент", "Менеджер"
	Type        string `json:"type"`         // person, org, inn, phone, email, address, custom
	AliasPrefix string `json:"alias_prefix"` // prefix for pseudonyms
}

// NerCustomRegex is a user-defined regex pattern for masking.
type NerCustomRegex struct {
	Pattern     string `json:"pattern"`      // Go regex
	AliasPrefix string `json:"alias_prefix"` // prefix for pseudonyms
}

// NerRules holds all NER masking rules loaded from ner_rules.json.
type NerRules struct {
	Description        string              `json:"description,omitempty"`
	ContextPatterns    []NerContextPattern `json:"context_patterns"`
	AlwaysMaskKeywords []string            `json:"always_mask_keywords"`
	CustomRegex        []NerCustomRegex    `json:"custom_regex,omitempty"`
}

// LoadNerRules reads NER rules from a JSON file. Returns nil if file doesn't exist.
func LoadNerRules(path string) (*NerRules, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rules NerRules
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	return &rules, nil
}

// ExportNerTemplate writes a template ner_rules.json with examples.
func ExportNerTemplate(path string) error {
	tmpl := &NerRules{
		Description: "NER rules for Trusted Gateway execute_code masking. " +
			"Edit this file and reload in the app. " +
			"Context patterns: when 'keyword' appears before a value, mask it with alias_prefix. " +
			"Types: person, org, inn, phone, email, address, custom.",
		ContextPatterns: []NerContextPattern{
			{Keyword: "Контрагент", Type: "org", AliasPrefix: "Контрагент"},
			{Keyword: "Менеджер", Type: "person", AliasPrefix: "Менеджер"},
			{Keyword: "Ответственный", Type: "person", AliasPrefix: "Сотрудник"},
			{Keyword: "Поставщик", Type: "org", AliasPrefix: "Поставщик"},
			{Keyword: "Адрес", Type: "address", AliasPrefix: "Адрес"},
		},
		AlwaysMaskKeywords: []string{"Наименование", "НаименованиеПолное", "ФИО", "Фамилия"},
		CustomRegex: []NerCustomRegex{
			{Pattern: `договор\s+№?\s*[А-Яа-яA-Za-z0-9/\-]+`, AliasPrefix: "Договор"},
		},
	}
	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// NerRulesPath returns the default path for ner_rules.json next to the executable.
func NerRulesPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "ner_rules.json"
	}
	return filepath.Join(filepath.Dir(exe), "ner_rules.json")
}
