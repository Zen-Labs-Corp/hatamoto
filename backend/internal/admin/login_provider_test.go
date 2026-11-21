package admin

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/secretbox"
)

const providerSite = "https://site.example"

// providerRig is the two-step rig with one provider "example" wired in.
type providerRig struct {
	*twoStepRig
	fp       *fakeProviderSignIn
	finished int
	uid      uuid.UUID
	mfa      bool
	finErr   error
	lastReq  authstate.FinishRequest
}

func newProviderRig(t *testing.T, mutate ...func(*Deps)) *providerRig {
	t.Helper()
	g := newTwoStepRigWith(t, mutate...)
	pr := &providerRig{twoStepRig: g, uid: g.tf.uid}
	pr.fp = &fakeProviderSignIn{
		infos: []authstate.ProviderInfo{{ID: "example", Name: "Example IdP"}},
		start: func(id, callbackURL, state string) (string, string, error) {
			return "https://idp.example/auth?cb=" + url.QueryEscape(callbackURL) + "&state=" + state, "verifier-1", nil
		},
		finish: func(id string, req authstate.FinishRequest) (uuid.UUID, bool, error) {
			pr.finished++
			pr.lastReq = req
			return pr.uid, pr.mfa, pr.finErr
		},
	}
	g.a.deps.ProviderSignIn = pr.fp
	g.a.deps.ProviderKey = secretbox.DeriveKey("test-secret", "sign-in-provider-state")
	g.a.deps.BaseURL = func(*http.Request) string { return providerSite }
	// The routes are mounted once, on first use; Deps changes above happen before it.
	return pr
}

// nav is a cross-site top-level navigation, as the provider's redirect back is.
func (g *providerRig) nav(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

// start runs the start route and returns the state cookie and the state sent to the provider.
func (g *providerRig) start(t *testing.T, target string) (*http.Cookie, string) {
	t.Helper()
	rec := g.nav(target)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Host != "idp.example" {
		t.Fatalf("redirect: %q", rec.Header().Get("Location"))
	}
	c := cookieNamed(rec, "hm_prov")
	if c == nil {
		t.Fatal("no state cookie")
	}
	return c, loc.Query().Get("state")
}

const cbPath = "/admin/login/provider/example/callback"

func TestProviderStartRedirectsWithSealedLaxCookie(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	rec := g.nav("/admin/login/provider/example/start?next=%2Fadmin%2Fsettings")
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	wantCB := url.QueryEscape(providerSite + cbPath)
	if !strings.HasPrefix(loc, "https://idp.example/auth?cb="+wantCB+"&state=") {
		t.Fatalf("redirect: %s", loc)
	}
	c := cookieNamed(rec, "hm_prov")
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/admin/login/provider/example" || c.MaxAge != 600 {
		t.Fatalf("state cookie: %+v", c)
	}
	state := loc[strings.LastIndex(loc, "state=")+len("state="):]
	if strings.Contains(c.Value, state) || strings.Contains(c.Value, "verifier-1") {
		t.Fatal("the state cookie is not sealed")
	}
}

func TestProviderCallbackSignsIn(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.mfa = true
	c, state := g.start(t, "/admin/login/provider/example/start?next=%2Fadmin%2Fsettings%2Fusers")
	rec := g.nav(cbPath+"?code=abc&state="+url.QueryEscape(state), c)
	// Never a redirect: it would belong to the cross-site navigation, which
	// does not carry the SameSite=Strict session cookie. A same-origin page
	// moves on by itself instead.
	if rec.Code != 200 || rec.Header().Get("Location") != "" {
		t.Fatalf("callback: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `http-equiv="refresh" content="0;url=/admin/settings/users"`) || !strings.Contains(body, `href="/admin/settings/users"`) {
		t.Fatalf("no refresh or continue link in the page:\n%s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("the page needs no inline script")
	}
	if s := cookieNamed(rec, "hatamoto_session"); s == nil || s.Value != "owner-token" {
		t.Fatalf("session cookie: %+v", s)
	}
	if cc := cookieNamed(rec, "hm_prov"); cc == nil || cc.MaxAge >= 0 {
		t.Fatalf("state cookie not cleared: %+v", cc)
	}
	if want := (authstate.Via{Method: authstate.MethodProvider, Provider: "example"}); len(g.auth.vias) != 1 || g.auth.vias[0] != want {
		t.Fatalf("vias = %+v", g.auth.vias)
	}
	r := g.lastReq
	if r.State != state || r.Data != "verifier-1" || r.CallbackURL != providerSite+cbPath || r.Query.Get("code") != "abc" {
		t.Fatalf("finish request: %+v", r)
	}
}

func TestProviderCallbackRefusalsAreNeutral(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	c, state := g.start(t, "/admin/login/provider/example/start")
	good := cbPath + "?code=abc&state=" + url.QueryEscape(state)

	// A cookie for another provider id (sealed under another AAD) or tampered.
	tampered := &http.Cookie{Name: "hm_prov", Value: c.Value[:len(c.Value)-2] + "AA"}
	for name, tc := range map[string]struct {
		target string
		cookie *http.Cookie
	}{
		"wrong state":   {cbPath + "?code=abc&state=nope", c},
		"missing state": {cbPath + "?code=abc", c},
		"no cookie":     {good, nil},
		"tampered":      {good, tampered},
	} {
		var rec *httptest.ResponseRecorder
		if tc.cookie != nil {
			rec = g.nav(tc.target, tc.cookie)
		} else {
			rec = g.nav(tc.target)
		}
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), providerMessage) || cookieNamed(rec, "hatamoto_session") != nil {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if g.finished != 0 {
		t.Fatalf("Finish ran %d times for refused callbacks", g.finished)
	}
}

func TestProviderCallbackOtherProviderIDRefused(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.fp.infos = append(g.fp.infos, authstate.ProviderInfo{ID: "other", Name: "Other"})
	c, state := g.start(t, "/admin/login/provider/example/start")
	rec := g.nav("/admin/login/provider/other/callback?state="+url.QueryEscape(state), c)
	if rec.Code != 422 || cookieNamed(rec, "hatamoto_session") != nil || g.finished != 0 {
		t.Fatalf("cookie of another provider: %d (finished %d)", rec.Code, g.finished)
	}
}

func TestProviderCallbackStateIsSingleUse(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.mfa = true
	c, state := g.start(t, "/admin/login/provider/example/start")
	target := cbPath + "?code=abc&state=" + url.QueryEscape(state)
	if rec := g.nav(target, c); rec.Code != 200 {
		t.Fatalf("first use: %d", rec.Code)
	}
	if rec := g.nav(target, c); rec.Code != 422 || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("replay: %d", rec.Code)
	}
	if g.finished != 1 {
		t.Fatalf("Finish ran %d times", g.finished)
	}
}

func TestProviderCallbackExpiredStateRefused(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	st := providerState{P: "example", S: "s", E: time.Now().Add(-time.Second), I: "x"}
	raw, _ := jsonx.Marshal(st)
	sealed, err := secretbox.Seal(g.a.deps.ProviderKey, providerAAD("example"), raw)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Cookie{Name: "hm_prov", Value: base64.RawURLEncoding.EncodeToString(sealed)}
	if rec := g.nav(cbPath+"?state=s", c); rec.Code != 422 || g.finished != 0 {
		t.Fatalf("expired: %d", rec.Code)
	}
}

func TestProviderCallbackRefusedSessionIsNeutral(t *testing.T) {
	t.Parallel()

	for _, err := range []error{auth.ErrInvalidCredentials, auth.ErrAccountLocked} {
		g := newProviderRig(t)
		g.mfa = true
		g.auth.verifiedErr = err
		c, state := g.start(t, "/admin/login/provider/example/start")
		rec := g.nav(cbPath+"?state="+url.QueryEscape(state), c)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), providerMessage) || cookieNamed(rec, "hatamoto_session") != nil {
			t.Fatalf("%v: %d", err, rec.Code)
		}
	}
}

func TestProviderFinishErrorIsNeutral(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.finErr = auth.ErrInvalidCredentials
	c, state := g.start(t, "/admin/login/provider/example/start")
	rec := g.nav(cbPath+"?state="+url.QueryEscape(state), c)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), providerMessage) || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("finish error: %d", rec.Code)
	}
}

func TestProviderUserWithFactorGoesThroughSecondStepUnlessAsserted(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	c, state := g.start(t, "/admin/login/provider/example/start?next=%2Fadmin%2Fsettings%2Fusers")
	rec := g.nav(cbPath+"?state="+url.QueryEscape(state), c)
	if rec.Code != 200 || cookieNamed(rec, "hatamoto_session") != nil || cookieNamed(rec, "hm_2fa") == nil {
		t.Fatalf("factor user, no MFA assertion: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "code") {
		t.Fatal("the second-step page was not rendered")
	}
	// The code then completes it as a provider sign-in.
	done := g.second(cookieNamed(rec, "hm_2fa").Value, "123456", nil)
	if done.Code != 303 || len(g.auth.vias) != 1 || g.auth.vias[0].Provider != "example" {
		t.Fatalf("second step: %d %+v", done.Code, g.auth.vias)
	}

	// A provider that asserted its own second factor signs in directly.
	g2 := newProviderRig(t)
	g2.mfa = true
	c, state = g2.start(t, "/admin/login/provider/example/start")
	rec = g2.nav(cbPath+"?state="+url.QueryEscape(state), c)
	if rec.Code != 200 || rec.Header().Get("Location") != "" || cookieNamed(rec, "hatamoto_session") == nil || cookieNamed(rec, "hm_2fa") != nil {
		t.Fatalf("MFA asserted: %d", rec.Code)
	}
}

func TestProviderRoutes404(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	for _, target := range []string{"/admin/login/provider/nope/start", "/admin/login/provider/nope/callback"} {
		if rec := g.nav(target); rec.Code != 404 {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	none := newRig(t)
	for _, p := range []string{"/admin/login/provider/example/start", cbPath} {
		if rec := none.do("GET", p, "", nil, nil); rec.Code != 404 {
			t.Errorf("no provider registered, %s: %d", p, rec.Code)
		}
	}
}

func TestProviderStartWithoutSiteAddressFailsNeutrally(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	rec := g.nav("/admin/login/provider/example/start")
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), providerMessage) || cookieNamed(rec, "hm_prov") != nil {
		t.Fatalf("no site address: %d", rec.Code)
	}
}

func TestProviderStartRefusesBadProviderOutput(t *testing.T) {
	t.Parallel()

	g := newProviderRig(t)
	g.fp.start = func(_, _, _ string) (string, string, error) { return "javascript:alert(1)", "", nil }
	if rec := g.nav("/admin/login/provider/example/start"); rec.Code != 422 {
		t.Fatalf("bad redirect: %d", rec.Code)
	}
	g.fp.start = func(_, _, _ string) (string, string, error) {
		return "https://idp.example/", strings.Repeat("x", authstate.MaxDataLen+1), nil
	}
	if rec := g.nav("/admin/login/provider/example/start"); rec.Code != 422 {
		t.Fatalf("long data: %d", rec.Code)
	}
}

// Both routes sit in the rate-limited group.
func TestProviderRoutesAreRateLimited(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	g := newProviderRig(t, func(d *Deps) {
		d.RateLimit = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/login/provider/") {
					hits.Add(1)
				}
				next.ServeHTTP(w, r)
			})
		}
	})
	_ = g.nav("/admin/login/provider/example/start")
	_ = g.nav(cbPath)
	if hits.Load() != 2 {
		t.Fatalf("rate limiter saw %d provider requests, want 2", hits.Load())
	}
}
