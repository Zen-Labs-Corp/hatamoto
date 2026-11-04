package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/siteaccess"
)

type fakeSiteAccess struct {
	settings siteaccess.Settings
	saved    []siteaccess.Settings
}

func (f *fakeSiteAccess) Get(context.Context) (siteaccess.Settings, error) { return f.settings, nil }
func (f *fakeSiteAccess) Save(_ context.Context, s siteaccess.Settings) error {
	f.saved = append(f.saved, s)
	f.settings = s
	return nil
}

func newSiteAccessRig(t *testing.T, svc *fakeSiteAccess) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	p.a.deps.SiteAccess = svc
	return p
}

const siteAccessURL = "/admin/settings/site-access"

func TestSiteAccessPage(t *testing.T) {
	t.Parallel()

	svc := &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModePassword, RetryAfter: 21600, PasswordHash: "SECRET-HASH", Heading: "Private", Epoch: 2}}
	p := newSiteAccessRig(t, svc)
	rec := p.do("GET", siteAccessURL, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{"Coming soon", "Maintenance", `value="password" checked`, `<option value="21600" selected>6 hours</option>`, "A password is set", `value="Private"`, `autocomplete="new-password"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "SECRET-HASH") || strings.Contains(body, "value=\"secret") {
		t.Error("the hash is echoed")
	}
}

func TestSiteAccessOwnerOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModeOpen, RetryAfter: 3600}}
	p := newSiteAccessRig(t, svc)
	if rec := p.do("GET", siteAccessURL, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	form := url.Values{"mode": {"maintenance"}, "retry_after": {"3600"}}
	if rec := p.do("POST", siteAccessURL, "editor-token", form, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if rec := p.do("POST", siteAccessURL, "owner-token", form, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("no CSRF POST = %d", rec.Code)
	}
	if len(svc.saved) != 0 || len(p.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestSiteAccessValidation(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		stored siteaccess.Settings
		form   url.Values
		want   string
	}{
		"bad mode":           {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"nope"}, "retry_after": {"3600"}}, "Choose one of the four modes."},
		"bad retry":          {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"maintenance"}, "retry_after": {"5"}}, "Choose one of the listed times."},
		"password required":  {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"password"}, "retry_after": {"3600"}}, "Set a password to use password mode."},
		"password too short": {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"password"}, "retry_after": {"3600"}, "password": {"short"}}, "Use 8 to 128 characters."},
		"heading too long":   {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"open"}, "retry_after": {"3600"}, "heading": {strings.Repeat("a", 121)}}, "Use 120 characters or fewer."},
		"message too long":   {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"open"}, "retry_after": {"3600"}, "message": {strings.Repeat("a", 1001)}}, "Use 1000 characters or fewer."},
		"password too long":  {siteaccess.Settings{RetryAfter: 3600}, url.Values{"mode": {"open"}, "retry_after": {"3600"}, "password": {strings.Repeat("a", 129)}}, "Use 8 to 128 characters."},
	} {
		svc := &fakeSiteAccess{settings: c.stored}
		p := newSiteAccessRig(t, svc)
		rec := p.do("POST", siteAccessURL, "owner-token", c.form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d, want message %q", name, rec.Code, c.want)
		}
		if len(svc.saved) != 0 || len(p.events) != 0 {
			t.Errorf("%s: invalid form saved", name)
		}
	}
}

func TestSiteAccessSave(t *testing.T) {
	t.Parallel()

	svc := &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModeOpen, RetryAfter: 3600, Epoch: 4}}
	p := newSiteAccessRig(t, svc)
	form := url.Values{"mode": {"password"}, "retry_after": {"900"}, "password": {"correct horse"}, "heading": {" Members "}, "message": {"Ask us."}}
	rec := p.do("POST", siteAccessURL, "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != siteAccessURL || len(rec.Result().Cookies()) == 0 {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.saved) != 1 {
		t.Fatalf("saved = %+v", svc.saved)
	}
	got := svc.saved[0]
	if got.Mode != siteaccess.ModePassword || got.RetryAfter != 900 || got.Heading != "Members" || got.Message != "Ask us." || got.Epoch != 5 {
		t.Fatalf("saved = %+v", got)
	}
	if ok, err := auth.VerifyPasswordContext(context.Background(), "correct horse", got.PasswordHash); err != nil || !ok {
		t.Fatalf("stored hash does not verify: %v", err)
	}
	if len(p.events) != 1 || p.events[0].Action != "site-access.change" || p.events[0].Detail["password_changed"] != true || p.events[0].Detail["new_mode"] != "password" || p.events[0].Detail["old_mode"] != "open" {
		t.Fatalf("audit = %+v", p.events)
	}
	for k, v := range p.events[0].Detail {
		if s, ok := v.(string); ok && (strings.Contains(s, "correct horse") || strings.Contains(s, got.PasswordHash)) {
			t.Errorf("audit detail %s leaks a secret", k)
		}
	}

	// A blank password keeps the hash and the epoch: switching mode does not sign visitors out.
	rec = p.do("POST", siteAccessURL, "owner-token", url.Values{"mode": {"maintenance"}, "retry_after": {"900"}}, nil)
	if rec.Code != 303 || len(svc.saved) != 2 {
		t.Fatalf("second save = %d", rec.Code)
	}
	if again := svc.saved[1]; again.PasswordHash != got.PasswordHash || again.Epoch != 5 || again.Mode != siteaccess.ModeMaintenance {
		t.Fatalf("second save = %+v", again)
	}
	if p.events[1].Detail["password_changed"] != false {
		t.Errorf("audit = %+v", p.events[1])
	}
}

func TestSiteAccessUnavailable(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	if rec := p.do("GET", siteAccessURL, "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET without service = %d", rec.Code)
	}
}
