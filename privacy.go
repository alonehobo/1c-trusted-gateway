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
	uuidPattern = regexp.MustCompile(
		`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`,
	)
	alwaysSensitiveFields = map[string]bool{
		"инн": true,
		"кпп": true,
	}
	numericPattern = regexp.MustCompile(`^[+-]?\d+(?:\.\d+)?$`)
	wordPattern    = regexp.MustCompile(`[A-Za-zА-Яа-яЁё0-9]+`)
)

// SanitizedResult holds the result of data sanitization.
type SanitizedResult struct {
	MaskedRows      []map[string]any `json:"masked_rows"`
	DisplayRows     []map[string]any `json:"display_rows"`
	AliasToOriginal map[string]string `json:"alias_to_original"`
	MaskedColumns   []string          `json:"masked_columns"`
}

// DataSanitizer masks sensitive data in query results.
type DataSanitizer struct {
	salt        string
	aliasLength int
}

// NewDataSanitizer creates a new sanitizer with the given salt and alias length.
func NewDataSanitizer(salt string, aliasLength int) *DataSanitizer {
	if aliasLength <= 0 {
		aliasLength = 10
	}
	return &DataSanitizer{
		salt:        salt,
		aliasLength: aliasLength,
	}
}

// SanitizeRows masks sensitive values in the given rows.
func (ds *DataSanitizer) SanitizeRows(
	rows []map[string]any,
	forceMaskFields map[string]bool,
	allowPlainFields map[string]bool,
) *SanitizedResult {
	aliasToOriginal := make(map[string]string)
	originalToAlias := make(map[string]string)
	maskedColumnsSet := make(map[string]bool)
	maskedRows := make([]map[string]any, 0, len(rows))

	forceMask := normalizeFieldSet(forceMaskFields)
	allowPlain := normalizeFieldSet(allowPlainFields)

	for _, row := range rows {
		maskedRow := make(map[string]any, len(row))
		for fieldName, value := range row {
			if ds.shouldMask(fieldName, value, forceMask, allowPlain) {
				alias := ds.aliasFor(fieldName, value, originalToAlias, aliasToOriginal)
				maskedRow[fieldName] = alias
				maskedColumnsSet[fieldName] = true
			} else {
				maskedRow[fieldName] = value
			}
		}
		maskedRows = append(maskedRows, maskedRow)
	}

	maskedColumns := make([]string, 0, len(maskedColumnsSet))
	for col := range maskedColumnsSet {
		maskedColumns = append(maskedColumns, col)
	}
	sort.Strings(maskedColumns)

	return &SanitizedResult{
		MaskedRows:      maskedRows,
		DisplayRows:     rows,
		AliasToOriginal: aliasToOriginal,
		MaskedColumns:   maskedColumns,
	}
}

// RehydrateText replaces aliases in text with their original values.
func RehydrateText(text string, aliasToOriginal map[string]string) string {
	if text == "" || len(aliasToOriginal) == 0 {
		return text
	}

	// Collect aliases that appear in text
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

	// Sort by alias length descending to replace longer aliases first
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].alias) > len(pairs[j].alias)
	})

	result := text
	for _, p := range pairs {
		result = strings.ReplaceAll(result, p.alias, p.original)
	}
	return result
}

func (ds *DataSanitizer) shouldMask(
	fieldName string,
	value any,
	forceMaskFields map[string]bool,
	allowPlainFields map[string]bool,
) bool {
	normalizedName := normalizeFieldName(fieldName)

	if value == nil {
		return false
	}

	switch value.(type) {
	case map[string]any, []any:
		return false
	}

	if forceMaskFields[normalizedName] {
		return true
	}

	switch value.(type) {
	case int, int64, float64, bool:
		return false
	}

	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" {
		return false
	}

	if allowPlainFields[normalizedName] {
		return false
	}

	if alwaysSensitiveFields[normalizedName] {
		return true
	}

	if looksNumeric(text) {
		// Leading zeros = code/serial, not a real number (e.g. "00000142")
		if len(text) > 1 && text[0] == '0' && text[1] != '.' {
			// fall through to mask
		} else if len([]rune(text)) <= 10 {
			// Short numbers (≤10 chars) are amounts, not serials
			return false
		}
	}

	if uuidPattern.MatchString(text) {
		return true
	}

	return true
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
