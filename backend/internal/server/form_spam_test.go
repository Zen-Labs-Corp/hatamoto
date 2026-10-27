package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/spam"
)

type fixedSettings struct{ s spam.Settings }

func (f fixedSettings) Get(context.Context) (spam.Settings, error) { return f.s, nil }

type fakeVerifier struct {
	res    spam.Result
	err    error
	tokens []string
	ips    []string
}

func (v *fakeVerifier) Verify(_ context.Context, _, secret, _, token, ip string) (spam.Result, error) {
	if secret != "provider-secret" {
		return spam.Result{}, errors.New("wrong secret")
	}
	v.tokens = append(v.tokens, token)
	v.ips = append(v.ips, ip)
	return v.res, v.err
}

var spamNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// newSpamServer returns a form server whose spam pipeline reads set, with the
// captcha secret sealed when set names a provider.
func newSpamServer(t *testing.T, set spam.Settings, v spam.Verifier) (*Server, *fakeFormRepo) {
	t.Helper()
	srv, repo := newFormPostServer(t)
	sk := spam.SecretKey("test-secret")
	if set.Provider != spam.ProviderNone {
		sealed, err := spam.SealSecret(sk, "provider-secret")
		if err != nil {
			t.Fatal(err)
		}
		set.SecretSealed = sealed
	}
	srv.spam = &spam.Pipeline{Settings: fixedSettings{set}, Verifier: v, SecretKey: sk,
		TimingKey: spam.TimingKey("test-secret"), Now: func() time.Time { return spamNow }}
	return srv, repo
}

func captchaSettings() spam.Settings {
	s := spam.Defaults()
	s.Provider = spam.ProviderTurnstile
	s.SiteKey = "site"
	return s
}

func requireCaptcha(repo *fakeFormRepo) { repo.forms["contact"].Settings.EnableCaptcha = true }

func postJSON(srv *Server, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/contact/submit", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", "contact")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	return rec
}

func tokenAgo(srv *Server, d time.Duration) string {
	return spam.IssueToken(srv.spam.TimingKey, "contact", spamNow.Add(-d))
}

func TestSpamCaptchaJSON(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		v     *fakeVerifier
		token any
		want  int
		store int
	}{
		{"no token", &fakeVerifier{res: spam.Result{OK: true}}, nil, 422, 0},
		{"invalid token", &fakeVerifier{res: spam.Result{OK: false}}, "bad", 422, 0},
		{"verifier down", &fakeVerifier{err: errors.New("timeout")}, "tok", 422, 0},
		{"valid token", &fakeVerifier{res: spam.Result{OK: true}}, "tok", 201, 1},
	} {
		srv, repo := newSpamServer(t, captchaSettings(), c.v)
		requireCaptcha(repo)
		body := map[string]any{"name": "Ada"}
		if c.token != nil {
			body["hm_captcha"] = c.token
		}
		rec := postJSON(srv, body)
		if rec.Code != c.want || len(repo.subs) != c.store {
			t.Errorf("%s: code %d stored %d (%s)", c.name, rec.Code, len(repo.subs), rec.Body)
		}
		if c.want == 422 && (!strings.Contains(rec.Body.String(), "Please complete the spam check and try again.") || !strings.Contains(rec.Body.String(), `"code":"captcha_required"`)) {
			t.Errorf("%s: body %s", c.name, rec.Body)
		}
		if c.want == 201 && repo.subs[0].Status != form.SubmissionStatusNew {
			t.Errorf("%s: status %q", c.name, repo.subs[0].Status)
		}
	}
}

func TestSpamCaptchaNativePost(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, captchaSettings(), &fakeVerifier{res: spam.Result{OK: true}})
	requireCaptcha(repo)
	rec := postForm(srv, "contact", url.Values{"name": {"Ada"}, "_redirect": {"/c"}})
	if rec.Header().Get("Location") != "/c?form_error=1" || len(repo.subs) != 0 {
		t.Fatalf("no token: %q stored %d", rec.Header().Get("Location"), len(repo.subs))
	}
	rec = postForm(srv, "contact", url.Values{"name": {"Ada"}, "_redirect": {"/c"}, "cf-turnstile-response": {"tok"}})
	if rec.Header().Get("Location") != "/c?submitted=1" || len(repo.subs) != 1 {
		t.Fatalf("provider field: %q stored %d", rec.Header().Get("Location"), len(repo.subs))
	}
}

func TestSpamNoKeysAccepts(t *testing.T) {
	t.Parallel()
	// Switch on, provider none.
	srv, repo := newSpamServer(t, spam.Defaults(), &fakeVerifier{})
	requireCaptcha(repo)
	if rec := postJSON(srv, map[string]any{"name": "Ada"}); rec.Code != 201 || len(repo.subs) != 1 {
		t.Fatalf("provider none: %d stored %d", rec.Code, len(repo.subs))
	}
	// Switch on, sealed secret no longer readable.
	srv, repo = newSpamServer(t, captchaSettings(), &fakeVerifier{})
	requireCaptcha(repo)
	srv.spam.SecretKey = spam.SecretKey("rotated")
	if rec := postJSON(srv, map[string]any{"name": "Ada"}); rec.Code != 201 || len(repo.subs) != 1 {
		t.Fatalf("unreadable secret: %d stored %d", rec.Code, len(repo.subs))
	}
}

// countHook counts FormSubmitted events the server fires.
func countHook(t *testing.T, srv *Server) *int {
	t.Helper()
	srv.hooks = hooks.New(srv.logger)
	n := new(int)
	srv.hooks.AddAction("form.submitted", 10, func(context.Context, any) { *n++ })
	return n
}

func TestSpamTimingStoresSpamWithoutSideEffects(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	fired := countHook(t, srv)
	sender := newFakeConfirmationSender()
	srv.formConfirmationSender = sender
	repo.forms["contact"].Fields = append(repo.forms["contact"].Fields,
		form.FormField{Name: "work_email", Label: "Email", Type: form.FieldTypeEmail})
	repo.forms["contact"].Settings.ConfirmationEmail = true

	rec := postJSON(srv, map[string]any{"name": "Ada", "work_email": "ada@example.com", "hm_ts": tokenAgo(srv, time.Second)})
	if rec.Code != 201 || len(repo.subs) != 1 || repo.subs[0].Status != form.SubmissionStatusSpam {
		t.Fatalf("fast submit: %d %v", rec.Code, repo.subs)
	}
	srv.hooks.Wait()
	select {
	case <-sender.calls:
		t.Error("confirmation sent for spam")
	case <-time.After(100 * time.Millisecond):
	}
	if *fired != 0 {
		t.Errorf("FormSubmitted fired %d times for spam", *fired)
	}

	// A slow submit of the same data is a normal lead with all side effects.
	rec = postJSON(srv, map[string]any{"name": "Ada", "work_email": "ada@example.com", "hm_ts": tokenAgo(srv, 10*time.Second)})
	if rec.Code != 201 || repo.subs[1].Status != form.SubmissionStatusNew {
		t.Fatalf("slow submit: %d %v", rec.Code, repo.subs[1])
	}
	select {
	case <-sender.calls:
	case <-time.After(2 * time.Second):
		t.Error("confirmation not sent for a normal submission")
	}
	srv.hooks.Wait()
	if *fired != 1 {
		t.Errorf("FormSubmitted fired %d times, want 1", *fired)
	}
}

func TestSpamMissingOrForgedTimingIsStoredNew(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, spam.Defaults(), nil)
	postJSON(srv, map[string]any{"name": "Ada"})
	postJSON(srv, map[string]any{"name": "Ada", "hm_ts": "v1." + "1." + "forged"})
	postJSON(srv, map[string]any{"name": "Ada", "hm_ts": spam.IssueToken(srv.spam.TimingKey, "contact", spamNow.Add(-48*time.Hour))})
	if len(repo.subs) != 3 {
		t.Fatalf("stored %d", len(repo.subs))
	}
	for i, s := range repo.subs {
		if s.Status != form.SubmissionStatusNew {
			t.Errorf("sub %d status %q", i, s.Status)
		}
	}
}

func TestSpamRulesAndRefuse(t *testing.T) {
	t.Parallel()
	set := spam.Defaults()
	set.BlockedWords = []string{"Crypto"}
	set.BlockedScripts = []string{"cyrillic"}
	for name, body := range map[string]map[string]any{
		"links":  {"name": "Ada", "topic": "sales", "email": "a@b.co", "tags": []any{"a"}, "message": "x"},
		"word":   {"name": "Buy CRYPTO now"},
		"script": {"name": "Привет"},
	} {
		if name == "links" {
			body["name"] = "http://a.io http://b.io http://c.io"
		}
		srv, repo := newSpamServer(t, set, nil)
		if rec := postJSON(srv, body); rec.Code != 201 || len(repo.subs) != 1 || repo.subs[0].Status != form.SubmissionStatusSpam {
			t.Errorf("%s spam mode: %d %v", name, rec.Code, repo.subs)
		}
		refuse := set
		refuse.Action = spam.ActionRefuse
		srv, repo = newSpamServer(t, refuse, nil)
		rec := postJSON(srv, body)
		if rec.Code != 422 || len(repo.subs) != 0 || !strings.Contains(rec.Body.String(), "Your message could not be accepted.") || !strings.Contains(rec.Body.String(), `"code":"refused"`) || strings.Contains(rec.Body.String(), "captcha_required") {
			t.Errorf("%s refuse mode: %d %s stored %d", name, rec.Code, rec.Body, len(repo.subs))
		}
	}
}

func TestSpamControlFieldsNeverReachDataOrRules(t *testing.T) {
	t.Parallel()
	set := spam.Defaults()
	set.MaxLinks = 0 // any link in submitted data would trip
	srv, repo := newSpamServer(t, set, nil)
	rec := postJSON(srv, map[string]any{"name": "Ada", "page_url": "https://example.com/c",
		"hm_ts": "x", "hm_captcha": "https://tok", "cf-turnstile-response": "t", "g-recaptcha-response": "g", "h-captcha-response": "h"})
	if rec.Code != 201 || len(repo.subs) != 1 || repo.subs[0].Status != form.SubmissionStatusNew {
		t.Fatalf("json: %d %v", rec.Code, repo.subs)
	}
	postForm(srv, "contact", url.Values{"name": {"Ada"}, "_redirect": {"/c"}, "page_url": {"https://example.com/c"},
		"hm_ts": {"x"}, "hm_captcha": {"https://tok"}, "cf-turnstile-response": {"t"}, "g-recaptcha-response": {"g"}, "h-captcha-response": {"h"}})
	if len(repo.subs) != 2 || repo.subs[1].Status != form.SubmissionStatusNew {
		t.Fatalf("native: %v", repo.subs)
	}
	for i, sub := range repo.subs {
		for k := range sub.Data {
			if isControlField(k) {
				t.Errorf("sub %d stored control field %q", i, k)
			}
		}
	}
	if got := formValues(url.Values{"hm_ts": {"x"}, "name": {"a"}}); len(got) != 1 {
		t.Errorf("formValues kept a control field: %v", got)
	}
}

func TestSpamHoneypotUnchanged(t *testing.T) {
	t.Parallel()
	srv, repo := newSpamServer(t, captchaSettings(), &fakeVerifier{})
	requireCaptcha(repo)
	rec := postJSON(srv, map[string]any{"name": "bot", "hm_website": "http://spam"})
	if rec.Code != 201 || len(repo.subs) != 0 {
		t.Fatalf("honeypot: %d stored %d", rec.Code, len(repo.subs))
	}
}

func TestFormTokenEndpoint(t *testing.T) {
	t.Parallel()
	srv, _ := newSpamServer(t, spam.Defaults(), nil)
	get := func(slug string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/forms/"+slug+"/token", nil)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		srv.handleFormToken(rec, req)
		return rec
	}
	rec := get("contact")
	var out struct{ Token string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if _, ok := spam.CheckToken(srv.spam.TimingKey, "contact", out.Token, spamNow); !ok {
		t.Error("issued token does not verify")
	}
	for _, slug := range []string{"missing", "draft"} {
		if rec := get(slug); rec.Code != 404 {
			t.Errorf("%s: %d", slug, rec.Code)
		}
	}
}

func TestFormsForSectionsCaptchaWidget(t *testing.T) {
	t.Parallel()
	sections := []render.Section{{Type: "contact_form", Content: map[string]any{"form_slug": "contact"}}}
	build := func(set spam.Settings, key []byte, switchOn bool) render.FormView {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		repo := slugFormRepo{forms: map[string]*form.Form{
			"contact": {Slug: "contact", Status: form.StatusActive, Settings: form.FormSettings{EnableCaptcha: switchOn}},
		}}
		srv := &Server{logger: logger, formService: form.NewService(repo, logger),
			spam: &spam.Pipeline{Settings: fixedSettings{set}, SecretKey: key}}
		return srv.formsForSections(context.Background(), sections)["contact"]
	}
	key := spam.SecretKey("test-secret")
	sealed, err := spam.SealSecret(key, "provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	ready := spam.Settings{Provider: spam.ProviderHCaptcha, SiteKey: "site-1", SecretSealed: sealed}

	got := build(ready, key, true)
	if !got.Captcha || got.CaptchaProvider != "hcaptcha" || got.CaptchaSiteKey != "site-1" {
		t.Errorf("ready: %+v", got)
	}
	for name, v := range map[string]render.FormView{
		"switch off":      build(ready, key, false),
		"unreadable":      build(ready, spam.SecretKey("rotated"), true),
		"no provider":     build(spam.Settings{Provider: spam.ProviderNone}, key, true),
		"no secret saved": build(spam.Settings{Provider: spam.ProviderHCaptcha, SiteKey: "s"}, key, true),
	} {
		if v.Captcha || v.CaptchaProvider != "" || v.CaptchaSiteKey != "" {
			t.Errorf("%s: widget requested: %+v", name, v)
		}
	}
}

func TestSpamRemoteIPIgnoresSpoofedForwardedFor(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{res: spam.Result{OK: true}}
	srv, repo := newSpamServer(t, captchaSettings(), v)
	requireCaptcha(repo)
	b, _ := json.Marshal(map[string]any{"name": "Ada", "hm_captcha": "tok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/contact/submit", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	req.RemoteAddr = "198.51.100.7:4444"
	req.SetPathValue("slug", "contact")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	if rec.Code != 201 || len(v.ips) != 1 {
		t.Fatalf("code %d ips %v", rec.Code, v.ips)
	}
	if v.ips[0] != "198.51.100.7" {
		t.Errorf("remoteip sent to provider = %q, want the peer address", v.ips[0])
	}
}
