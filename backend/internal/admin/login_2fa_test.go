package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

// stepAuth is fakeAuth plus the two-step half: the user with a factor never
// gets a session from LoginSession.
type stepAuth struct {
	*fakeAuth
	gate *fakeTwoFactor
}

func (s stepAuth) CheckPassword(ctx context.Context, email, password string) (*user.User, error) {
	u, _, err := s.fakeAuth.LoginSession(ctx, email, password, "", "")
	return u, err
}

func (s stepAuth) Admit(_ context.Context, _ uuid.UUID, _ authstate.Via) error {
	s.fakeAuth.mu.Lock()
	defer s.fakeAuth.mu.Unlock()
	return s.fakeAuth.verifiedErr
}

func (s stepAuth) IssueVerifiedSession(ctx context.Context, id uuid.UUID, via authstate.Via, _, _ string) (string, error) {
	s.fakeAuth.mu.Lock()
	s.fakeAuth.vias = append(s.fakeAuth.vias, via)
	err := s.fakeAuth.verifiedErr
	s.fakeAuth.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.fakeAuth.IssueSession(ctx, id, "", "")
}

// fakeTwoFactor models twofactor.Service: an app code that works once, one
// recovery code that works once, and a lock after 3 wrong codes.
type fakeTwoFactor struct {
	mu        sync.Mutex
	uid       uuid.UUID
	passkeys  int
	hasErr    error
	totp      string
	recovery  string
	fails     int
	lockAfter int // wrong codes before the fake locks; 0 means 3
	verifies  int

	// Security page state: secure switches the fake from "has a factor" to the fields below.
	secure   bool
	totpOn   bool
	pending  bool
	keys     []twofactor.Passkey
	left     int
	confirm  string
	removedP int
}

func (f *fakeTwoFactor) HasAnyFactor(_ context.Context, id uuid.UUID) (bool, error) {
	if f.secure {
		return f.totpOn || len(f.keys) > 0, f.hasErr
	}
	return f.hasErr == nil && id == f.uid, f.hasErr
}

func (f *fakeTwoFactor) Status(context.Context, uuid.UUID) (twofactor.Factors, error) {
	if f.secure {
		return twofactor.Factors{TOTP: f.totpOn, Passkeys: len(f.keys), RecoveryLeft: f.left}, nil
	}
	return twofactor.Factors{TOTP: true, Passkeys: f.passkeys}, nil
}

func (f *fakeTwoFactor) Counts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]twofactor.Factors, error) {
	out := map[uuid.UUID]twofactor.Factors{}
	for _, id := range ids {
		if id == f.uid {
			out[id] = twofactor.Factors{TOTP: true, Passkeys: len(f.keys)}
		}
	}
	return out, nil
}

func (f *fakeTwoFactor) Passkeys(context.Context, uuid.UUID) ([]twofactor.Passkey, error) {
	return f.keys, nil
}

func (f *fakeTwoFactor) BeginTOTP(context.Context, uuid.UUID, string) (string, string, error) {
	if f.totpOn {
		return "", "", twofactor.ErrAlreadyEnrolled
	}
	f.pending = true
	return "JBSWY3DPEHPK3PXP", "otpauth://totp/Acme:olga@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Acme", nil
}

func (f *fakeTwoFactor) PendingSecret(ctx context.Context, uid uuid.UUID, a string) (string, string, error) {
	if !f.pending || f.totpOn {
		return "", "", twofactor.ErrNothingPending
	}
	return "JBSWY3DPEHPK3PXP", "otpauth://totp/Acme:olga@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Acme", nil
}

func (f *fakeTwoFactor) ConfirmTOTP(_ context.Context, _ uuid.UUID, code string) ([]string, error) {
	if !f.pending {
		return nil, twofactor.ErrNothingPending
	}
	if code != f.confirm {
		return nil, twofactor.ErrWrongCode
	}
	f.pending, f.totpOn, f.left = false, true, 10
	return []string{"AAAA-BBBB-CCCC", "DDDD-EEEE-FFFF"}, nil
}

func (f *fakeTwoFactor) RegenerateCodes(context.Context, uuid.UUID) ([]string, error) {
	f.left = 10
	return []string{"GGGG-HHHH-JJJJ"}, nil
}

func (f *fakeTwoFactor) RemoveTOTP(context.Context, uuid.UUID) error {
	f.totpOn = false
	return nil
}

func (f *fakeTwoFactor) RemovePasskey(_ context.Context, _, id uuid.UUID) (bool, error) {
	for i, k := range f.keys {
		if k.ID == id {
			f.keys = append(f.keys[:i:i], f.keys[i+1:]...)
			f.removedP++
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeTwoFactor) lockLimit() int {
	if f.lockAfter != 0 {
		return f.lockAfter
	}
	return 3
}

func (f *fakeTwoFactor) Verify(_ context.Context, id uuid.UUID, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifies++
	if id != f.uid || f.fails >= f.lockLimit() {
		return twofactor.ErrWrongCode
	}
	switch {
	case code != "" && code == f.totp:
		f.totp = ""
	case code != "" && code == f.recovery:
		f.recovery = ""
	default:
		f.fails++
		return twofactor.ErrWrongCode
	}
	f.fails = 0
	return nil
}

type twoStepRig struct {
	*authRig
	tf *fakeTwoFactor
}

func newTwoStepRig(t *testing.T) *twoStepRig { return newTwoStepRigWith(t) }

func newTwoStepRigWith(t *testing.T, mutate ...func(*Deps)) *twoStepRig {
	t.Helper()
	u := &user.User{ID: uuid.New(), Email: "olga@example.com", Role: user.RoleOwner}
	tf := &fakeTwoFactor{uid: u.ID, totp: "123456", recovery: "ABCDEFGHJKMN"}
	fa := &fakeAuth{users: map[string]*user.User{u.Email: u}, fails: map[string]int{}}
	ft := &fakeTokens{resets: map[string]*user.User{}, invites: map[string]*auth.Invite{}, user: u}
	g := newRig(t, func(d *Deps) {
		d.Auth, d.Tokens, d.Finder = stepAuth{fa, tf}, ft, fakeFinder{u}
		d.TwoFactor = tf
		d.Users = fakeUsers{u.ID: u}
		d.PendingKey = twofactor.PendingKeyFor("test-secret")
		for _, m := range mutate {
			m(d)
		}
	})
	return &twoStepRig{authRig: &authRig{rig: g, auth: fa, tokens: ft}, tf: tf}
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// withPending issues the pending cookie as the password step would and returns its value.
func (g *twoStepRig) password(t *testing.T) string {
	t.Helper()
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}, "next": {"/admin/settings/users"}}
	rec := g.do("POST", "/admin/login", "", form, nil)
	if rec.Code != 200 {
		t.Fatalf("password step: %d", rec.Code)
	}
	if c := cookieNamed(rec, "hatamoto_session"); c != nil {
		t.Fatalf("session issued before the second step: %+v", c)
	}
	c := cookieNamed(rec, "hm_2fa")
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/admin" || c.MaxAge != 300 {
		t.Fatalf("pending cookie: %+v", c)
	}
	return c.Value
}

func (g *twoStepRig) second(pending, code string, extra url.Values) *httptest.ResponseRecorder {
	form := url.Values{"code": {code}}
	for k, v := range extra {
		form[k] = v
	}
	return g.withPending("POST", "/admin/login/2fa", pending, form)
}

// withPending is rig.do for a signed-out browser that holds the pending cookie.
func (g *twoStepRig) withPending(method, target, pending string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if pending != "" {
		req.AddCookie(&http.Cookie{Name: "hm_2fa", Value: pending})
	}
	g.addFetchMeta(req, "", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func TestTwoStepPasswordThenCodeSignsIn(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	rec := g.second(p, "123456", nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/users" {
		t.Fatalf("code: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if c := cookieNamed(rec, "hatamoto_session"); c == nil || c.Value != "owner-token" {
		t.Fatalf("session cookie: %+v", c)
	}
	if c := cookieNamed(rec, "hm_2fa"); c == nil || c.MaxAge >= 0 {
		t.Fatalf("pending cookie not cleared: %+v", c)
	}
}

// A signed-in browser can be shown the sign-in form: the SameSite=Strict session
// cookie is withheld on a navigation from another site, so the form and the
// second step carry the pre-session token while the code is submitted with the
// session cookie back. The second step must check the code, not answer 403.
func TestTwoStepWhileSignedInChecksTheCode(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	g.realTokens()
	pre := g.a.newPreID(time.Now().Add(time.Hour))
	submit := func(pending, code string, withPre bool) *httptest.ResponseRecorder {
		cookie := "hatamoto_session=owner-token; hm_2fa=" + pending
		if withPre {
			cookie += "; hm_pre=" + pre
		}
		return g.browserForm("/admin/login/2fa", "", pre, url.Values{"code": {code}}, map[string]string{"Cookie": cookie})
	}

	p := g.password(t)
	if rec := submit(p, "000000", true); rec.Code != 422 || !strings.Contains(rec.Body.String(), loginMessage2FA) || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("wrong code while signed in: %d", rec.Code)
	}
	// The pre-session token is worth nothing without its cookie.
	if rec := submit(p, "123456", false); rec.Code != 403 || g.tf.verifies != 1 {
		t.Fatalf("pre-session token without hm_pre: %d (verifies %d)", rec.Code, g.tf.verifies)
	}
	rec := submit(p, "123456", true)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/users" {
		t.Fatalf("right code while signed in: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if c := cookieNamed(rec, "hatamoto_session"); c == nil || c.Value != "owner-token" {
		t.Fatalf("session cookie: %+v", c)
	}
}

func TestTwoStepPendingCookieIsSingleUse(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	if rec := g.second(p, "123456", nil); rec.Code != 303 {
		t.Fatalf("first use: %d", rec.Code)
	}
	// The same cookie, replayed after the sign-in finished, is no pending sign-in.
	rec := g.second(p, "123456", nil)
	if rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "/login") || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("replayed cookie: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTwoStepReplayedCodeAndSpentRecoveryCodeRefused(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	if rec := g.second(p, "123456", nil); rec.Code != 303 {
		t.Fatalf("first use: %d", rec.Code)
	}
	p = g.password(t)
	if rec := g.second(p, "123456", nil); rec.Code != 422 || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("replay: %d", rec.Code)
	}
	if rec := g.second(p, "ABCDEFGHJKMN", url.Values{"recovery": {"1"}}); rec.Code != 303 {
		t.Fatalf("recovery code: %d", rec.Code)
	}
	p = g.password(t)
	if rec := g.second(p, "ABCDEFGHJKMN", url.Values{"recovery": {"1"}}); rec.Code != 422 {
		t.Fatalf("spent recovery code: %d", rec.Code)
	}
}

func TestTwoStepWrongCodesLockAndStayNeutral(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	var wrong string
	for i := 0; i < 3; i++ {
		rec := g.second(p, "000000", nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), loginMessage2FA) {
			t.Fatalf("wrong code %d: %d", i, rec.Code)
		}
		wrong = rec.Body.String()
	}
	locked := g.second(p, "123456", nil)
	if locked.Code != 422 || cookieNamed(locked, "hatamoto_session") != nil {
		t.Fatalf("locked account accepted a right code: %d", locked.Code)
	}
	if normalizePage(locked.Body.String()) != normalizePage(wrong) {
		t.Error("a locked account answers differently from a wrong code")
	}
}

func TestTwoStepCapsWrongCodesPerPending(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	g.tf.lockAfter = 1000
	p := g.password(t)
	for i := 0; i < maxTwoStepAttempts; i++ {
		if rec := g.second(p, "000000", nil); rec.Code != 422 {
			t.Fatalf("wrong code %d: %d", i, rec.Code)
		}
	}
	before := g.tf.verifies
	rec := g.second(p, g.tf.totp, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("attempt over the cap: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g.tf.verifies != before {
		t.Error("an attempt over the cap reached Verify")
	}
	if rec := g.second(g.password(t), g.tf.totp, nil); rec.Code != 303 || cookieNamed(rec, "hatamoto_session") == nil {
		t.Fatalf("fresh pending sign-in: %d", rec.Code)
	}
}

func TestTwoStepPendingCookieIsRequiredAndTamperProof(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	forged := twofactor.PendingKeyFor("other-secret").Seal(twofactor.Pending{UserID: g.tf.uid, Expires: time.Now().Add(time.Minute), ID: "x"})
	for name, c := range map[string]string{"none": "", "tampered": p + "x", "wrong key": forged, "garbage": "a.b"} {
		rec := g.second(c, "123456", nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" || cookieNamed(rec, "hatamoto_session") != nil {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	if g.tf.verifies != 0 {
		t.Errorf("a bad pending cookie reached Verify %d times", g.tf.verifies)
	}
}

func TestTwoStepFactorLookupErrorNeverSignsIn(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	g.tf.hasErr = context.DeadlineExceeded
	rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}, nil)
	if rec.Code == 303 || cookieNamed(rec, "hatamoto_session") != nil {
		t.Fatalf("signed in without knowing the factor state: %d", rec.Code)
	}
}

func TestTwoStepUserWithoutFactorSignsInAsBefore(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	g.tf.uid = uuid.New()
	rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}, nil)
	if rec.Code != 303 || cookieNamed(rec, "hatamoto_session") == nil {
		t.Fatalf("no factor: %d", rec.Code)
	}
}

func TestTwoStepPagesGolden(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	p := g.password(t)
	g.tf.passkeys = 1
	for name, target := range map[string]string{"two-step": "/admin/login/2fa", "two-step-recovery": "/admin/login/2fa?recovery=1"} {
		rec := g.withPending("GET", target, p, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", name, strings.Join(issues, "; "))
		}
		compareGolden(t, "pages/auth-"+name+".html", normalizePage(rec.Body.String()))
	}
	if rec := g.do("GET", "/admin/login/2fa", "", nil, nil); rec.Code != 303 {
		t.Errorf("no pending cookie: %d", rec.Code)
	}
}

// The pending sign-in remembers the first step: the session is issued for that
// method, so the sign-in policy judges the real one. Cookies without it are a
// password sign-in.
func TestTwoStepCompletesWithThePendingVia(t *testing.T) {
	t.Parallel()

	g := newTwoStepRig(t)
	seal := func(via string) string {
		p, err := twofactor.NewPending(g.tf.uid, "", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		p.Via = via
		return g.a.deps.PendingKey.Seal(p)
	}
	for _, tc := range []struct {
		via  string
		want authstate.Via
	}{
		{"provider:x", authstate.Via{Method: authstate.MethodProvider, Provider: "x"}},
		{"", authstate.Via{Method: authstate.MethodPassword}},
	} {
		g.tf.totp = "123456"
		g.auth.vias = nil
		if rec := g.second(seal(tc.via), "123456", nil); rec.Code != 303 {
			t.Fatalf("via %q: %d", tc.via, rec.Code)
		}
		if len(g.auth.vias) != 1 || g.auth.vias[0] != tc.want {
			t.Fatalf("via %q: issued for %+v, want %+v", tc.via, g.auth.vias, tc.want)
		}
	}
}
