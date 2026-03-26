package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	numericPattern = regexp.MustCompile(`^[+-]?\d+(?:\.\d+)?$`)
	wordPattern    = regexp.MustCompile(`[A-Za-zА-Яа-яЁё0-9]+`)

	// sensitiveFieldKeywords — fields that are always masked even when skipNumeric is on.
	// Only explicit whitelist can override this.
	sensitiveFieldKeywords = []string{"инн", "кпп", "снилс", "огрн", "огрнип", "бик", "серия", "серии", "серий", "inn", "kpp", "snils", "serial"}

	// DefaultAllowPlainKeywords is empty — all fields are masked by default.
	// Users add fields to the white-list explicitly via UI or settings.
	DefaultAllowPlainKeywords = []string{}
)

// SanitizedResult holds the result of data sanitization.
type SanitizedResult struct {
	MaskedRows      []map[string]any `json:"masked_rows"`
	DisplayRows     []map[string]any `json:"display_rows"`
	AliasToOriginal map[string]string `json:"alias_to_original"`
	MaskedColumns   []string          `json:"masked_columns"`
	UnmaskedColumns []string          `json:"unmasked_columns"`
}

// DataSanitizer masks sensitive data in query results.
// Policy: mask ALL fields by default; only allow-listed fields pass through.
type DataSanitizer struct {
	salt             string
	aliasLength      int
	allowKeywords    []string // substrings to match in field names for allow-list
	skipNumeric      bool     // when true, values that look like real numbers (prices, amounts) pass through unmasked
}

// NewDataSanitizer creates a new sanitizer.
func NewDataSanitizer(salt string, aliasLength int, _ int) *DataSanitizer {
	if aliasLength <= 0 {
		aliasLength = 10
	}
	return &DataSanitizer{
		salt:          salt,
		aliasLength:   aliasLength,
		allowKeywords: DefaultAllowPlainKeywords,
	}
}

// SanitizeRows masks values in the given rows.
// Policy: mask everything by default. Only allow-listed fields and explicit allowPlainFields pass through.
func (ds *DataSanitizer) SanitizeRows(
	rows []map[string]any,
	forceMaskFields map[string]bool,
	allowPlainFields map[string]bool,
) *SanitizedResult {
	aliasToOriginal := make(map[string]string)
	originalToAlias := make(map[string]string)
	maskedColumnsSet := make(map[string]bool)
	unmaskedColumnsSet := make(map[string]bool)
	maskedRows := make([]map[string]any, 0, len(rows))

	forceMask := normalizeFieldSet(forceMaskFields)
	allowPlain := normalizeFieldSet(allowPlainFields)

	for _, row := range rows {
		maskedRow := make(map[string]any, len(row))
		for fieldName, value := range row {
			maskedValue := ds.maskValue(fieldName, value, forceMask, allowPlain, originalToAlias, aliasToOriginal)
			maskedRow[fieldName] = maskedValue
			if !valueEqual(value, maskedValue) {
				maskedColumnsSet[fieldName] = true
			} else {
				unmaskedColumnsSet[fieldName] = true
			}
		}
		maskedRows = append(maskedRows, maskedRow)
	}

	maskedColumns := sortedKeys(maskedColumnsSet)
	unmaskedColumns := sortedKeys(unmaskedColumnsSet)

	return &SanitizedResult{
		MaskedRows:      maskedRows,
		DisplayRows:     rows,
		AliasToOriginal: aliasToOriginal,
		MaskedColumns:   maskedColumns,
		UnmaskedColumns: unmaskedColumns,
	}
}

// maskValue recursively masks a value. Returns masked version.
func (ds *DataSanitizer) maskValue(
	fieldName string,
	value any,
	forceMask map[string]bool,
	allowPlain map[string]bool,
	originalToAlias map[string]string,
	aliasToOriginal map[string]string,
) any {
	if value == nil {
		return nil
	}

	// Recursively process nested maps
	if m, ok := value.(map[string]any); ok {
		masked := make(map[string]any, len(m))
		for k, v := range m {
			masked[k] = ds.maskValue(k, v, forceMask, allowPlain, originalToAlias, aliasToOriginal)
		}
		return masked
	}

	// Recursively process nested slices
	if arr, ok := value.([]any); ok {
		masked := make([]any, len(arr))
		for i, v := range arr {
			masked[i] = ds.maskValue(fieldName, v, forceMask, allowPlain, originalToAlias, aliasToOriginal)
		}
		return masked
	}

	// Booleans never masked
	if _, ok := value.(bool); ok {
		return value
	}

	// Check if field is explicitly allow-listed
	normalizedName := normalizeFieldName(fieldName)

	// Force-mask always wins
	if forceMask[normalizedName] {
		return ds.aliasFor(fieldName, value, originalToAlias, aliasToOriginal)
	}

	// Explicit allow-plain from user
	if allowPlain[normalizedName] {
		return value
	}

	// Check built-in allow keywords (Количество, Цена, Сумма, НДС)
	if ds.matchesAllowKeyword(normalizedName) {
		return value
	}

	// Skip numeric values if the option is enabled,
	// but never for sensitive fields (ИНН, КПП, etc.) — those require explicit whitelist
	if ds.skipNumeric && !isSensitiveField(normalizedName) && isRealNumber(value) {
		return value
	}

	// Boolean-like string values pass through when skipNumeric is on
	if ds.skipNumeric && isBooleanValue(value) {
		return value
	}

	// Empty strings pass through
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" {
		return value
	}

	// Mask everything else (strings, numbers, etc.)
	return ds.aliasFor(fieldName, value, originalToAlias, aliasToOriginal)
}

// matchesAllowKeyword checks if field name contains any allow-list keyword.
func (ds *DataSanitizer) matchesAllowKeyword(normalizedFieldName string) bool {
	for _, kw := range ds.allowKeywords {
		if strings.Contains(normalizedFieldName, kw) {
			return true
		}
	}
	return false
}

// RehydrateText replaces aliases in text with their original values.
// Uses strings.Replacer for efficient single-pass replacement.
func RehydrateText(text string, aliasToOriginal map[string]string) string {
	if text == "" || len(aliasToOriginal) == 0 {
		return text
	}

	// Collect aliases that appear in text, sorted by length descending
	// so longer aliases are replaced first (prevents partial matches).
	type pair struct {
		alias    string
		original string
	}
	var pairs []pair
	for alias, original := range aliasToOriginal {
		if strings.Contains(text, alias) {
			pairs = append(pairs, pair{alias, original})
		}
	}
	if len(pairs) == 0 {
		return text
	}

	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].alias) > len(pairs[j].alias)
	})

	// Build a Replacer for single-pass replacement
	args := make([]string, 0, len(pairs)*2)
	for _, p := range pairs {
		args = append(args, p.alias, p.original)
	}
	return strings.NewReplacer(args...).Replace(text)
}

func (ds *DataSanitizer) aliasFor(
	fieldName string,
	value any,
	originalToAlias map[string]string,
	aliasToOriginal map[string]string,
) string {
	text := toText(value)
	cacheKey := fieldName + "::" + text

	if cached, ok := originalToAlias[cacheKey]; ok {
		return cached
	}

	prefix := prefixFor(fieldName)
	base := fmt.Sprintf("%s:%s:%s", fieldName, text, ds.salt)
	hash := sha256.Sum256([]byte(base))
	digest := fmt.Sprintf("%x", hash[:])
	if len(digest) > ds.aliasLength {
		digest = digest[:ds.aliasLength]
	}
	alias := prefix + "_" + digest

	originalToAlias[cacheKey] = alias
	aliasToOriginal[alias] = text
	return alias
}

func toText(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case map[string]any, []any:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func prefixFor(fieldName string) string {
	words := wordPattern.FindAllString(fieldName, -1)
	if len(words) == 0 {
		return "Поле"
	}
	token := words[0]
	runes := []rune(token)
	if len(runes) > 12 {
		runes = runes[:12]
	}
	if len(runes) == 0 {
		return "Поле"
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// isBooleanValue checks if the value is a boolean-like string: Да/Нет, Истина/Ложь, True/False.
func isBooleanValue(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "да", "нет", "истина", "ложь", "true", "false", "yes", "no":
		return true
	}
	return false
}

// isSensitiveField checks if the field name contains ИНН, КПП or similar identifiers
// that must always be masked even when skipNumeric is on.
func isSensitiveField(normalizedFieldName string) bool {
	for _, kw := range sensitiveFieldKeywords {
		if strings.Contains(normalizedFieldName, kw) {
			return true
		}
	}
	return false
}

// maxNumericStringLen is the maximum length of a digit string to consider it a real number.
// Longer strings are treated as codes/identifiers and get masked.
const maxNumericStringLen = 15

// isRealNumber checks whether a value looks like a real number (price, amount, quantity)
// as opposed to a phone number, document number, or other digit-based identifier.
// Rules:
//   - Go numeric types (float64, int, etc.) are always real numbers
//   - String values: strip thousand separators (spaces, nbsp), allow one "." or "," as decimal
//   - Reject if there are any characters besides digits, one separator, leading +/-, spaces
//   - Reject leading zeros like "007", "00123" (but allow "0", "0.5", "0,12")
//   - Reject digit strings longer than 15 characters (likely codes/identifiers)
func isRealNumber(value any) bool {
	switch value.(type) {
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	case json.Number:
		return true
	}

	text, ok := value.(string)
	if !ok {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	// Strip thousand separators (spaces, non-breaking spaces)
	normalized := strings.ReplaceAll(text, "\u00A0", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	// Replace comma decimal separator with dot
	normalized = strings.ReplaceAll(normalized, ",", ".")

	// Must match a simple number pattern: optional sign, digits, optional one decimal part
	if !numericPattern.MatchString(normalized) {
		return false
	}

	// Reject leading zeros: "007", "00123" — likely document/phone numbers
	// Allow: "0", "0.5", "-0.12"
	digits := normalized
	if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
		digits = digits[1:]
	}
	if len(digits) > 1 && digits[0] == '0' && digits[1] != '.' {
		return false
	}

	// Reject overly long digit strings — likely codes or identifiers
	digitOnly := strings.ReplaceAll(digits, ".", "")
	if len(digitOnly) > maxNumericStringLen {
		return false
	}

	return true
}

// looksNumeric checks if text looks like a number, handling Russian formatting
// (spaces, nbsp as thousand separators, comma as decimal separator).
func looksNumeric(text string) bool {
	normalized := strings.ReplaceAll(text, "\u00A0", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, ",", ".")
	return numericPattern.MatchString(normalized)
}

// normalizeFieldName lowercases and removes whitespace from a field name.
func normalizeFieldName(fieldName string) string {
	result := strings.Join(strings.Fields(fieldName), "")
	return strings.ToLower(result)
}

func normalizeFieldSet(fields map[string]bool) map[string]bool {
	if fields == nil {
		return make(map[string]bool)
	}
	normalized := make(map[string]bool, len(fields))
	for f := range fields {
		normalized[normalizeFieldName(f)] = true
	}
	return normalized
}

func valueEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AllowPlainKeywordsCSV returns the default allow-list keywords as a comma-separated string.
func AllowPlainKeywordsCSV() string {
	return strings.Join(DefaultAllowPlainKeywords, ", ")
}

// ─── NER-based free text masking for execute_code ───────────────

// nerMatch represents a single detected entity in text.
type nerMatch struct {
	start       int
	end         int
	original    string
	aliasPrefix string
}

// Built-in NER regex patterns (always active).
var (
	// СНИЛС: 123-456-789 01
	reSnils = regexp.MustCompile(`\b\d{3}-\d{3}-\d{3}\s?\d{2}\b`)

	// Phone: +7/8 (xxx) xxx-xx-xx and variants
	rePhone = regexp.MustCompile(`(?:\+7|8)[\s-]?\(?\d{3}\)?[\s-]?\d{3}[\s-]?\d{2}[\s-]?\d{2}`)

	// Email
	reEmail = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// БИК: starts with 04, 9 digits total
	reBik = regexp.MustCompile(`\b04\d{7}\b`)

	// ОГРН (13 digits) / ОГРНИП (15 digits)
	reOgrn = regexp.MustCompile(`\b\d{13}\b`)
	reOgrnip = regexp.MustCompile(`\b\d{15}\b`)

	// ИНН with context: "ИНН 7701234567" or "ИНН: 7701234567"
	reInnCtx = regexp.MustCompile(`(?i)ИНН[\s:=]*(\d{10,12})\b`)

	// КПП with context
	reKppCtx = regexp.MustCompile(`(?i)КПП[\s:=]*(\d{9})\b`)

	// Organization: ООО/АО/ИП/... + name in quotes or next words
	reOrgQuoted = regexp.MustCompile(`(?:ООО|ОАО|ЗАО|ПАО|АО|ИП|НКО|ФГУП|МУП|ГУП)\s*[""«]([^""»]+)[""»]`)
	reOrgPlain  = regexp.MustCompile(`(?:ООО|ОАО|ЗАО|ПАО|АО|ИП|НКО|ФГУП|МУП|ГУП)\s+([А-ЯЁ][а-яёА-ЯЁ\s\-]{1,40})`)

	// ФИО: Иванов И.И. / Иванов И. И.
	reFioShort = regexp.MustCompile(`[А-ЯЁ][а-яё]+\s+[А-ЯЁ]\.\s?[А-ЯЁ]\.`)

	// ФИО full: Иванов Иван Иванович (3rd word ends with -вич/-вна/-ич/-чна)
	reFioFull = regexp.MustCompile(`[А-ЯЁ][а-яё]{1,30}\s+[А-ЯЁ][а-яё]{1,30}\s+[А-ЯЁ][а-яё]*(вич|вна|ич|чна)\b`)

	// Context value extractor: "Keyword": "value" (JSON), Keyword: value, Keyword = value
	reCtxJSON  = regexp.MustCompile(`(?i)"%s"\s*:\s*"([^"]+)"`)
	reCtxColon = regexp.MustCompile(`(?i)%s\s*:\s*(.+?)(?:\n|,|\t|$)`)
	reCtxEq    = regexp.MustCompile(`(?i)%s\s*=\s*(.+?)(?:\n|,|\t|;|$)`)
)

// SanitizeFreeText applies NER-based masking to free-form text.
// Returns the masked text and an alias→original map for later rehydration.
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

	// Helper to generate alias
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

	// СНИЛС
	for _, loc := range reSnils.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "СНИЛС"})
	}

	// Phone
	for _, loc := range rePhone.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Телефон"})
	}

	// Email
	for _, loc := range reEmail.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Email"})
	}

	// БИК
	for _, loc := range reBik.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "БИК"})
	}

	// ИНН with context
	for _, m := range reInnCtx.FindAllStringSubmatchIndex(text, -1) {
		if m[2] >= 0 && m[3] >= 0 {
			matches = append(matches, nerMatch{m[2], m[3], text[m[2]:m[3]], "ИНН"})
		}
	}

	// КПП with context
	for _, m := range reKppCtx.FindAllStringSubmatchIndex(text, -1) {
		if m[2] >= 0 && m[3] >= 0 {
			matches = append(matches, nerMatch{m[2], m[3], text[m[2]:m[3]], "КПП"})
		}
	}

	// ОГРН/ОГРНИП (only if near context word)
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

	// Organizations (quoted)
	for _, loc := range reOrgQuoted.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "Организация"})
	}

	// Organizations (plain, only if not already covered by quoted)
	for _, loc := range reOrgPlain.FindAllStringIndex(text, -1) {
		orig := strings.TrimSpace(text[loc[0]:loc[1]])
		matches = append(matches, nerMatch{loc[0], loc[0] + len(orig), orig, "Организация"})
	}

	// ФИО short (Иванов И.И.)
	for _, loc := range reFioShort.FindAllStringIndex(text, -1) {
		matches = append(matches, nerMatch{loc[0], loc[1], text[loc[0]:loc[1]], "ФИО"})
	}

	// ФИО full (Иванов Иван Иванович)
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

		// Custom regex
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
	// Sort by position descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].start > matches[j].start
	})

	// Sensitive prefixes: numeric values must still be masked for these.
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
		// Skip numeric values UNLESS they are sensitive identifiers
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

	// JSON: "Keyword": "value"
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

	// Colon: Keyword: value (until newline, comma, tab)
	reCol, err := regexp.Compile(fmt.Sprintf(`(?i)%s\s*:\s*([^\n,\t;]{2,60})`, regexp.QuoteMeta(keyword)))
	if err == nil {
		for _, m := range reCol.FindAllStringSubmatchIndex(text, -1) {
			if m[2] >= 0 && m[3] >= 0 {
				val := strings.TrimSpace(text[m[2]:m[3]])
				// Skip if it starts with a quote (already handled by JSON pattern)
				if val != "" && !strings.HasPrefix(val, "\"") && !looksNumeric(val) {
					results = append(results, nerMatch{m[2], m[2] + len(val), val, ""})
				}
			}
		}
	}

	// Equals: Keyword = value
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

	// Sort by length descending (longer matches win), then by position
	sort.Slice(matches, func(i, j int) bool {
		li := matches[i].end - matches[i].start
		lj := matches[j].end - matches[j].start
		if li != lj {
			return li > lj
		}
		return matches[i].start < matches[j].start
	})

	var result []nerMatch
	occupied := make([]bool, 0) // simple interval check

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
	_ = occupied

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
