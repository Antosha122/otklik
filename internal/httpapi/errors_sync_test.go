package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var (
	errLiteralRe = regexp.MustCompile(`errorResp\{"((?:[^"\\]|\\.)*)"`)
	dictKeyRe    = regexp.MustCompile(`^\s*'([^']+)'\s*:`)
	hasCyrillic  = func(s string) bool {
		for _, r := range s {
			if unicode.Is(unicode.Cyrillic, r) {
				return true
			}
		}
		return false
	}
)

// TestErrorDictionarySync не даёт серверным английским сообщениям «уйти мимо»
// русского словаря фронтенда: каждый литерал errorResp{"..."} в исходниках
// хендлеров должен иметь перевод в web/assets/js/core/errors.js — либо сам
// быть русскоязычным текстом для заявителя (перевод не нужен).
func TestErrorDictionarySync(t *testing.T) {
	dictData, err := os.ReadFile(filepath.Join("web", "assets", "js", "core", "errors.js"))
	if err != nil {
		t.Fatalf("прочитать errors.js: %v", err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(dictData), "\n") {
		if m := dictKeyRe.FindStringSubmatch(line); m != nil {
			known[m[1]] = true
		}
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range errLiteralRe.FindAllStringSubmatch(string(data), -1) {
			msg := strings.ReplaceAll(m[1], `\"`, `"`)
			if hasCyrillic(msg) || known[msg] {
				continue
			}
			missing = append(missing, f+": "+msg)
		}
	}
	for _, m := range missing {
		t.Errorf("нет перевода в errors.js: %s", m)
	}
	if len(missing) > 0 {
		t.Logf("всего без перевода: %d — добавьте ключи в web/assets/js/core/errors.js", len(missing))
	}
}
