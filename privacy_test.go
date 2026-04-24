package main

import "testing"

// TestSanitizerTypePolicyIntegration verifies that the sanitizer respects
// the TypePolicy decision before falling back to name-based rules.
func TestSanitizerTypePolicyIntegration(t *testing.T) {
	tp := NewDefaultTypePolicy()

	rows := []map[string]any{
		{
			"ВидДвижения":   "Приход",
			"СчетДт":        "41.01",
			"Регистратор":   "Реализация товаров и услуг 000123",
			"Контрагент":    "ООО Рога и Копыта",
			"Период":        "2026-01-15",
			"Сумма":         "12345.67",
			"Идентификатор": "abc-def-123",
		},
	}
	// Имена типов — как их отдаёт MCP-сервис 1С (Метаданные.НайтиПоТипу().ПолноеИмя()),
	// форма БЕЗ "Ссылка": "Справочник.X", "Перечисление.X", "ПланСчетов.X".
	columnTypes := map[string][]string{
		"ВидДвижения":   {"Перечисление.ВидыДвижения"},
		"СчетДт":        {"ПланСчетов.Хозрасчетный"},
		"Регистратор":   {"Документ.РеализацияТоваровУслуг"},
		"Контрагент":    {"Справочник.Контрагенты"},
		"Период":        {"Дата"},
		"Сумма":         {"Число"},
		"Идентификатор": {"УникальныйИдентификатор"},
	}

	ds := NewDataSanitizer("test-salt", 8, 10)
	ds.typePolicy = tp
	ds.columnTypes = columnTypes

	sanitized := ds.SanitizeRows(rows, nil, nil)
	got := sanitized.MaskedRows[0]

	// Enum / chart-of-accounts / Date / Number / UUID — plain.
	for _, safeField := range []string{"ВидДвижения", "СчетДт", "Регистратор", "Период", "Сумма", "Идентификатор"} {
		if got[safeField] != rows[0][safeField] {
			t.Errorf("%s should be plain (got %v; want %v)",
				safeField, got[safeField], rows[0][safeField])
		}
	}
	// Контрагент → Справочник.* (not in whitelist) → mask.
	if got["Контрагент"] == rows[0]["Контрагент"] {
		t.Errorf("Контрагент should be masked; got plain: %v", got["Контрагент"])
	}
}

func TestSanitizerDefaultExactPlainFields(t *testing.T) {
	rows := []map[string]any{{
		"Номер":         "000123",
		"Дата":          "2026-01-15",
		"НомерПаспорта": "1234 567890",
		"ДатаРождения":  "1990-05-01",
	}}

	ds := NewDataSanitizer("salt", 8, 10)
	sanitized := ds.SanitizeRows(rows, nil, nil)
	got := sanitized.MaskedRows[0]

	if got["Номер"] != rows[0]["Номер"] {
		t.Fatalf("Номер should be plain by default; got %v", got["Номер"])
	}
	if got["Дата"] != rows[0]["Дата"] {
		t.Fatalf("Дата should be plain by default; got %v", got["Дата"])
	}
	if got["НомерПаспорта"] == rows[0]["НомерПаспорта"] {
		t.Fatalf("НомерПаспорта must stay masked")
	}
	if got["ДатаРождения"] == rows[0]["ДатаРождения"] {
		t.Fatalf("ДатаРождения must stay masked")
	}
}

// TestSanitizerTypePolicyTruncatedAllowsSafeTypes — truncated no longer forces
// mask; visible safe types stay plain.
func TestSanitizerTypePolicyTruncatedAllowsSafeTypes(t *testing.T) {
	tp := NewDefaultTypePolicy()
	rows := []map[string]any{{"X": "some value"}}
	columnTypes := map[string][]string{"X": {"Перечисление.Одно"}}
	truncated := map[string]bool{"X": true}

	ds := NewDataSanitizer("salt", 8, 10)
	ds.typePolicy = tp
	ds.columnTypes = columnTypes
	ds.columnTruncated = truncated

	sanitized := ds.SanitizeRows(rows, nil, nil)
	if sanitized.MaskedRows[0]["X"] != rows[0]["X"] {
		t.Errorf("truncated safe column should stay plain; got %v", sanitized.MaskedRows[0]["X"])
	}
}

// TestSanitizerTypePolicyUnknownFallsThrough — when schema is absent, the
// name-based rules still apply (backwards compatibility).
func TestSanitizerTypePolicyUnknownFallsThrough(t *testing.T) {
	tp := NewDefaultTypePolicy()
	rows := []map[string]any{{"Any": "secret"}}
	// No columnTypes — type policy returns Unknown, old behavior wins.
	ds := NewDataSanitizer("salt", 8, 10)
	ds.typePolicy = tp

	// Without whitelist, name-based default masks everything.
	sanitized := ds.SanitizeRows(rows, nil, nil)
	if sanitized.MaskedRows[0]["Any"] == rows[0]["Any"] {
		t.Errorf("unknown type should fall through to name-based default (mask); got plain")
	}

	// With allow-plain, the field is allowed.
	allow := map[string]bool{"any": true}
	sanitized2 := ds.SanitizeRows(rows, nil, allow)
	if sanitized2.MaskedRows[0]["Any"] != rows[0]["Any"] {
		t.Errorf("allow-plain should pass value through; got %v", sanitized2.MaskedRows[0]["Any"])
	}
}

// TestSanitizerForceMaskBeatsTypePolicy — explicit force-mask wins over the
// type policy's plain verdict.
func TestSanitizerAllowPlainSupportsSuffixWildcard(t *testing.T) {
	rows := []map[string]any{{
		"ДокументРегистратор": "Реализация товаров",
		"Контрагент":          "ООО Ромашка",
	}}

	ds := NewDataSanitizer("salt", 8, 10)
	allow := map[string]bool{"*регистратор": true}
	sanitized := ds.SanitizeRows(rows, nil, allow)

	if sanitized.MaskedRows[0]["ДокументРегистратор"] != rows[0]["ДокументРегистратор"] {
		t.Fatalf("suffix wildcard should allow plain value; got %v", sanitized.MaskedRows[0]["ДокументРегистратор"])
	}
	if sanitized.MaskedRows[0]["Контрагент"] == rows[0]["Контрагент"] {
		t.Fatalf("non-matching field should stay masked")
	}
}

func TestSanitizerAllowPlainSupportsPrefixWildcard(t *testing.T) {
	rows := []map[string]any{{
		"РегистраторДокумента": "Реализация товаров",
		"Контрагент":           "ООО Ромашка",
	}}

	ds := NewDataSanitizer("salt", 8, 10)
	allow := map[string]bool{"регистратор*": true}
	sanitized := ds.SanitizeRows(rows, nil, allow)

	if sanitized.MaskedRows[0]["РегистраторДокумента"] != rows[0]["РегистраторДокумента"] {
		t.Fatalf("prefix wildcard should allow plain value; got %v", sanitized.MaskedRows[0]["РегистраторДокумента"])
	}
	if sanitized.MaskedRows[0]["Контрагент"] == rows[0]["Контрагент"] {
		t.Fatalf("non-matching field should stay masked")
	}
}

func TestSanitizerForceMaskBeatsTypePolicy(t *testing.T) {
	tp := NewDefaultTypePolicy()
	rows := []map[string]any{{"Сумма": "100"}}
	columnTypes := map[string][]string{"Сумма": {"Число"}}

	ds := NewDataSanitizer("salt", 8, 10)
	ds.typePolicy = tp
	ds.columnTypes = columnTypes

	force := map[string]bool{"сумма": true}
	sanitized := ds.SanitizeRows(rows, force, nil)
	if sanitized.MaskedRows[0]["Сумма"] == rows[0]["Сумма"] {
		t.Errorf("force-mask should beat type policy plain; got plain")
	}
}

// TestExtractRowsWithSchema verifies the schema-aware JSON parser.
func TestExtractRowsWithSchema(t *testing.T) {
	raw := `{
		"version": 1,
		"columns": [
			{"name": "Контрагент", "types": ["Справочник.Контрагенты"]},
			{"name": "Сумма", "types": ["Число"]}
		],
		"rows": [
			{"Контрагент": "ООО Ромашка", "Сумма": "500"}
		]
	}`
	rows, cols, order := extractRowsWithSchema(raw)
	if len(rows) != 1 {
		t.Fatalf("want 1 row; got %d", len(rows))
	}
	if len(cols) != 2 {
		t.Fatalf("want 2 columns; got %d", len(cols))
	}
	if len(order) != 2 || order[0] != "Контрагент" || order[1] != "Сумма" {
		t.Errorf("column order wrong: %v", order)
	}
	if rows[0]["Сумма"] != "500" {
		t.Errorf("row value wrong: %v", rows[0]["Сумма"])
	}
}

// TestExtractRowsWithSchemaFallsBackOnBadJSON — legacy TSV untouched.
func TestExtractRowsWithSchemaFallsBackOnBadJSON(t *testing.T) {
	rows, cols, order := extractRowsWithSchema("A\tB\n1\t2")
	if rows != nil || cols != nil || order != nil {
		t.Errorf("TSV input must produce nils; got rows=%v cols=%v order=%v",
			rows, cols, order)
	}
}
