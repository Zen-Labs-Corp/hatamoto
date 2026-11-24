package changeset

import (
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"testing"
)

func TestWriteTooLarge(t *testing.T) {
	t.Parallel()
	many := make([]string, MaxCreateStrings+1)
	manyAttrs := make([]string, MaxCreateStrings)
	for i := range manyAttrs {
		manyAttrs[i] = `"` + strconv.Itoa(i) + `"`
	}
	for name, c := range map[string]struct {
		strs  []string
		attrs string
		want  bool
	}{
		"small":                   {[]string{"name", "slug"}, `{"url":"https://example.test"}`, false},
		"empty":                   {nil, "", false},
		"string at the cap":       {[]string{strings.Repeat("a", MaxCreateString)}, "", false},
		"string over the cap":     {[]string{strings.Repeat("a", MaxCreateString+1)}, "", true},
		"too many strings":        {many, "", true},
		"strings at the cap":      {many[:MaxCreateStrings], "", false},
		"attribute string over":   {nil, `{"text":"` + strings.Repeat("a", MaxCreateString+1) + `"}`, true},
		"attributes over":         {nil, `{"text":"` + strings.Repeat("a", MaxCreateAttributes) + `"}`, true},
		"keys and values counted": {[]string{"x"}, `{"a":[` + strings.Join(manyAttrs, ",") + `]}`, true},
	} {
		if got := WriteTooLarge(c.strs, jsontext.Value(c.attrs)); got != c.want {
			t.Errorf("%s: WriteTooLarge = %v, want %v", name, got, c.want)
		}
	}
}
