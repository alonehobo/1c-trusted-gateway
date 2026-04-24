package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// TypePolicyDecision is the outcome of classifying a column by its 1C types.
type TypePolicyDecision int

const (
	// TypeDecisionUnknown — schema unavailable or types[] empty; defer to downstream rules.
	TypeDecisionUnknown TypePolicyDecision = iota
	// TypeDecisionPlain — column is safe to expose as-is.
	TypeDecisionPlain
	// TypeDecisionMask — column must be masked regardless of name whitelist.
	TypeDecisionMask
)

func (d TypePolicyDecision) String() string {
	switch d {
	case TypeDecisionPlain:
		return "plain"
	case TypeDecisionMask:
		return "mask"
	default:
		return "unknown"
	}
}

// TypePolicy maps 1C metadata types to plain/mask decisions.
//
// A type is either:
//   - an exact name: "Справочник.Валюты", "Число", "Перечисление.ВидыДвижения"
//   - a prefix ending with ".": "Перечисление.", "ПланСчетов.", "Документ."
//
// forcedMask always wins over plain matches.
//
// Single-word types (no ".") are treated as platform-defined primitives or
// system enums (e.g. "Число", "ВидДвиженияНакопления"). They are plain by
// default, except for the small free-text/binary blacklist
// (stringBinaryDefaults) which always carries user content.
type TypePolicy struct {
	plainExact     map[string]bool
	plainPrefixes  []string
	forcedExact    map[string]bool
	forcedPrefixes []string
}

// stringBinaryDefaults — single-word platform types that MUST stay masked
// even though they have no dot. Free text or binary blobs may contain
// arbitrary user data.
var stringBinaryDefaults = map[string]bool{
	"Строка":            true,
	"String":            true,
	"ХранилищеЗначения": true,
	"ValueStorage":      true,
	"ДвоичныеДанные":    true,
	"BinaryData":        true,
}

// NewDefaultTypePolicy returns a policy with hardcoded defaults (see design.md).
func NewDefaultTypePolicy() *TypePolicy {
	tp := &TypePolicy{
		plainExact:     make(map[string]bool),
		plainPrefixes:  nil,
		forcedExact:    make(map[string]bool),
		forcedPrefixes: nil,
	}
	// Plain prefixes — whole families of safe metadata objects.
	// Имена типов берутся из MCP-сервиса 1С, который использует
	// Метаданные.НайтиПоТипу().ПолноеИмя() — это форма БЕЗ "Ссылка".
	// (см. mcp_ИнструментЗапросыКБазе/Ext/ManagerModule.bsl, СхемаКолонокТЗ).
	// Формы СправочникСсылка.*/ПеречислениеСсылка.*/ПланСчетовСсылка.*
	// в JSON-ответах не встречаются и в default'ы не добавляются.
	tp.plainPrefixes = []string{
		"Перечисление.",
		"ПланСчетов.",
		"ПланВидовХарактеристик.",
		"Документ.",
	}
	// Plain exact — narrowly-safe reference catalogs.
	for _, t := range []string{
		"Справочник.Валюты",
		"Справочник.СтавкиНДС",
		"Справочник.СтраныМира",
		"Справочник.БанкиРФ",
		// Primitives that are plain by default.
		"Число",
		"Number",
		"Дата",
		"Date",
		"Булево",
		"Boolean",
		"УникальныйИдентификатор",
		"UUID",
	} {
		tp.plainExact[t] = true
	}
	// By default, Строка/String stays out of plain — the free-text primitive
	// is masked unless the user's name-whitelist overrides it.
	return tp
}

// PersistedTypePolicy is the JSON shape saved in credential store.
type PersistedTypePolicy struct {
	PlainTypes         []string `json:"plain_types"`
	PlainPrefixes      []string `json:"plain_prefixes"`
	ForcedMaskTypes    []string `json:"forced_mask_types"`
	ForcedMaskPrefixes []string `json:"forced_mask_prefixes"`
}

// MergePersisted applies user overrides on top of defaults. Empty additions
// are ignored; duplicates are de-duplicated.
func (tp *TypePolicy) MergePersisted(persisted string) {
	if strings.TrimSpace(persisted) == "" {
		return
	}
	var p PersistedTypePolicy
	if err := json.Unmarshal([]byte(persisted), &p); err != nil {
		return
	}
	for _, t := range p.PlainTypes {
		t = strings.TrimSpace(t)
		if t != "" {
			tp.plainExact[t] = true
		}
	}
	for _, pref := range p.PlainPrefixes {
		pref = strings.TrimSpace(pref)
		if pref != "" && !containsString(tp.plainPrefixes, pref) {
			tp.plainPrefixes = append(tp.plainPrefixes, pref)
		}
	}
	for _, t := range p.ForcedMaskTypes {
		t = strings.TrimSpace(t)
		if t != "" {
			tp.forcedExact[t] = true
		}
	}
	for _, pref := range p.ForcedMaskPrefixes {
		pref = strings.TrimSpace(pref)
		if pref != "" && !containsString(tp.forcedPrefixes, pref) {
			tp.forcedPrefixes = append(tp.forcedPrefixes, pref)
		}
	}
}

// Decide classifies a column given its 1C types and truncation flag.
//
// Rules (see design.md):
//  1. types empty → Unknown (defer to name-based rules).
//  2. Any type matches forcedMask (exact or prefix) → Mask.
//  3. Per type:
//     - dotted ("Справочник.X", "Перечисление.X"): must match plainExact
//     or plainPrefixes, otherwise the column is Masked.
//     - dotless ("Число", "ВидДвиженияНакопления"): plain by default
//     (platform-defined primitive or system enum), unless the type is in
//     the free-text/binary blacklist (stringBinaryDefaults). User
//     plainExact entries always win over the blacklist.
//  4. All visible types resolve to plain → Plain. Otherwise → Mask.
//
// "Null"/"Неопределено" are treated as nullability markers, not real types,
// and are stripped before classification (1C always appends "Null" to
// composite-type columns such as ["Дата","Null"]).
//
// truncated currently does NOT force masking: for large composites like
// Регистратор we classify by the visible type slice returned by 1C.
func (tp *TypePolicy) Decide(types []string, truncated bool) TypePolicyDecision {
	_ = truncated
	// Gather non-empty types, excluding nullability markers.
	var effective []string
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" || isNullabilityMarker(t) {
			continue
		}
		effective = append(effective, t)
	}
	if len(effective) == 0 {
		return TypeDecisionUnknown
	}
	for _, t := range effective {
		if tp.forcedExact[t] || matchAnyPrefix(t, tp.forcedPrefixes) {
			return TypeDecisionMask
		}
	}
	for _, t := range effective {
		if !tp.isPlain(t) {
			return TypeDecisionMask
		}
	}
	return TypeDecisionPlain
}

// isPlain reports whether a single non-empty, non-nullability type resolves
// to plain. Dotted types must be on the plain whitelist; dotless types are
// plain by default unless blacklisted (and user plainExact still overrides).
func (tp *TypePolicy) isPlain(t string) bool {
	if tp.plainExact[t] {
		return true
	}
	if strings.Contains(t, ".") {
		return matchAnyPrefix(t, tp.plainPrefixes)
	}
	// Dotless = platform-defined type. Plain unless explicitly blacklisted.
	return !stringBinaryDefaults[t]
}

// Snapshot returns a stable JSON representation of the current policy
// (for the UI table and settings export).
func (tp *TypePolicy) Snapshot() PersistedTypePolicy {
	plainExact := make([]string, 0, len(tp.plainExact))
	for k := range tp.plainExact {
		plainExact = append(plainExact, k)
	}
	sort.Strings(plainExact)

	plainPrefixes := append([]string(nil), tp.plainPrefixes...)
	sort.Strings(plainPrefixes)

	forcedExact := make([]string, 0, len(tp.forcedExact))
	for k := range tp.forcedExact {
		forcedExact = append(forcedExact, k)
	}
	sort.Strings(forcedExact)

	forcedPrefixes := append([]string(nil), tp.forcedPrefixes...)
	sort.Strings(forcedPrefixes)

	return PersistedTypePolicy{
		PlainTypes:         plainExact,
		PlainPrefixes:      plainPrefixes,
		ForcedMaskTypes:    forcedExact,
		ForcedMaskPrefixes: forcedPrefixes,
	}
}

// isNullabilityMarker returns true for 1C type names that only signal a
// nullable column (not a real data type), which Decide must ignore.
func isNullabilityMarker(t string) bool {
	switch strings.ToLower(t) {
	case "null", "неопределено", "undefined":
		return true
	}
	return false
}

func matchAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
