package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/spam"
)

const spamTestSecret = "s3cr3t-captcha-KEY-0123456789"

// fakeSpam is a SpamService that really seals the secret, so "saved" and
// "readable" behave as in production.
type fakeSpam struct {
	set      spam.Settings
	key      []byte
	saved    int
	saveErr  error
	testOK   bool
	testText string
	testErr  error
	tests    int
	tested   spam.Settings // the settings TestSecret was last called with
}

func newFakeSpam() *fakeSpam {
	return &fakeSpam{set: spam.Defaults(), key: spam.SecretKey("test-server-secret")}
}

func (f *fakeSpam) Settings(context.Context) (spam.Settings, error) { return f.set, nil }
func (f *fakeSpam) Save(_ context.Context, s spam.Settings) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved++
	f.set = s
	return nil
}
func (f *fakeSpam) SealSecret(secret string) ([]byte, error) { return spam.SealSecret(f.key, secret) }
func (f *fakeSpam) SecretReadable(s spam.Settings) bool {
	_, ok := s.Secret(f.key)
	return ok
}
func (f *fakeSpam) TestSecret(_ context.Context, s spam.Settings) (bool, string, error) {
	f.tests++
	f.tested = s
	return f.testOK, f.testText, f.testErr
}

func (f *fakeSpam) withSecret(provider, siteKey string) *fakeSpam {
	sealed, _ := f.SealSecret(spamTestSecret)
	f.set.Provider, f.set.SiteKey, f.set.SecretSealed = provider, siteKey, sealed
	return f
}

type spamRig struct {
	*ownerRig
	svc    *fakeSpam
	events []AuditEvent
}

func newSpamRig(t *testing.T, svc *fakeSpam) *spamRig {
	t.Helper()
	g := newOwnerRig(t, true)
	s := &spamRig{ownerRig: g, svc: svc}
	if svc != nil {
		g.a.deps.Spam = svc
	}
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { s.events = append(s.events, e) }
	return s
}

func (s *spamRig) get(t *testing.T) string {
	t.Helper()
	rec := s.do("GET", "/admin/settings/spam", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func goodSpamForm() url.Values {
	return url.Values{
		"provider": {"turnstile"}, "site_key": {"0x4AAA-site"}, "secret_key": {spamTestSecret},
		"min_seconds": {"5"}, "max_links": {"1"}, "blocked_words": {"Crypto, seo services\nviagra"},
		"scripts": {"cyrillic", "cjk"}, "action": {"refuse"},
	}
}

func TestSpamPageGoldenAndNoSecret(t *testing.T) {
	s := newSpamRig(t, newFakeSpam().withSecret(spam.ProviderTurnstile, "0x4AAA-site"))
	body := s.get(t)
	for _, want := range []string{"Captcha provider", "Cloudflare Turnstile", "0x4AAA-site", "Test connection", "Leave blank to keep the saved secret key.", `placeholder="Saved"`, "Minimum time to submit", "Block scripts", "Save as Spam, no notification", "Refuse submission"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, spamTestSecret) {
		t.Error("page contains the secret")
	}
	if strings.Contains(body, "hm-callout--warning") {
		t.Error("warning shown with no captcha forms")
	}
	compareGolden(t, "pages/settings-spam.html", normalizePage(body))
}

func TestSpamScriptChipsSitInARow(t *testing.T) {
	body := newSpamRig(t, newFakeSpam()).get(t)
	i := strings.Index(body, `id="scripts"`)
	if i < 0 || !strings.Contains(body[i:], `<div class="hm-field__chips">`) || strings.Index(body[i:], `hm-field__chips`) > strings.Index(body[i:], `name="scripts"`) {
		t.Error("the block-script chips must sit inside .hm-field__chips")
	}
}

func TestSpamPageDefaults(t *testing.T) {
	body := newSpamRig(t, newFakeSpam()).get(t)
	for _, want := range []string{`name="min_seconds" value="3"`, `name="max_links" value="2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("defaults lack %q", want)
		}
	}
}

func TestSpamOwnerOnly(t *testing.T) {
	s := newSpamRig(t, newFakeSpam())
	for _, m := range []string{"GET", "POST"} {
		for _, p := range []string{"/admin/settings/spam", "/admin/settings/spam/test"} {
			if m == "GET" && p != "/admin/settings/spam" {
				continue
			}
			if rec := s.do(m, p, "editor-token", goodSpamForm(), nil); rec.Code != 403 {
				t.Errorf("editor %s %s = %d, want 403", m, p, rec.Code)
			}
		}
	}
	if s.svc.saved != 0 {
		t.Error("editor save reached the service")
	}
	// The owner's Forms tabs include Spam protection; the editor's never do (nav test).
	if body := s.do("GET", "/admin/settings/spam", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `href="/admin/settings/spam"`) {
		t.Error("owner lacks the Spam protection tab")
	}
}

func TestSpamUnavailable(t *testing.T) {
	t.Parallel()

	s := newSpamRig(t, nil)
	if rec := s.do("GET", "/admin/settings/spam", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d, want 503", rec.Code)
	}
}

func TestSpamSave(t *testing.T) {
	s := newSpamRig(t, newFakeSpam())
	rec := s.do("POST", "/admin/settings/spam", "owner-token", goodSpamForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/spam" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	got := s.svc.set
	if got.Provider != "turnstile" || got.SiteKey != "0x4AAA-site" || got.MinSeconds != 5 || got.MaxLinks != 1 || got.Action != "refuse" {
		t.Fatalf("saved = %+v", got)
	}
	if strings.Join(got.BlockedWords, "|") != "Crypto|seo services|viagra" || strings.Join(got.BlockedScripts, ",") != "cyrillic,cjk" {
		t.Errorf("lists = %v %v", got.BlockedWords, got.BlockedScripts)
	}
	if secret, ok := got.Secret(s.svc.key); !ok || secret != spamTestSecret {
		t.Error("secret was not sealed into the settings")
	}
	if s.flushed != 1 {
		t.Errorf("page cache invalidated %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "spam.settings" || s.events[0].Detail["secret_changed"] != true {
		t.Fatalf("audit = %+v", s.events)
	}
	if strings.Contains(fmt.Sprint(s.events[0]), spamTestSecret) || strings.Contains(fmt.Sprintf("%+v", s.events[0].Detail), "SecretSealed") {
		t.Error("audit detail carries the secret")
	}
	if strings.Contains(rec.Body.String(), spamTestSecret) {
		t.Error("response carries the secret")
	}
}

func TestSpamBlankSecretKeepsStored(t *testing.T) {
	svc := newFakeSpam().withSecret(spam.ProviderTurnstile, "old-key")
	before := append([]byte(nil), svc.set.SecretSealed...)
	s := newSpamRig(t, svc)
	form := goodSpamForm()
	form.Set("secret_key", "")
	form.Set("site_key", "new-key")
	if rec := s.do("POST", "/admin/settings/spam", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if string(svc.set.SecretSealed) != string(before) {
		t.Error("blank secret replaced the stored one")
	}
	if svc.set.SiteKey != "new-key" {
		t.Errorf("site key = %q", svc.set.SiteKey)
	}
	if s.events[0].Detail["secret_changed"] != false {
		t.Errorf("secret_changed = %v", s.events[0].Detail["secret_changed"])
	}
}

func TestSpamSaveValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(url.Values)
		stored *fakeSpam
		want   string
	}{
		{"min time too high", func(v url.Values) { v.Set("min_seconds", "61") }, newFakeSpam(), "from 0 to 60"},
		{"min time not a number", func(v url.Values) { v.Set("min_seconds", "x") }, newFakeSpam(), "from 0 to 60"},
		{"max links negative", func(v url.Values) { v.Set("max_links", "-1") }, newFakeSpam(), "from 0 to 50"},
		{"unknown provider", func(v url.Values) { v.Set("provider", "evil") }, newFakeSpam(), "Choose one of the listed providers."},
		{"site key required", func(v url.Values) { v.Set("site_key", " ") }, newFakeSpam(), "Enter the site key"},
		{"secret required", func(v url.Values) { v.Set("secret_key", "") }, newFakeSpam(), "Enter the secret key"},
		{"secret required for a new provider", func(v url.Values) { v.Set("secret_key", ""); v.Set("provider", "hcaptcha") },
			newFakeSpam().withSecret(spam.ProviderTurnstile, "k"), "for the new provider"},
		{"bad script", func(v url.Values) { v.Add("scripts", "klingon") }, newFakeSpam(), "listed scripts"},
		{"bad action", func(v url.Values) { v.Set("action", "nuke") }, newFakeSpam(), "Choose Save as Spam"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSpamRig(t, c.stored)
			form := goodSpamForm()
			c.mutate(form)
			rec := s.do("POST", "/admin/settings/spam", "owner-token", form, nil)
			if rec.Code != 422 {
				t.Fatalf("save = %d, want 422", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, c.want) {
				t.Errorf("no message %q", c.want)
			}
			if strings.Contains(body, spamTestSecret) {
				t.Error("re-rendered form echoes the secret")
			}
			if s.svc.saved != 0 || s.flushed != 0 {
				t.Error("invalid save was stored or purged the cache")
			}
		})
	}
}

func TestSpamProviderNoneNeedsNoKeys(t *testing.T) {
	s := newSpamRig(t, newFakeSpam())
	form := goodSpamForm()
	form.Set("provider", "none")
	form.Set("site_key", "")
	form.Set("secret_key", "")
	if rec := s.do("POST", "/admin/settings/spam", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
}

func TestSpamSaveFailureIs500AndLeaksNothing(t *testing.T) {
	svc := newFakeSpam()
	svc.saveErr = errors.New("db down")
	s := newSpamRig(t, svc)
	rec := s.do("POST", "/admin/settings/spam", "owner-token", goodSpamForm(), nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), spamTestSecret) || s.flushed != 0 {
		t.Errorf("save failure = %d flushed=%d", rec.Code, s.flushed)
	}
}

func TestSpamWarningBanner(t *testing.T) {
	ff := newFakeForms()
	ff.addForm("Contact").Settings.EnableCaptcha = true
	ff.addForm("Quote").Settings.EnableCaptcha = true
	ff.addForm("Plain")
	svc := newFakeSpam()
	s := newSpamRig(t, svc)
	s.a.deps.Growth.Forms = ff
	body := s.get(t)
	if !strings.Contains(body, "Captcha is required on 2 forms but no provider keys are set. Submissions are accepted unchecked.") {
		t.Error("no warning for 2 captcha forms without keys")
	}
	// A secret that no longer opens counts as no keys, and the page says so.
	svc.withSecret(spam.ProviderTurnstile, "k")
	svc.key = spam.SecretKey("rotated")
	body = s.get(t)
	if !strings.Contains(body, "hm-callout--warning") || !strings.Contains(body, "can no longer be read") {
		t.Error("unreadable secret is not reported")
	}
	// Working keys clear the warning.
	svc.key = spam.SecretKey("test-server-secret")
	if body = s.get(t); strings.Contains(body, "hm-callout--warning") {
		t.Error("warning shown with usable keys")
	}
}

func TestSpamTestConnection(t *testing.T) {
	// Posting no settings fields tests what is saved.
	cases := []struct {
		name  string
		svc   *fakeSpam
		calls int
		want  string
	}{
		{"valid", func() *fakeSpam {
			f := newFakeSpam().withSecret("turnstile", "k")
			f.testOK, f.testText = true, "The provider accepted the secret."
			return f
		}(), 1, "Click Save to keep these keys."},
		{"invalid secret", func() *fakeSpam {
			f := newFakeSpam().withSecret("turnstile", "k")
			f.testText = "The provider says this secret key is not valid."
			return f
		}(), 1, "this secret key is not valid"},
		{"provider down", func() *fakeSpam {
			f := newFakeSpam().withSecret("turnstile", "k")
			f.testErr = errors.New("captcha provider unreachable")
			return f
		}(), 1, "could not be reached"},
		{"no provider", newFakeSpam(), 0, "Choose a captcha provider"},
		{"no secret", func() *fakeSpam { f := newFakeSpam(); f.set.Provider = "turnstile"; f.set.SiteKey = "k"; return f }(), 0, "Enter the secret key to test."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSpamRig(t, c.svc)
			rec := s.do("POST", "/admin/settings/spam/test", "owner-token", url.Values{}, nil)
			if rec.Code != 200 {
				t.Fatalf("test = %d", rec.Code)
			}
			if c.svc.tests != c.calls {
				t.Errorf("provider calls = %d, want %d", c.svc.tests, c.calls)
			}
			if body := rec.Body.String(); strings.Contains(body, "Save settings") || !strings.Contains(body, c.want) || strings.Contains(body, spamTestSecret) || !strings.Contains(body, `id="spam-test-result"`) {
				t.Errorf("result lacks %q or leaks the secret", c.want)
			}
		})
	}
}

// TestSpamTestConnectionUsesTypedKeys is the regression for "Test connection
// says save first": the keys typed on the page are tested without saving.
func TestSpamTestConnectionUsesTypedKeys(t *testing.T) {
	typed := url.Values{
		"provider": {"hcaptcha"}, "site_key": {"typed-site"}, "secret_key": {spamTestSecret},
		"min_seconds": {"3"}, "max_links": {"2"}, "action": {"spam"},
	}
	t.Run("fresh install, nothing saved", func(t *testing.T) {
		svc := newFakeSpam()
		svc.testOK = true
		s := newSpamRig(t, svc)
		rec := s.do("POST", "/admin/settings/spam/test", "owner-token", typed, nil)
		body := rec.Body.String()
		if rec.Code != 200 || svc.tests != 1 || !strings.Contains(body, "Connection works.") {
			t.Fatalf("code=%d tests=%d body lacks success", rec.Code, svc.tests)
		}
		if svc.tested.Provider != "hcaptcha" || svc.tested.SiteKey != "typed-site" {
			t.Errorf("tested %q/%q, want the typed provider and site key", svc.tested.Provider, svc.tested.SiteKey)
		}
		if got, ok := svc.tested.Secret(svc.key); !ok || got != spamTestSecret {
			t.Error("tested secret is not the typed one")
		}
		if svc.saved != 0 || svc.set.Provider != spam.ProviderNone {
			t.Error("Test connection saved the settings")
		}
		if strings.Contains(body, spamTestSecret) || !strings.Contains(body, "typed-site") {
			t.Error("page leaks the secret or drops the typed site key")
		}
	})
	t.Run("blank secret uses the saved one for the same provider", func(t *testing.T) {
		svc := newFakeSpam().withSecret("hcaptcha", "old-site")
		svc.testOK = true
		s := newSpamRig(t, svc)
		v := url.Values{}
		for k, vs := range typed {
			v[k] = vs
		}
		v.Set("secret_key", "")
		s.do("POST", "/admin/settings/spam/test", "owner-token", v, nil)
		if svc.tests != 1 || svc.tested.SiteKey != "typed-site" {
			t.Fatalf("tests=%d site=%q", svc.tests, svc.tested.SiteKey)
		}
	})
	t.Run("blank secret with a changed provider asks for it", func(t *testing.T) {
		svc := newFakeSpam().withSecret("turnstile", "old-site")
		s := newSpamRig(t, svc)
		v := url.Values{}
		for k, vs := range typed {
			v[k] = vs
		}
		v.Set("secret_key", "")
		rec := s.do("POST", "/admin/settings/spam/test", "owner-token", v, nil)
		if svc.tests != 0 || !strings.Contains(rec.Body.String(), "Enter the secret key to test.") {
			t.Fatalf("tests=%d, want a prompt for the new provider's secret", svc.tests)
		}
	})
}
