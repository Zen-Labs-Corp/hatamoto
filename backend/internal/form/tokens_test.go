package form

import (
	"strings"
	"testing"
)

func TestFillTokens(t *testing.T) {
	t.Parallel()
	fields := []FormField{{Name: "first_name"}, {Name: "email"}}
	data := map[string]any{"first_name": "Ada", "email": "a@x.com", "secret": "no", "n": 3}
	long := strings.Repeat("x", 300)
	tests := []struct{ name, tpl, want string }{
		{"known", "New enquiry from {{first_name}}", "New enquiry from Ada"},
		{"spaced", "Hi {{ first_name }}!", "Hi Ada!"},
		{"unknown token empty", "a{{nope}}b", "ab"},
		{"unlisted data key empty", "{{secret}}", ""},
		{"missing value empty", "[{{email}}] [{{first_name}}]", "[a@x.com] [Ada]"},
		{"no tokens", "plain", "plain"},
		{"cr lf in template", "a\r\nb", "a b"},
		{"cr lf in value", "{{first_name}}", "Ada"},
		{"cap", long, long[:200]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := FillTokens(tc.tpl, fields, data); got != tc.want {
				t.Errorf("FillTokens(%q) = %q, want %q", tc.tpl, got, tc.want)
			}
		})
	}
	got := FillTokens("{{first_name}}", fields, map[string]any{"first_name": "Ada\r\nBcc: evil@x.com"})
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("CR/LF survived: %q", got)
	}
}
