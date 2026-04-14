package main

import "testing"

func TestTypePolicyDecide(t *testing.T) {
	tp := NewDefaultTypePolicy()

	cases := []struct {
		name      string
		types     []string
		truncated bool
		want      TypePolicyDecision
	}{
		{"truncated всегда mask", []string{"Перечисление.ВидыДвижения"}, true, TypeDecisionMask},
		{"пустой → unknown", nil, false, TypeDecisionUnknown},
		{"пустые строки → unknown", []string{"", " "}, false, TypeDecisionUnknown},
		{"Перечисление.* → plain (по префиксу)", []string{"Перечисление.ВидыДвижения"}, false, TypeDecisionPlain},
		{"ПланСчетов.* → plain", []string{"ПланСчетов.Хозрасчетный"}, false, TypeDecisionPlain},
		{"ПланВидовХарактеристик.* → plain", []string{"ПланВидовХарактеристик.ВидыСубконто"}, false, TypeDecisionPlain},
		{"Справочник.Валюты (exact) → plain", []string{"Справочник.Валюты"}, false, TypeDecisionPlain},
		{"Справочник.Номенклатура → mask", []string{"Справочник.Номенклатура"}, false, TypeDecisionMask},
		// Ссылочные формы (СправочникСсылка.*, ПеречислениеСсылка.*) в реальных
		// JSON-ответах от MCP-сервиса 1С не встречаются — они в default'ах не
		// заданы. Если вдруг придут — попадут под mask, как любой неизвестный тип.
		{"ПеречислениеСсылка.* → mask (нет в default'ах)", []string{"ПеречислениеСсылка.ВидыДвижения"}, false, TypeDecisionMask},
		{"СправочникСсылка.Валюты → mask (нет в default'ах)", []string{"СправочникСсылка.Валюты"}, false, TypeDecisionMask},
		{"Документ.* → mask (не в plain)", []string{"Документ.РеализацияТоваровУслуг"}, false, TypeDecisionMask},
		{"Число → plain (примитив)", []string{"Число"}, false, TypeDecisionPlain},
		{"Number → plain (англ.)", []string{"Number"}, false, TypeDecisionPlain},
		{"Дата → plain", []string{"Дата"}, false, TypeDecisionPlain},
		{"Булево → plain", []string{"Булево"}, false, TypeDecisionPlain},
		{"УникальныйИдентификатор → plain", []string{"УникальныйИдентификатор"}, false, TypeDecisionPlain},
		{"Строка → mask (по умолчанию)", []string{"Строка"}, false, TypeDecisionMask},
		{"String → mask (англ.)", []string{"String"}, false, TypeDecisionMask},
		{"ХранилищеЗначения → mask (бинарь)", []string{"ХранилищеЗначения"}, false, TypeDecisionMask},
		{"ValueStorage → mask (англ.)", []string{"ValueStorage"}, false, TypeDecisionMask},
		{"ДвоичныеДанные → mask (бинарь)", []string{"ДвоичныеДанные"}, false, TypeDecisionMask},
		// Системные enum'ы платформы — plain по dot-эвристике, без явного whitelist.
		{"ВидДвиженияНакопления (системный enum) → plain", []string{"ВидДвиженияНакопления"}, false, TypeDecisionPlain},
		{"ВидДвиженияБухгалтерии → plain", []string{"ВидДвиженияБухгалтерии"}, false, TypeDecisionPlain},
		{"ВидСчета → plain", []string{"ВидСчета"}, false, TypeDecisionPlain},
		{"AccumulationRecordType (англ.) → plain", []string{"AccumulationRecordType"}, false, TypeDecisionPlain},
		// Гипотетический будущий платформенный тип — тоже plain, без обновления списка.
		{"новый платформенный тип без точки → plain", []string{"НовыйСистемныйТип2030"}, false, TypeDecisionPlain},
		// Композит: ВидДвижения + Null (1С добавляет Null к составным колонкам).
		{"ВидДвиженияНакопления + Null → plain", []string{"ВидДвиженияНакопления", "Null"}, false, TypeDecisionPlain},
		{"составной: все plain → plain", []string{"Перечисление.ВидыДвижения", "Число"}, false, TypeDecisionPlain},
		{"составной: один опасный → mask", []string{"Перечисление.ВидыДвижения", "Справочник.Контрагенты"}, false, TypeDecisionMask},
		// 1C attaches "Null" to composite-type columns; it's a nullability
		// marker, not a real type, and must be ignored during classification.
		{"Дата + Null → plain (Null игнорируется)", []string{"Дата", "Null"}, false, TypeDecisionPlain},
		{"Null + Булево → plain", []string{"Null", "Булево"}, false, TypeDecisionPlain},
		{"Null + Число → plain", []string{"Null", "Число"}, false, TypeDecisionPlain},
		{"Справочник.Валюты + Null → plain", []string{"Справочник.Валюты", "Null"}, false, TypeDecisionPlain},
		{"Перечисление.* + Null → plain", []string{"Перечисление.ХозяйственныеОперации", "Null"}, false, TypeDecisionPlain},
		{"Справочник.Контрагенты + Null → mask", []string{"Справочник.Контрагенты", "Null"}, false, TypeDecisionMask},
		{"только Null → unknown (nullability без типа)", []string{"Null"}, false, TypeDecisionUnknown},
		{"Неопределено тоже маркер → unknown", []string{"Неопределено"}, false, TypeDecisionUnknown},
		{"регистронезависимо: null → unknown", []string{"null"}, false, TypeDecisionUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tp.Decide(tc.types, tc.truncated)
			if got != tc.want {
				t.Errorf("Decide(%v, %v) = %s; want %s",
					tc.types, tc.truncated, got, tc.want)
			}
		})
	}
}

func TestTypePolicyMergePersisted(t *testing.T) {
	tp := NewDefaultTypePolicy()
	// User adds a custom plain prefix and a forced-mask type.
	persisted := `{
		"plain_types": ["Справочник.Организации"],
		"plain_prefixes": ["БизнесПроцесс."],
		"forced_mask_types": ["Перечисление.СекретноеПеречисление"]
	}`
	tp.MergePersisted(persisted)

	// User-added plain type.
	if got := tp.Decide([]string{"Справочник.Организации"}, false); got != TypeDecisionPlain {
		t.Errorf("user plain type not honored: got %s", got)
	}
	// User-added plain prefix.
	if got := tp.Decide([]string{"БизнесПроцесс.Согласование"}, false); got != TypeDecisionPlain {
		t.Errorf("user plain prefix not honored: got %s", got)
	}
	// User forced-mask overrides the default Перечисление. prefix.
	if got := tp.Decide([]string{"Перечисление.СекретноеПеречисление"}, false); got != TypeDecisionMask {
		t.Errorf("forced-mask priority broken: got %s", got)
	}
	// Other Перечисление.* still plain.
	if got := tp.Decide([]string{"Перечисление.ВидыДвижения"}, false); got != TypeDecisionPlain {
		t.Errorf("default plain lost after merge: got %s", got)
	}
}

func TestTypePolicyUserPlainOverridesBlacklist(t *testing.T) {
	tp := NewDefaultTypePolicy()
	// User explicitly whitelists Строка — overrides default blacklist.
	tp.MergePersisted(`{"plain_types": ["Строка"]}`)
	if got := tp.Decide([]string{"Строка"}, false); got != TypeDecisionPlain {
		t.Errorf("user plain_types must override stringBinaryDefaults: got %s", got)
	}
}

func TestTypePolicyForcedMaskBeatsDotlessDefault(t *testing.T) {
	tp := NewDefaultTypePolicy()
	// User force-masks a dotless system enum.
	tp.MergePersisted(`{"forced_mask_types": ["ВидДвиженияНакопления"]}`)
	if got := tp.Decide([]string{"ВидДвиженияНакопления"}, false); got != TypeDecisionMask {
		t.Errorf("forced_mask_types must override dotless plain default: got %s", got)
	}
}

func TestTypePolicyForcedMaskPrefixPriority(t *testing.T) {
	tp := NewDefaultTypePolicy()
	tp.MergePersisted(`{"forced_mask_prefixes": ["Перечисление."]}`)
	// With the prefix forced, nothing in Перечисление.* can be plain.
	if got := tp.Decide([]string{"Перечисление.ВидыДвижения"}, false); got != TypeDecisionMask {
		t.Errorf("forced-mask prefix not honored: got %s", got)
	}
}

func TestTypePolicyIgnoresInvalidJSON(t *testing.T) {
	tp := NewDefaultTypePolicy()
	tp.MergePersisted(`{not valid json`)
	// Defaults survive.
	if got := tp.Decide([]string{"Число"}, false); got != TypeDecisionPlain {
		t.Errorf("invalid JSON corrupted defaults: got %s", got)
	}
}
