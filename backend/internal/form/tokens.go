package form

import (
	"fmt"
	"regexp"
	"strings"
)

const maxFilledTokensRunes = 200

var tokenRe = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

// FillTokens replaces {{field_name}} in tpl with the submitted value of that
// field. Tokens naming no field of the form are replaced with nothing. The
// result is one line of at most 200 runes, safe for a mail header.
func FillTokens(tpl string, fields []FormField, data map[string]any) string {
	known := make(map[string]bool, len(fields))
	for i := range fields {
		known[fields[i].Name] = true
	}
	out := tokenRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := tokenRe.FindStringSubmatch(m)[1]
		v, ok := data[name]
		if !known[name] || !ok || v == nil {
			return ""
		}
		return fmt.Sprint(v)
	})
	out = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(out)), " ")
	if r := []rune(out); len(r) > maxFilledTokensRunes {
		out = string(r[:maxFilledTokensRunes])
	}
	return out
}
