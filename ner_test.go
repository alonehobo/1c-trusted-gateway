package main

import (
	"strings"
	"testing"
)

func TestSanitizeFreeTextMasksDigitOnlyPhone(t *testing.T) {
	for _, phone := range []string{"89161234567", "+7 916 123 45 67"} {
		out, _ := SanitizeFreeText("Тел: "+phone, nil, "s", 10)
		if strings.Contains(out, phone) {
			t.Fatalf("phone %q leaked: %q", phone, out)
		}
	}
}

func TestSanitizeFreeTextMasksTypographicQuotedOrg(t *testing.T) {
	out, _ := SanitizeFreeText("Поставщик ООО “ромашка плюс”", nil, "s", 10)
	if strings.Contains(out, "ромашка") {
		t.Fatalf("org leaked: %q", out)
	}
}
