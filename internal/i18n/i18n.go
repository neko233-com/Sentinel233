package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

var translations map[string]map[string]string

func init() {
	translations = make(map[string]map[string]string)
	loadLocale("zh-CN")
	loadLocale("en-US")
	loadLocale("ja-JP")
}

func loadLocale(lang string) {
	data, err := localeFS.ReadFile(fmt.Sprintf("locales/%s.json", lang))
	if err != nil {
		translations[lang] = make(map[string]string)
		return
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		translations[lang] = make(map[string]string)
		return
	}
	translations[lang] = m
}

func T(lang, key string, args ...interface{}) string {
	m, ok := translations[lang]
	if !ok {
		m = translations["zh-CN"]
	}
	msg, ok := m[key]
	if !ok {
		msg = key
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	return msg
}

func Languages() []string {
	langs := make([]string, 0, len(translations))
	for k := range translations {
		langs = append(langs, k)
	}
	return langs
}

func DetectLang(acceptLang string) string {
	if strings.HasPrefix(acceptLang, "zh") {
		return "zh-CN"
	}
	if strings.HasPrefix(acceptLang, "ja") {
		return "ja-JP"
	}
	return "en-US"
}
