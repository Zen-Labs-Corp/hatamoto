package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/comment/identity"
)

type fakeSignIn struct {
	enabled     bool
	peekErr     error
	confirmErr  error
	requestErr  error
	device      bool
	peeks       int
	confirms    int
	requests    []identity.SignInRequest
	clears      int
	confirmedID uuid.UUID
}

func (f *fakeSignIn) Enabled(context.Context) bool { return f.enabled }
func (f *fakeSignIn) RequestSignIn(_ context.Context, r identity.SignInRequest) (identity.Outcome, error) {
	f.requests = append(f.requests, r)
	return identity.OutcomeCheckEmail, f.requestErr
}
func (f *fakeSignIn) PeekSignIn(context.Context, string) error { f.peeks++; return f.peekErr }
func (f *fakeSignIn) ConfirmSignIn(context.Context, string) (identity.Confirmed, error) {
	f.confirms++
	return identity.Confirmed{CommenterID: f.confirmedID, CookieLifetime: 24 * time.Hour}, f.confirmErr
}
func (f *fakeSignIn) Cookie(c identity.Confirmed, secure bool) *http.Cookie {
	return &http.Cookie{Name: "hm_c", Value: "v", Path: "/", MaxAge: int(c.CookieLifetime / time.Second), Secure: secure}
}
func (f *fakeSignIn) ClearCookie(secure bool) *http.Cookie {
	f.clears++
	return &http.Cookie{Name: "hm_c", Path: "/", MaxAge: -1, Secure: secure}
}
func (f *fakeSignIn) Device(context.Context, string) (uuid.UUID, bool) {
	return uuid.New(), f.device
}

// onceTokens is a spam.TokenStore that accepts each token id once.
type onceTokens struct{ seen map[string]bool }

func (o *onceTokens) ConsumeFormToken(_ context.Context, id string, _ time.Time) (bool, error) {
	if o.seen[id] {
		return false, nil
	}
	o.seen[id] = true
	return true, nil
}

type signInRig struct {
	s *Server
	f *fakeSignIn
	h http.HandlerFunc
}

func newSignInRig() *signInRig {
	f := &fakeSignIn{enabled: true, confirmedID: uuid.New()}
	d := commentSignInDeps{
		tokenKey: []byte("0123456789abcdef0123456789abcdef"), tokens: &onceTokens{seen: map[string]bool{}},
		hashIP: func(string) (string, string) { return "ip", "net" }, now: time.Now,
	}
	s := &Server{}
	return &signInRig{s: s, f: f, h: s.commentSignInHandler(f, d)}
}

func (g *signInRig) serve(method, target string, hdr map[string]string, form url.Values) *httptest.ResponseRecorder {
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, "http://site.test"+target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, "http://site.test"+target, nil)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, commentSignInPrefix); ok {
		r.SetPathValue("token", rest)
	}
	w := httptest.NewRecorder()
	g.h(w, r)
	return w
}

var withCookie = map[string]string{"Cookie": "hm_c=v"}

var sameOrigin = map[string]string{"Sec-Fetch-Site": "same-origin"}

// formToken reads the hm_ct value a rendered page carries.
func formToken(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, `name="hm_ct" value="`)
	if !ok {
		t.Fatalf("no hm_ct in %s", body)
	}
	tok, _, _ := strings.Cut(rest, `"`)
	return tok
}

func TestSignInOffIs404(t *testing.T) {
	g := newSignInRig()
	g.f.enabled = false
	for _, c := range []struct{ m, p string }{
		{"GET", commentSignInPath}, {"POST", commentSignInPath}, {"GET", commentSignInPrefix + "tok"},
		{"POST", commentSignInPrefix + "tok"}, {"POST", commentSignOutPath},
	} {
		if w := g.serve(c.m, c.p, sameOrigin, url.Values{}); w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c.m, c.p, w.Code)
		}
	}
	nilSvc := (&Server{}).commentSignInHandler(nil, commentSignInDeps{})
	w := httptest.NewRecorder()
	nilSvc(w, httptest.NewRequest("GET", commentSignInPath, nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("nil service: %d", w.Code)
	}
	if len(g.f.requests) != 0 || g.f.confirms != 0 {
		t.Error("handler acted while off")
	}
}

func TestSignInLinkGetDoesNotConsume(t *testing.T) {
	g := newSignInRig()
	w := g.serve("GET", commentSignInPrefix+"tok", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="`+commentSignInPrefix+`tok"`) {
		t.Fatalf("GET link: %d %s", w.Code, w.Body.String())
	}
	if g.f.confirms != 0 || len(w.Result().Cookies()) != 0 {
		t.Error("GET consumed the link or set a cookie")
	}
	for k, want := range map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex", "Referrer-Policy": "no-referrer"} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q", k, got)
		}
	}
	g.f.peekErr = identity.ErrTokenInvalid
	if w := g.serve("GET", commentSignInPrefix+"tok", nil, nil); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("invalid link: %d %s", w.Code, w.Body.String())
	}
}

func TestSignInLinkPostSetsCookie(t *testing.T) {
	g := newSignInRig()
	w := g.serve("POST", commentSignInPrefix+"tok", sameOrigin, url.Values{})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != commentSignInPath {
		t.Fatalf("POST link: %d Location %q", w.Code, w.Header().Get("Location"))
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "hm_c" || cs[0].MaxAge <= 0 {
		t.Errorf("cookies = %v", cs)
	}
	g.f.confirmErr = identity.ErrTokenInvalid
	w = g.serve("POST", commentSignInPrefix+"tok", sameOrigin, url.Values{})
	if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
		t.Errorf("replayed link: %d cookies %v", w.Code, w.Result().Cookies())
	}
	g.f.confirmErr = errors.New("boom")
	if w := g.serve("POST", commentSignInPrefix+"tok", sameOrigin, url.Values{}); w.Code != http.StatusInternalServerError {
		t.Errorf("other error: %d", w.Code)
	}
}

func TestSignInCrossSitePostsRefused(t *testing.T) {
	for _, hdr := range []map[string]string{
		{}, {"Sec-Fetch-Site": "cross-site"}, {"Sec-Fetch-Site": "same-origin", "Origin": "https://other.test"},
	} {
		g := newSignInRig()
		for _, p := range []string{commentSignInPath, commentSignInPrefix + "tok", commentSignOutPath} {
			if w := g.serve("POST", p, hdr, url.Values{"email": {"a@b.test"}}); w.Code != http.StatusForbidden {
				t.Errorf("%v POST %s: %d, want 403", hdr, p, w.Code)
			}
		}
		if len(g.f.requests) != 0 || g.f.confirms != 0 || g.f.clears != 0 {
			t.Errorf("%v: handler acted on a cross-site POST", hdr)
		}
	}
}

func TestSignInPostNeedsSingleUseToken(t *testing.T) {
	g := newSignInRig()
	form := func(tok string) url.Values { return url.Values{"email": {"a@b.test"}, "hm_ct": {tok}} }

	// Missing token: the form again, with a fresh token, nothing requested.
	w := g.serve("POST", commentSignInPath, sameOrigin, form(""))
	if w.Code != http.StatusOK || len(g.f.requests) != 0 || formToken(t, w.Body.String()) == "" {
		t.Fatalf("missing token: %d requests %d", w.Code, len(g.f.requests))
	}
	tok := formToken(t, g.serve("GET", commentSignInPath, nil, nil).Body.String())
	if w := g.serve("POST", commentSignInPath, sameOrigin, form(tok)); !strings.Contains(w.Body.String(), "email") || len(g.f.requests) != 1 {
		t.Fatalf("valid token: %d requests %d", w.Code, len(g.f.requests))
	}
	g.serve("POST", commentSignInPath, sameOrigin, form(tok)) // replay
	if len(g.f.requests) != 1 {
		t.Errorf("replayed token requested a mail: %d", len(g.f.requests))
	}
	if got := g.f.requests[0]; got.Email != "a@b.test" || got.IPHash != "ip" || got.NetHash != "net" {
		t.Errorf("request = %+v", got)
	}

	// Sign-out: a sign-in token does not work, a sign-out token does, once.
	if g.serve("POST", commentSignOutPath, sameOrigin, url.Values{"hm_ct": {tok}}); g.f.clears != 0 {
		t.Error("a sign-in token signed out")
	}
	g.f.device = true
	out := formToken(t, g.serve("GET", commentSignInPath, withCookie, nil).Body.String())
	w = g.serve("POST", commentSignOutPath, sameOrigin, url.Values{"hm_ct": {out}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != commentSignInPath+"?out=1" || g.f.clears != 1 {
		t.Fatalf("sign-out: %d %q clears %d", w.Code, w.Header().Get("Location"), g.f.clears)
	}
	g.serve("POST", commentSignOutPath, sameOrigin, url.Values{"hm_ct": {out}})
	if g.f.clears != 1 {
		t.Error("a replayed sign-out token cleared again")
	}
}

func TestSignInKnownAndUnknownAddressSameBody(t *testing.T) {
	post := func(g *signInRig) string {
		tok := formToken(t, g.serve("GET", commentSignInPath, nil, nil).Body.String())
		w := g.serve("POST", commentSignInPath, sameOrigin, url.Values{"email": {"a@b.test"}, "hm_ct": {tok}})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		return w.Body.String()
	}
	a, b := newSignInRig(), newSignInRig()
	if post(a) != post(b) || strings.Contains(post(a), "a@b.test") {
		t.Error("the sent page differs between runs or echoes the address")
	}
}

func TestSignInBadAddressShowsFormAgain(t *testing.T) {
	g := newSignInRig()
	g.f.requestErr = identity.ErrEmailInvalid
	tok := formToken(t, g.serve("GET", commentSignInPath, nil, nil).Body.String())
	w := g.serve("POST", commentSignInPath, sameOrigin, url.Values{"email": {"nope"}, "hm_ct": {tok}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `role="alert"`) {
		t.Errorf("bad address: %d %s", w.Code, w.Body.String())
	}
	if formToken(t, w.Body.String()) == tok {
		t.Error("the retry form reuses the spent token")
	}
}

func TestSignOutClearsCookieAndCallsNoRevoke(t *testing.T) {
	g := newSignInRig()
	g.f.device = true
	tok := formToken(t, g.serve("GET", commentSignInPath, withCookie, nil).Body.String())
	w := g.serve("POST", commentSignOutPath, sameOrigin, url.Values{"hm_ct": {tok}})
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != "hm_c" || cs[0].MaxAge >= 0 {
		t.Fatalf("cookies = %v", cs)
	}
	if g.f.confirms != 0 || len(g.f.requests) != 0 {
		t.Error("sign-out did something besides clearing the cookie")
	}
	if w := g.serve("GET", commentSignOutPath, nil, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET signout: %d", w.Code)
	}
	if w := g.serve("GET", commentSignInPath+"?out=1", nil, nil); !strings.Contains(w.Body.String(), "signed out") && w.Code != http.StatusOK {
		t.Errorf("out page: %d", w.Code)
	}
}

func TestSignInLinkTokenIsRedactedInLogs(t *testing.T) {
	if got := redactPreviewToken(commentSignInPrefix + "SECRET"); got != commentSignInPrefix+"<token>" {
		t.Errorf("redactPreviewToken = %q", got)
	}
	if got := redactPreviewToken(commentSignInPath); got != commentSignInPath {
		t.Errorf("the form path is not a secret: %q", got)
	}
}
