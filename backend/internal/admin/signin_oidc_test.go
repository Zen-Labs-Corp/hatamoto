package admin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

const oidcTestSecret = "super-secret-value-123"

type fakeSignInSettings struct {
	view     SignInOIDCView
	saved    []SignInOIDCInput
	keeps    []bool
	checked  []string
	checkErr error
}

func (f *fakeSignInSettings) Get(context.Context) (SignInOIDCView, error) { return f.view, nil }

func (f *fakeSignInSettings) Save(_ context.Context, in SignInOIDCInput, keep bool) error {
	f.saved, f.keeps = append(f.saved, in), append(f.keeps, keep)
	return nil
}

func (f *fakeSignInSettings) Check(_ context.Context, issuer string) error {
	f.checked = append(f.checked, issuer)
	return f.checkErr
}

type oidcRig struct {
	*twoFactorSecRig
	svc    *fakeSignInSettings
	events []AuditEvent
}

func newOIDCRig(t *testing.T, view SignInOIDCView) *oidcRig {
	t.Helper()
	g := newUsersTwoFactorRig(t)
	r := &oidcRig{twoFactorSecRig: g, svc: &fakeSignInSettings{view: view}}
	g.a.deps.SignInSettings = r.svc
	g.a.deps.BaseURL = func(*http.Request) string { return providerSite }
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { r.events = append(r.events, e) }
	return r
}

func savedOIDCView() SignInOIDCView {
	return SignInOIDCView{
		SignInOIDCInput: SignInOIDCInput{
			Enabled: true, Label: "Acme SSO", Issuer: "https://login.example.com", ClientID: "client-1",
			Secret:  oidcTestSecret, // a service must never fill this; the card must not show it anyway
			Domains: []string{"acme.com", "example.org"}, DefaultRole: "editor",
		},
		SecretSaved: true,
	}
}

func oidcFlash(t *testing.T, h http.Header) string {
	t.Helper()
	for _, line := range h.Values("Set-Cookie") {
		if !strings.HasPrefix(line, flashCookie+"=") {
			continue
		}
		v := strings.SplitN(strings.TrimPrefix(line, flashCookie+"="), ";", 2)[0]
		raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(v, ".", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	t.Fatal("no flash set")
	return ""
}

func TestSignInOIDCCardRenders(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	rec := g.do("GET", "/admin/settings/users/sign-in", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Single sign-on (OIDC)", "Required for owners", // the policy card stays
		providerSite + "/admin/login/provider/oidc/callback",
		`value="Acme SSO"`, `value="https://login.example.com"`, `value="client-1"`, "acme.com, example.org",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, oidcTestSecret) {
		t.Error("the client secret is in the page")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/users-sign-in-oidc.html", uuidRE.ReplaceAllString(normalizePage(body), "UUID"))
}

func TestSignInOIDCCardWithoutSiteAddressAndLostSecret(t *testing.T) {
	t.Parallel()

	v := savedOIDCView()
	v.SecretLost = true
	g := newOIDCRig(t, v)
	g.a.deps.BaseURL = nil
	body := g.do("GET", "/admin/settings/users/sign-in", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Set the site address in Settings &gt; General first.", "can no longer be read"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `id="oidc-redirect"`) {
		t.Error("a redirect URL is shown without a site address")
	}
}

func TestSignInOIDCCardHiddenWithoutService(t *testing.T) {
	t.Parallel()

	g := newUsersTwoFactorRig(t)
	if body := g.do("GET", "/admin/settings/users/sign-in", "owner-token", nil, nil).Body.String(); strings.Contains(body, "oidc-card") {
		t.Error("the card shows with no service wired")
	}
}

func TestSignInOIDCOwnerOnly(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	g.grant()
	for _, p := range []string{"/admin/settings/users/sign-in/oidc", "/admin/settings/users/sign-in/oidc/test"} {
		if rec := g.do("POST", p, "editor-token", oidcForm(), nil); rec.Code != 403 {
			t.Errorf("editor POST %s: %d", p, rec.Code)
		}
	}
	if len(g.svc.saved)+len(g.svc.checked) != 0 {
		t.Fatal("an editor reached the service")
	}
}

func TestSignInOIDCSaveNeedsSudo(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	rec := g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm(), nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/sudo") || len(g.svc.saved) != 0 {
		t.Fatalf("without sudo: %d %s (saved %d)", rec.Code, rec.Header().Get("Location"), len(g.svc.saved))
	}
}

func TestSignInOIDCSave(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	g.grant()
	rec := g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm("secret", ""), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/users/sign-in" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(g.svc.saved) != 1 || !g.svc.keeps[0] || g.svc.saved[0].Issuer != "https://login.example.com" || g.svc.saved[0].Secret != "" {
		t.Fatalf("saved = %+v keeps %v", g.svc.saved, g.svc.keeps)
	}
	if !strings.Contains(oidcFlash(t, rec.Header()), "saved") {
		t.Error("no success flash")
	}
	if len(g.events) != 1 || g.events[0].Action != "signin.oidc.settings" {
		t.Fatalf("audit = %+v", g.events)
	}
	if strings.Contains(fmt.Sprint(g.events[0]), "s3cret") {
		t.Error("audit holds the secret")
	}
}

func TestSignInOIDCSaveTypedSecretIsNotKept(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	g.grant()
	g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm(), nil)
	if len(g.svc.saved) != 1 || g.svc.keeps[0] || g.svc.saved[0].Secret != "s3cret" {
		t.Fatalf("saved = %+v keeps %v", g.svc.saved, g.svc.keeps)
	}
	if len(g.events) != 1 || g.events[0].Detail["secret_changed"] != true || strings.Contains(fmt.Sprint(g.events[0]), "s3cret") {
		t.Fatalf("audit = %+v", g.events)
	}
}

func TestSignInOIDCInvalidSaveRerenders(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	g.grant()
	rec := g.do("POST", "/admin/settings/users/sign-in/oidc", "owner-token", oidcForm("issuer", "http://insecure.example.com", "secret", ""), nil)
	body := rec.Body.String()
	if rec.Code != 422 || len(g.svc.saved) != 0 {
		t.Fatalf("invalid save = %d (saved %d)", rec.Code, len(g.svc.saved))
	}
	for _, want := range []string{"Enter an https address", "Required for owners", `value="http://insecure.example.com"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if len(g.events) != 0 {
		t.Error("a refused save was audited")
	}
}

func TestSignInOIDCTest(t *testing.T) {
	t.Parallel()

	g := newOIDCRig(t, savedOIDCView())
	rec := g.do("POST", "/admin/settings/users/sign-in/oidc/test", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || len(g.svc.checked) != 1 || g.svc.checked[0] != "https://login.example.com" {
		t.Fatalf("test = %d, checked %v", rec.Code, g.svc.checked)
	}
	if f := oidcFlash(t, rec.Header()); !strings.Contains(f, "Continue with Acme SSO") || !strings.Contains(f, "success") {
		t.Errorf("flash = %s", f)
	}

	g.svc.checkErr = errors.New("dial tcp 10.0.0.5:443: refused")
	rec = g.do("POST", "/admin/settings/users/sign-in/oidc/test", "owner-token", url.Values{}, nil)
	f := oidcFlash(t, rec.Header())
	if rec.Code != 303 || !strings.Contains(f, "error") || !strings.Contains(f, "Could not reach") || strings.Contains(f, "10.0.0.5") {
		t.Errorf("failed check: %d %s", rec.Code, f)
	}
}
