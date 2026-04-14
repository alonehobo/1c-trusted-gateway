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

	// Type-aware masking: when typePolicy is non-nil the sanitizer consults
	// it (using columnTypes/columnTruncated) before falling back to the
	// name-based whitelist. See design.md layer 5.
	typePolicy      *TypePolicy
	columnTypes     map[string][]string
	columnTruncated map[string]bool
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

	// Type-aware masking (layer 5): when column schema is known, consult the
	// TypePolicy before name-based rules. A decisive plain/mask verdict here
	// wins over the name-based allow/deny lists so that the user's type
	// policy is the authoritative source.
	if ds.typePolicy != nil {
		var types []string
		truncated := false
		if ds.columnTypes != nil {
			if t, ok := ds.columnTypes[fieldName]; ok {
				types = t
			} else if t, ok := ds.columnTypes[normalizedName]; ok {
				types = t
			}
		}
		if ds.columnTruncated != nil {
			if v, ok := ds.columnTruncated[fieldName]; ok {
				truncated = v
			} else if v, ok := ds.columnTruncated[normalizedName]; ok {
				truncated = v
			}
		}
		switch ds.typePolicy.Decide(types, truncated) {
		case TypeDecisionPlain:
			return value
		case TypeDecisionMask:
			return ds.aliasFor(fieldName, value, originalToAlias, aliasToOriginal)
		}
		// Unknown → fall through to name-based rules (legacy behavior).
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
