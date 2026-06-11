package i18n

import "testing"

func TestTZhCN(t *testing.T) {
	msg := T("zh-CN", "app.title")
	if msg == "" || msg == "app.title" {
		t.Fatalf("expected zh-CN translation for app.title, got '%s'", msg)
	}
}

func TestTEnUS(t *testing.T) {
	msg := T("en-US", "app.title")
	if msg == "" || msg == "app.title" {
		t.Fatalf("expected en-US translation for app.title, got '%s'", msg)
	}
}

func TestTJaJP(t *testing.T) {
	msg := T("ja-JP", "app.title")
	if msg == "" || msg == "app.title" {
		t.Fatalf("expected ja-JP translation for app.title, got '%s'", msg)
	}
}

func TestTDetectLang(t *testing.T) {
	if DetectLang("zh-CN") != "zh-CN" {
		t.Fatal("expected zh-CN")
	}
	if DetectLang("en-US") != "en-US" {
		t.Fatal("expected en-US")
	}
	if DetectLang("ja-JP") != "ja-JP" {
		t.Fatal("expected ja-JP")
	}
	if DetectLang("fr-FR") != "en-US" {
		t.Fatal("expected en-US as fallback")
	}
}

func TestTLanguages(t *testing.T) {
	langs := Languages()
	if len(langs) < 3 {
		t.Fatalf("expected at least 3 languages, got %d", len(langs))
	}
}

func TestTFallback(t *testing.T) {
	msg := T("unknown-lang", "app.title")
	if msg == "" {
		t.Fatal("expected fallback to zh-CN")
	}
}

func TestTUnknownKey(t *testing.T) {
	msg := T("en-US", "nonexistent.key.12345")
	if msg != "nonexistent.key.12345" {
		t.Fatalf("expected key as fallback, got '%s'", msg)
	}
}
