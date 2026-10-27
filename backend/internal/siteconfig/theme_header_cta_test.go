package siteconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestThemeConfigHeaderCTAOmittedWhenUnset(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(DefaultThemeConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "headerCta") {
		t.Fatalf("headerCta must be omitted when unset: %s", b)
	}
	want := `{"colors":{"primary":"#0ea5e9","secondary":"#64748b","accent":"#f59e0b","background":"#ffffff","text":"#1e293b"},` +
		`"typography":{"fontFamily":"Inter, sans-serif","headingFamily":"Inter, sans-serif"},` +
		`"borderRadius":{"sm":"4px","md":"8px","lg":"16px"}}`
	if string(b) != want {
		t.Fatalf("default theme JSON changed:\n got %s\nwant %s", b, want)
	}
}

func TestThemeConfigHeaderCTARoundTrip(t *testing.T) {
	t.Parallel()
	in := DefaultThemeConfig()
	in.HeaderCTA = &ThemeCTA{Text: "Get started", URL: "/start"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"headerCta":{"text":"Get started","url":"/start"}`) {
		t.Fatalf("headerCta missing: %s", b)
	}
	var out ThemeConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.HeaderCTA == nil || *out.HeaderCTA != *in.HeaderCTA {
		t.Fatalf("round trip: %+v", out.HeaderCTA)
	}
}
