package seo

import (
	"hatamoto.dev/core/internal/jsonx"
	"strings"
	"testing"
)

func TestAIFilesEffective(t *testing.T) {
	var z AIFiles
	if !z.IsZero() || z.EffectivePosts() != DefaultAIPosts || z.EffectiveFullCapBytes() != 2048<<10 {
		t.Fatalf("zero value is not the default: %+v", z)
	}
	a := AIFiles{Posts: 5, FullCapKB: 64}
	if a.IsZero() || a.EffectivePosts() != 5 || a.EffectiveFullCapBytes() != 64<<10 {
		t.Fatalf("saved values ignored: %+v", a)
	}
	if got := (AIFiles{Posts: 999, FullCapKB: 1}).EffectivePosts(); got != DefaultAIPosts {
		t.Fatalf("out-of-range posts = %d", got)
	}
}

func TestValidateAIFiles(t *testing.T) {
	cases := []struct {
		name  string
		in    AIFiles
		field string
	}{
		{"zero", AIFiles{}, ""},
		{"ok", AIFiles{Posts: 200, FullCapKB: 20480, Summary: "x"}, ""},
		{"posts high", AIFiles{Posts: 201}, "ai_files[posts]"},
		{"posts negative", AIFiles{Posts: -1}, "ai_files[posts]"},
		{"cap low", AIFiles{FullCapKB: 63}, "ai_files[full_cap_kb]"},
		{"cap high", AIFiles{FullCapKB: 20481}, "ai_files[full_cap_kb]"},
		{"summary", AIFiles{Summary: strings.Repeat("é", 301)}, "ai_files[summary]"},
		{"summary ok", AIFiles{Summary: strings.Repeat("é", 300)}, ""},
	}
	for _, c := range cases {
		errs := validateAIFiles(c.in)
		switch {
		case c.field == "" && len(errs) != 0, c.field != "" && (len(errs) != 1 || errs[0].Field != c.field):
			t.Errorf("%s: errs = %v, want field %q", c.name, errs, c.field)
		}
	}
	s := Defaults()
	s.AIFiles.Posts = 500
	if errs := s.Validate(); len(errs) != 1 || errs[0].Field != "ai_files[posts]" {
		t.Fatalf("Settings.Validate = %v", errs)
	}
}

func TestAIFilesJSON(t *testing.T) {
	b, err := jsonx.Marshal(Defaults())
	if err != nil || strings.Contains(string(b), "ai_files") {
		t.Fatalf("zero value must omit the key: %s %v", b, err)
	}
	s := Defaults()
	s.AIFiles = AIFiles{LLMsOff: true, Posts: 5}
	b, _ = jsonx.Marshal(s)
	if !strings.Contains(string(b), `"ai_files":{"llms_off":true,"posts":5}`) {
		t.Fatalf("round trip lost the section: %s", b)
	}
	var back Settings
	if err := jsonx.Unmarshal(b, &back); err != nil || back.AIFiles != s.AIFiles {
		t.Fatalf("decode = %+v %v", back.AIFiles, err)
	}
}
