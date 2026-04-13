package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ─── NER Rules Configuration ──────────────────────────────────

// NerContextPattern describes a keyword-based masking rule.
type NerContextPattern struct {
	Keyword     string `json:"keyword"`
	Type        string `json:"type"`
	AliasPrefix string `json:"alias_prefix"`
}

// NerCustomRegex is a user-defined regex pattern for masking.
type NerCustomRegex struct {
	Pattern     string `json:"pattern"`
	AliasPrefix string `json:"alias_prefix"`
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

// ─── NER-based free text masking ──────────────────────────────

// nerMatch represents a single detected entity in text.
type nerMatch struct {
	start       int
	end         int
	original    string
	aliasPrefix string
}

// Built-in NER regex patterns (always active).
var (
	reSnils     = regexp.MustCompile(`\b\d{3}-\d{3}-\d{3}\s?\d{2}\b`)
	rePhone     = regexp.MustCompile(`(?:\+7|8)[\s-]?\(?\d{3}\)?[\s-]?\d{3}[\s-]?\d{2}[\s-]?\d{2}`)
	reEmail     = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	reBik       = regexp.MustCompile(`\b04\d{7}\b`)
	reOgrn      = regexp.MustCompile(`\b\d{13}\b`)
	reOgrnip    = regexp.MustCompile(`\b\d{15}\b`)
	reInnCtx    = regexp.MustCompile(`(?i)ИНН[\s:=]*(\d{10,12})\b`)
	reKppCtx    = regexp.MustCompile(`(?i)КПП[\s:=]*(\d{9})\b`)
	reOrgQuoted = regexp.MustCompile(`(?:ООО|ОАО|ЗАО|ПАО|АО|ИП|НКО|ФГУП|МУП|ГУП)\s*[""«]([^""»]+)[""»]`)
	reOrgPlain  = regexp.MustCompile(`(?:ООО|ОАО|ЗАО|ПАО|АО|ИП|НКО|ФГУП|МУП|ГУП)\s+([А-ЯЁ][а-яёА-ЯЁ\s\-]{1,40})`)
	reFioShort  = regexp.MustCompile(`[А-ЯЁ][а-яё]+\s+[А-ЯЁ]\.\s?[А-ЯЁ]\.`)
	reFioFull   = regexp.MustCompile(`[А-ЯЁ][а-яё]{1,30}\s+[А-ЯЁ][а-яё]{1,30}\s+[А-ЯЁ][а-яё]*(вич|вна|ич|чна)\b`)
)

// SanitizeFreeText applies NER-based masking to free-form text.
func SanitizeFreeText(text string, rules *NerRules, salt string, aliasLength int) (string, map[string]string) {
	if text == "" {
		return text, nil
	}
	if aliasLength <= 0 {
		aliasLength = 10
	}

	aliasToOriginal := make(map[string]string)
	originalToAlias := make(map[string]string)
	var matches []nerMatch

	makeAlias := func(prefix, value string) string {
		cacheKey := prefix + "::" + value
		if cached, ok := originalToAlias[cacheKey]; ok {
			return cached
		}
		base := fmt.Sprintf("%s:%s:%s", prefix, value, salt)
		hash := sha256.Sum256([]byte(base))
		digest := fmt.Sprintf("%x", hash[:])
		if len(digest) > aliasLength {
			digest = digest[:aliasLength]
		}
		alias := prefix + "_" + digest
		originalToAlias[cacheKey] = alias
		aliasToOriginal[alias] = value
		return alias
	}

	// --- Built-in patterns ---

	for _, loc := range reSnils.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "СНИЛС"})
	}
	for _, loc := range rePhone.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Телефон"})
	}
	for _, loc := range reEmail.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Email"})
	}
	for _, loc := range reBik.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "БИК"})
	}
	for _, m := range reInnCtx.FindAllStringSubmatchIndex(text, -1) {
		if m[2] >= 0 && m[3] >= 0 {
			matches = append(matches, nerMatch{m[2], m[3], text[m[2]:m[3]], "ИНН"})
		}
	}
	for _, m := range reKppCtx.FindAllStringSubmatchIndex(text, -1) {
		if m[2] >= 0 && m[3] >= 0 {
			matches = append(matches, nerMatch{m[2], m[3], text[m[2]:m[3]], "КПП"})
		}
	}
	for _, loc := range reOgrnip.FindAllStringIndex(text, -1) {
		ctx := safeSubstr(text, loc[0]-20, loc[0])
		if strings.Contains(strings.ToLower(ctx), "огрн") {
			matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "ОГРНИП"})
		}
	}
	for _, loc := range reOgrn.FindAllStringIndex(text, -1) {
		ctx := safeSubstr(text, loc[0]-20, loc[0])
		if strings.Contains(strings.ToLower(ctx), "огрн") {
			matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "ОГРН"})
		}
	}
	for _, loc := range reOrgQuoted.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Организация"})
	}
	for _, loc := range reOrgPlain.FindAllStringIndex(text, -1) {
		orig := strings.TrimSpace(text[loc[0]:loc[1]])
		matches = append(matches, nerMatch{loc[0], loc[0] + len(orig), orig, "Организация"})
	}
	for _, loc := range reFioShort.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "ФИО"})
	}
	for _, loc := range reFioFull.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "ФИО"})
	}

	// --- Context patterns from NER rules ---
	if rules != nil {
		for _, cp := range rules.ContextPatterns {
			prefix := cp.AliasPrefix
			if prefix == "" {
				prefix = cp.Keyword
			}
			ctxMatches := findContextValues(text, cp.Keyword)
			for _, cm := range ctxMatches {
				matches = append(matches, nerMatch{cm.start, cm.end, cm.original, prefix})
			}
		}

		for _, cr := range rules.CustomRegex {
			re, err := regexp.Compile(cr.Pattern)
			if err != nil {
				continue
			}
			for _, loc := range re.FindAllStringIndex(text, -1) {
				matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], cr.AliasPrefix})
			}
		}
	}

	// --- Resolve overlaps: longer matches win ---
	matches = resolveOverlaps(matches)

	// --- Replace from end to start ---
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].start > matches[j].start
	})

	sensitiveNerPrefixes := map[string]bool{
		"ИНН": true, "КПП": true, "СНИЛС": true, "БИК": true,
		"ОГРН": true, "ОГРНИП": true, "РасчСчет": true, "КоррСчет": true,
	}

	result := text
	for _, m := range matches {
		value := strings.TrimSpace(m.original)
		if value == "" {
			continue
		}
		if looksNumeric(value) && !sensitiveNerPrefixes[m.aliasPrefix] {
			continue
		}
		alias := makeAlias(m.aliasPrefix, value)
		result = result[:m.start] + alias + result[m.end:]
	}

	return result, aliasToOriginal
}

// findContextValues finds values after a keyword in various formats (JSON, colon, equals).
func findContextValues(text, keyword string) []nerMatch {
	var results []nerMatch

	reJSON, err := regexp.Compile(fmt.Sprintf(`(?i)"%s"\s*:\s*"([^"]+)"`, regexp.QuoteMeta(keyword)))
	if err == nil {
		for _, m := range reJSON.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[3] >= 0 {
				val := strings.TrimSpace(text[m[2]:m[3]])
				if val != "" && !looksNumeric(val) {
					results = append(results, nerMatch{m[2], m[3], val, ""})
				}
			}
		}
	}

	reCol, err := regexp.Compile(fmt.Sprintf(`(?i)%s\s*:\s*([^\n,\t;]{2,60})`, regexp.QuoteMeta(keyword)))
	if err == nil {
		for _, m := range reCol.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[3] >= 0 {
				val := strings.TrimSpace(text[m[2]:m[3]])
				if val != "" && !strings.HasPrefix(val, "\"") && !looksNumeric(val) {
					results = append(results, nerMatch{m[2], m[2] + len(val), val, ""})
				}
			}
		}
	}

	reEq, err := regexp.Compile(fmt.Sprintf(`(?i)%s\s*=\s*([^\n,\t;]{2,60})`, regexp.QuoteMeta(keyword)))
	if err == nil {
		for _, m := range reEq.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[3] >= 0 {
				val := strings.TrimSpace(text[m[2]:m[3]])
				if val != "" && !strings.HasPrefix(val, "\"") && !looksNumeric(val) {
					results = append(results, nerMatch{m[2], m[2] + len(val), val, ""})
				}
			}
		}
	}

	return results
}

// resolveOverlaps removes overlapping matches, keeping longer ones.
func resolveOverlaps(matches []nerMatch) []nerMatch {
	if len(matches) <= 1 {
		return matches
	}

	sort.Slice(matches, func(i, j int) bool {
		li := matches[i].end - matches[i].start
		lj := matches[j].end - matches[j].start
		if li != lj {
			return li > lj
		}
		return matches[i].start < matches[j].start
	})

	var result []nerMatch
	for _, m := range matches {
		overlaps := false
		for _, existing := range result {
			if m.start < existing.end && m.end > existing.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			result = append(result, m)
		}
	}

	return result
}

// safeSubstr returns a substring with bounds checking.
func safeSubstr(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}
