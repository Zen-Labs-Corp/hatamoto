package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSEOFormSocialTextRoundTrip(t *testing.T) {
	t.Parallel()

	m := map[string]any{"og_title": "Old", "og_description": "Old d", "other": 1}
	seoForm{OGTitle: "  New ", OGDescription: ""}.merge(m)
	if m["og_title"] != "New" {
		t.Errorf("og_title = %v", m["og_title"])
	}
	if _, ok := m["og_description"]; ok {
		t.Errorf("empty og_description kept")
	}
	if m["other"] != 1 {
		t.Errorf("unrelated key touched")
	}
	if f := seoFrom(map[string]any{"og_title": "A", "og_description": "B"}); f.OGTitle != "A" || f.OGDescription != "B" {
		t.Errorf("seoFrom = %+v", f)
	}

	r := &http.Request{PostForm: url.Values{"seo_og_title": {"T"}, "seo_og_description": {"D"}}}
	if f := seoFromForm(r); f.OGTitle != "T" || f.OGDescription != "D" {
		t.Errorf("seoFromForm = %+v", f)
	}
	found := 0
	for _, k := range seoKeys {
		if strings.HasPrefix(k, "og_") && k != "og_image" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("seoKeys lacks social text keys: %v", seoKeys)
	}
}
