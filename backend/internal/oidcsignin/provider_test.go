package oidcsignin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/oidcsignin/oidctest"
)

const (
	testClientID = "client-1"
	testSecret   = "s3cret-value"
	testCallback = "https://site.example.test/admin/login/provider/oidc/callback"
	testState    = "state-1"
)

var testKey = SecretKey("server-secret")

type fakeSource struct {
	s     Settings
	found bool
	err   error
}

func (f *fakeSource) Get(context.Context) (Settings, bool, error) { return f.s, f.found, f.err }

// rig is a provider wired to a fake IdP and in-memory settings.
type rig struct {
	idp *oidctest.Server
	src *fakeSource
	p   *Provider
}

func newRig(t *testing.T) *rig {
	t.Helper()
	idp := oidctest.New(t, testClientID, testSecret)
	sealed, err := SealSecret(testKey, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{found: true, s: Settings{
		Enabled: true, Label: "Acme SSO", Issuer: idp.Issuer(), ClientID: testClientID,
		SecretSealed: sealed, AllowedDomains: []string{"example.com"}, DefaultRole: "editor",
	}}
	p := &Provider{}
	p.SetRuntime(Runtime{Settings: src, SecretKey: testKey, Client: idp.Client()})
	return &rig{idp: idp, src: src, p: p}
}

// signIn runs Start, the IdP's authorize step and returns the Finish request.
func (r *rig) start(t *testing.T) (authstate.StartResult, authstate.FinishRequest) {
	t.Helper()
	res, err := r.p.Start(context.Background(), authstate.StartRequest{CallbackURL: testCallback, State: testState})
	if err != nil {
		t.Fatal(err)
	}
	c := *r.idp.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(res.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return res, authstate.FinishRequest{CallbackURL: testCallback, State: testState, Data: res.Data, Query: loc.Query()}
}

func TestAvailable(t *testing.T) {
	ctx := context.Background()
	if _, ok := (&Provider{}).Available(ctx); ok {
		t.Fatal("available without a runtime")
	}
	r := newRig(t)
	if label, ok := r.p.Available(ctx); !ok || label != "Acme SSO" {
		t.Fatalf("Available = %q, %v", label, ok)
	}
	for name, mut := range map[string]func(*fakeSource){
		"disabled":     func(f *fakeSource) { f.s.Enabled = false },
		"no secret":    func(f *fakeSource) { f.s.SecretSealed = nil },
		"no row":       func(f *fakeSource) { f.found = false },
		"store error":  func(f *fakeSource) { f.err = errors.New("db down") },
		"other secret": func(f *fakeSource) { f.s.SecretSealed, _ = SealSecret(SecretKey("other"), "x") },
		"invalid":      func(f *fakeSource) { f.s.AllowedDomains = nil },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			mut(r.src)
			if _, ok := r.p.Available(ctx); ok {
				t.Fatal("available")
			}
			if _, err := r.p.Start(ctx, authstate.StartRequest{}); ReasonOf(err) != reasonDisabled {
				t.Fatalf("Start = %v, want disabled", err)
			}
		})
	}
}

func TestAvailableDefaultLabel(t *testing.T) {
	r := newRig(t)
	r.src.s.Label = ""
	if label, ok := r.p.Available(context.Background()); !ok || label != DefaultLabel {
		t.Fatalf("Available = %q, %v", label, ok)
	}
}

func TestAvailableCachesSettings(t *testing.T) {
	r := newRig(t)
	if _, ok := r.p.Available(context.Background()); !ok {
		t.Fatal("not available")
	}
	r.src.s.Enabled = false
	if _, ok := r.p.Available(context.Background()); !ok {
		t.Fatal("cache not used within its TTL")
	}
	r.p.Invalidate()
	if _, ok := r.p.Available(context.Background()); ok {
		t.Fatal("still available after Invalidate")
	}
}

func TestStart(t *testing.T) {
	r := newRig(t)
	res, _ := r.start(t)
	u, err := url.Parse(res.RedirectURL)
	if err != nil || !strings.HasPrefix(res.RedirectURL, r.idp.Issuer()+"/authorize?") {
		t.Fatalf("redirect = %q, %v", res.RedirectURL, err)
	}
	q := r.idp.LastAuth
	if u.Query().Get("state") != testState || q.Get("state") != testState {
		t.Error("state missing")
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Errorf("PKCE = %q %q", q.Get("code_challenge_method"), q.Get("code_challenge"))
	}
	if q.Get("nonce") == "" || q.Get("redirect_uri") != testCallback || q.Get("client_id") != testClientID {
		t.Errorf("query = %v", q)
	}
	if q.Get("scope") != "openid email profile" || q.Get("response_type") != "code" || q.Has("response_mode") {
		t.Errorf("scope/response = %v", q)
	}
	if len(res.Data) >= 400 {
		t.Errorf("data is %d bytes", len(res.Data))
	}
	if strings.Contains(res.RedirectURL, testSecret) || strings.Contains(res.Data, testSecret) {
		t.Error("client secret leaked into the redirect or data")
	}
}

func TestFinishHappy(t *testing.T) {
	r := newRig(t)
	r.idp.Claims = func(c map[string]any) map[string]any {
		c["email"] = "  Ana@Example.COM "
		c["email_verified"] = "true"
		c["name"] = " Ana Perez "
		return c
	}
	_, fin := r.start(t)
	id, err := r.p.Finish(context.Background(), fin)
	if err != nil {
		t.Fatal(err)
	}
	want := authstate.Identity{Issuer: r.idp.Issuer(), Subject: "sub-1", Email: "ana@example.com", EmailVerified: true, Name: "Ana Perez"}
	if id != want {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
	if r.idp.LastToken.Get("code_verifier") == "" {
		t.Error("no PKCE verifier sent to the token endpoint")
	}
}

func TestFinishEmailVerifiedForms(t *testing.T) {
	for name, claims := range map[string]map[string]any{
		"bool":      {"email_verified": true},
		"string":    {"email_verified": "true"},
		"entra":     {"email_verified": nil, "xms_edov": true},
		"false":     {"email_verified": false},
		"missing":   {"email_verified": nil},
		"not true":  {"email_verified": "yes"},
		"entra off": {"email_verified": nil, "xms_edov": false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.idp.Claims = func(c map[string]any) map[string]any {
				for k, v := range claims {
					if v == nil {
						delete(c, k)
					} else {
						c[k] = v
					}
				}
				return c
			}
			_, fin := r.start(t)
			id, err := r.p.Finish(context.Background(), fin)
			if err != nil {
				t.Fatal(err)
			}
			want := name == "bool" || name == "string" || name == "entra"
			if id.EmailVerified != want {
				t.Fatalf("EmailVerified = %v, want %v", id.EmailVerified, want)
			}
		})
	}
}

// A key rotation at the IdP must not break the next sign-in, although the
// context of the first request is long gone.
func TestFinishAfterKeyRotation(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, fin := r.start(t)
	if _, err := r.p.Finish(ctx, fin); err != nil {
		t.Fatal(err)
	}
	cancel()
	r.idp.RotateKey()
	_, fin = r.start(t)
	if _, err := r.p.Finish(context.Background(), fin); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
}

func TestCheck(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.p.Check(ctx, r.src.s); err != nil {
		t.Fatal(err)
	}
	if r.idp.LastToken != nil {
		t.Error("Check called the token endpoint")
	}
	bad := r.src.s
	bad.Issuer = r.idp.Issuer() + "/" // discovery reports the issuer without the slash
	if err := r.p.Check(ctx, bad); err == nil {
		t.Error("issuer mismatch accepted")
	}
	bad.Issuer = "http://idp.example.test"
	if err := r.p.Check(ctx, bad); err == nil {
		t.Error("http issuer accepted")
	}
	if err := (&Provider{}).Check(ctx, r.src.s); err == nil {
		t.Error("Check without runtime succeeded")
	}
}

type ctxSource struct{ fakeSource }

func (c *ctxSource) Get(ctx context.Context) (Settings, bool, error) {
	if err := ctx.Err(); err != nil {
		return Settings{}, false, err
	}
	return c.fakeSource.Get(ctx)
}

func TestAvailableDoesNotCacheCancelledRead(t *testing.T) {
	r := newRig(t)
	src := &ctxSource{fakeSource: *r.src}
	r.p.SetRuntime(Runtime{Settings: src, SecretKey: testKey, Client: r.idp.Client()})
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := r.p.Available(gone); ok {
		t.Fatal("available on a cancelled context")
	}
	if _, ok := r.p.Available(context.Background()); !ok {
		t.Fatal("a cancelled read was cached for the next visitor")
	}
}

func TestGoogleHostedDomain(t *testing.T) {
	const g = "https://accounts.google.com"
	allowed := []string{"acme.com"}
	for _, tc := range []struct {
		name, issuer, hd, email string
		want                    bool
	}{
		{"workspace", g, "acme.com", "ana@acme.com", true},
		{"hd case", g, "ACME.com", "ana@acme.com", true},
		{"consumer account, no hd", g, "", "ana@acme.com", false},
		{"hd differs from email", g, "other.org", "ana@acme.com", false},
		{"hd not allowed", g, "other.org", "ana@other.org", false},
		{"hd allowed, email elsewhere", g, "acme.com", "ana@example.org", false},
		{"other issuer ignores hd", "https://idp.example.test", "", "ana@acme.com", true},
	} {
		if got := googleHostedDomainOK(tc.issuer, tc.hd, tc.email, allowed); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDiscoveryRefusesPlainHTTPEndpoints(t *testing.T) {
	for _, key := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri"} {
		t.Run(key, func(t *testing.T) {
			r := newRig(t)
			r.idp.Discovery = func(d map[string]any) map[string]any {
				d[key] = strings.Replace(d[key].(string), "https://", "http://", 1)
				return d
			}
			if _, err := r.p.Start(context.Background(), authstate.StartRequest{CallbackURL: testCallback, State: testState}); ReasonOf(err) != reasonDiscoveryFailed {
				t.Fatalf("Start = %v, want discovery_failed", err)
			}
			if err := r.p.Check(context.Background(), r.src.s); err == nil {
				t.Fatal("Check accepted a plain http endpoint")
			}
		})
	}
}

func TestNoOutboundClientFailsClosed(t *testing.T) {
	r := newRig(t)
	r.p.SetRuntime(Runtime{Settings: r.src, SecretKey: testKey})
	if _, err := r.p.Start(context.Background(), authstate.StartRequest{CallbackURL: testCallback, State: testState}); ReasonOf(err) != reasonDiscoveryFailed {
		t.Fatalf("Start = %v, want discovery_failed", err)
	}
	if err := r.p.Check(context.Background(), r.src.s); !errors.Is(err, errNoClient) {
		t.Fatalf("Check = %v, want errNoClient", err)
	}
}
