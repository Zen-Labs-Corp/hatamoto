package server

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/spam"
)

func TestCSPWithExtrasEmptyIsBase(t *testing.T) {
	t.Parallel()
	for _, base := range []string{renderCSP, renderCSPStrict} {
		if got := cspWithExtras(base, cspExtras{}); got != base {
			t.Errorf("empty extras changed the policy: %q", got)
		}
		if got := cspWithExtras(base, captchaExtras("none")); got != base {
			t.Errorf("provider none changed the policy: %q", got)
		}
	}
}

func TestCSPWithExtrasCaptchaOutputUnchanged(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		spam.ProviderTurnstile: "https://challenges.cloudflare.com",
		spam.ProviderRecaptcha: "https://www.google.com https://www.gstatic.com",
		spam.ProviderHCaptcha:  "https://hcaptcha.com https://*.hcaptcha.com",
	}
	for provider, origins := range want {
		got := cspWithExtras(renderCSP, captchaExtras(provider))
		if !strings.Contains(got, "script-src 'self' 'unsafe-inline' "+origins+";") ||
			!strings.Contains(got, "connect-src 'self' "+origins+";") {
			t.Errorf("%s: %q", provider, got)
		}
		strict := cspWithExtras(renderCSPStrict, captchaExtras(provider))
		if !strings.Contains(strict, "script-src 'self' "+origins+";") || strings.Contains(strings.SplitN(strict, "style-src", 2)[0], "unsafe-inline") {
			t.Errorf("%s strict: %q", provider, strict)
		}
	}
}

func TestCSPWithExtrasAllDirectives(t *testing.T) {
	t.Parallel()
	e := cspExtras{
		Script:  []string{"https://a.example"},
		Connect: []string{"https://b.example", "wss://b.example"},
		Style:   []string{"https://c.example"},
		Font:    []string{"https://d.example"},
	}
	got := cspWithExtras(renderCSP, e)
	for _, want := range []string{
		"script-src 'self' 'unsafe-inline' https://a.example;",
		"connect-src 'self' https://b.example wss://b.example;",
		"style-src 'self' 'unsafe-inline' https://c.example;",
		"font-src 'self' https://d.example;",
		"frame-src https:;", "img-src 'self' data: https:;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestCSPWithExtrasMissingDirectiveLeftOut(t *testing.T) {
	t.Parallel()
	got := cspWithExtras("default-src 'self'; script-src 'self'", cspExtras{Script: []string{"https://a.example"}, Font: []string{"https://f.example"}})
	if got != "default-src 'self'; script-src 'self' https://a.example" {
		t.Errorf("got %q", got)
	}
}

func TestCSPExtrasMergeDedupes(t *testing.T) {
	t.Parallel()
	a := captchaExtras(spam.ProviderRecaptcha)
	b := cspExtras{Script: []string{"https://www.google.com", "https://x.example"}}
	m := a.merge(b)
	if strings.Join(m.Script, " ") != "https://www.google.com https://www.gstatic.com https://x.example" {
		t.Errorf("script = %v", m.Script)
	}
	if (cspExtras{}).merge(cspExtras{}).empty() != true {
		t.Error("empty merge not empty")
	}
}
