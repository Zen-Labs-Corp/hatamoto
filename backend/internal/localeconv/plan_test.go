package localeconv

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const validPlan = `{
  "locales": [
    {"tag":"en-XX","segment":"xx","root_slug":"xx"},
    {"tag":"en-ZZ","segment":"zz","root_slug":"zz"}
  ],
  "default": "en-XX",
  "x_default": "en-XX",
  "locale_from": {"attribute":"region","map":{"xx":"en-XX","zz":"en-ZZ"}}
}`

func mustParse(t *testing.T, s string) *Plan {
	t.Helper()
	p, err := ParsePlan(strings.NewReader(s))
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	return p
}

func codes(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

func TestParsePlanDefaults(t *testing.T) {
	p := mustParse(t, validPlan)
	if p.Version != 1 || p.RootSchema != "page" || !p.PrefixAllValue() || p.Shared != "und" ||
		p.LocaleFrom.Fallback != "root" || p.Home.Slug != "home" || p.Home.OnConflict != "rename-archived" ||
		p.StripAttributes == nil || p.GroupFrom != nil {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if got := p.Validate(); len(got) != 0 {
		t.Fatalf("valid plan has issues: %v", got)
	}
}

func TestParsePlanStrict(t *testing.T) {
	for name, in := range map[string]string{
		"unknown field":  `{"locales":[],"sitemap_naming":"auto"}`,
		"unknown nested": `{"locale_from":{"attribute":"a","bogus":1}}`,
		"trailing data":  validPlan + `{}`,
		"not json":       `nope`,
		"wrong type":     `{"prefix_all":"yes"}`,
	} {
		if _, err := ParsePlan(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestPrefixAllFalseAccepted(t *testing.T) {
	p := mustParse(t, strings.Replace(validPlan, `"default"`, `"prefix_all": false, "default"`, 1))
	if p.PrefixAllValue() || len(p.Validate()) != 0 {
		t.Fatalf("prefix_all=false must parse and validate: %v", p.Validate())
	}
}

func TestValidateCodes(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Plan)
		want string
	}{
		{"version", func(p *Plan) { p.Version = 2 }, CodeUnsupportedVersion},
		{"one locale", func(p *Plan) { p.Locales = p.Locales[:1] }, CodeNeedTwoLocales},
		{"too many", func(p *Plan) {
			for len(p.Locales) <= 32 {
				p.Locales = append(p.Locales, LocaleSpec{Tag: "en-AA", Segment: "aa", RootSlug: "aa"})
			}
		}, CodeTooManyLocales},
		{"bad tag", func(p *Plan) { p.Locales[0].Tag = "not a tag!" }, CodeBadTag},
		{"und tag", func(p *Plan) { p.Locales[0].Tag = "und" }, CodeBadTag},
		{"duplicate tag", func(p *Plan) { p.Locales[1].Tag = "en_xx" }, CodeDuplicateTag},
		{"empty segment", func(p *Plan) { p.Locales[0].Segment = "" }, CodeBadSegment},
		{"upper segment", func(p *Plan) { p.Locales[0].Segment = "XX" }, CodeBadSegment},
		{"long segment", func(p *Plan) { p.Locales[0].Segment = strings.Repeat("a", 33) }, CodeBadSegment},
		{"duplicate segment", func(p *Plan) { p.Locales[1].Segment = "xx" }, CodeDuplicateSegment},
		{"reserved segment", func(p *Plan) { p.Locales[0].Segment = "admin" }, CodeReservedSegment},
		{"reserved sitemap", func(p *Plan) { p.Locales[0].Segment = "sitemap-x" }, CodeReservedSegment},
		{"reserved tag", func(p *Plan) { p.Locales[0].Segment = "tag" }, CodeReservedSegment},
		{"empty root", func(p *Plan) { p.Locales[0].RootSlug = "" }, CodeBadRootSlug},
		{"duplicate root", func(p *Plan) { p.Locales[1].RootSlug = "xx" }, CodeDuplicateRoot},
		{"default missing", func(p *Plan) { p.Default = "en-AA" }, CodeDefaultNotListed},
		{"default empty", func(p *Plan) { p.Default = "" }, CodeDefaultNotListed},
		{"x-default missing", func(p *Plan) { p.XDefault = "en-AA" }, CodeXDefaultNotListed},
		{"no attribute", func(p *Plan) { p.LocaleFrom.Attribute = "" }, CodeMissingAttribute},
		{"map unknown", func(p *Plan) { p.LocaleFrom.Map["qq"] = "en-AA" }, CodeMapUnknownTag},
		{"bad fallback", func(p *Plan) { p.LocaleFrom.Fallback = "other" }, CodeBadFallback},
		{"empty group attr", func(p *Plan) { p.GroupFrom = &GroupFrom{} }, CodeBadGroupAttribute},
		{"bad shared", func(p *Plan) { p.Shared = "en-XX" }, CodeBadShared},
		{"duplicate strip", func(p *Plan) { p.StripAttributes = []string{"a", "a"} }, CodeDuplicateStrip},
		{"bad home slug", func(p *Plan) { p.Home.Slug = "Home Page" }, CodeBadHomeSlug},
		{"bad on_conflict", func(p *Plan) { p.Home.OnConflict = "skip" }, CodeBadOnConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParse(t, validPlan)
			tc.edit(p)
			got := codes(p.Validate())
			if !slices.Contains(got, tc.want) {
				t.Fatalf("want %s in %v", tc.want, got)
			}
		})
	}
}

func TestStripsLocaleAttribute(t *testing.T) {
	p := mustParse(t, validPlan)
	if p.StripsLocaleAttribute() {
		t.Fatal("nothing stripped")
	}
	p.StripAttributes = []string{"region"}
	if !p.StripsLocaleAttribute() {
		t.Fatal("region is stripped")
	}
}

func TestBlockingError(t *testing.T) {
	var err error = &BlockingError{Issues: []Issue{{Code: CodePathChanges, Message: "3 paths change"}}}
	var b *BlockingError
	if !errors.As(err, &b) || !strings.Contains(err.Error(), "path_changes") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestServiceNotImplemented(t *testing.T) {
	s := NewService(nil, nil)
	if _, err := s.History(t.Context()); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("got %v", err)
	}
}
