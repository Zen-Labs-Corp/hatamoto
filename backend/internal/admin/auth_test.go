package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/user"
)

// fakeAuth models auth.Service: lockout after 3 bad passwords, one password.
type fakeAuth struct {
	mu     sync.Mutex
	users  map[string]*user.User
	fails  map[string]int
	issued []uuid.UUID
}

func (f *fakeAuth) LoginSession(_ context.Context, email, password, _, _ string) (*user.User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[strings.ToLower(email)]
	if !ok {
		return nil, "", auth.ErrInvalidCredentials
	}
	if f.fails[u.Email] >= 3 {
		return nil, "", auth.ErrAccountLocked
	}
	if password != "correct-horse-battery" {
		f.fails[u.Email]++
		return nil, "", auth.ErrInvalidCredentials
	}
	return u, "owner-token", nil
}

func (f *fakeAuth) IssueSession(_ context.Context, id uuid.UUID, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued = append(f.issued, id)
	return "owner-token", nil
}

type fakeTokens struct {
	mu      sync.Mutex
	resets  map[string]*user.User // live reset tokens
	invites map[string]*auth.Invite
	issued  int
	user    *user.User
	listErr error // makes ListInvites fail
}

func (f *fakeTokens) IssueReset(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued++
	tok := "reset-token-" + uuid.NewString()
	f.resets[tok] = f.user
	return tok, nil
}
func (f *fakeTokens) ResetValid(_ context.Context, t string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.resets[t]
	return ok
}
func (f *fakeTokens) ConsumeReset(_ context.Context, t, pw string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.resets[t]
	if !ok {
		return nil, auth.ErrTokenInvalid
	}
	delete(f.resets, t)
	return u, nil
}
func (f *fakeTokens) IssueInvite(_ context.Context, addr string, role user.Role, by uuid.UUID) (string, *auth.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.EqualFold(addr, f.user.Email) {
		return "", nil, user.ErrEmailAlreadyExists
	}
	if !strings.Contains(addr, "@") {
		return "", nil, auth.ErrInvalidEmail
	}
	for t, old := range f.invites {
		if old.Email == strings.ToLower(addr) {
			delete(f.invites, t)
		}
	}
	inv := &auth.Invite{ID: uuid.New(), Email: strings.ToLower(addr), Role: role, InvitedBy: &by, ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
	tok := "invite-token-" + uuid.NewString()
	f.invites[tok] = inv
	return tok, inv, nil
}
func (f *fakeTokens) ListInvites(context.Context) ([]auth.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []auth.Invite
	for _, inv := range f.invites {
		out = append(out, *inv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}
func (f *fakeTokens) RevokeInvite(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for t, inv := range f.invites {
		if inv.ID == id {
			delete(f.invites, t)
		}
	}
	return nil
}
func (f *fakeTokens) PeekInvite(_ context.Context, t string) (*auth.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if inv, ok := f.invites[t]; ok {
		return inv, nil
	}
	return nil, auth.ErrTokenInvalid
}
func (f *fakeTokens) ConsumeInvite(_ context.Context, t, name, pw string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.invites[t]
	if !ok {
		return nil, auth.ErrTokenInvalid
	}
	delete(f.invites, t)
	n := name
	return &user.User{ID: uuid.New(), Email: inv.Email, Role: inv.Role, Name: &n}, nil
}

type fakeFinder struct{ u *user.User }

func (f fakeFinder) GetByEmail(_ context.Context, e string) (*user.User, error) {
	if f.u != nil && strings.EqualFold(e, f.u.Email) {
		return f.u, nil
	}
	return nil, user.ErrUserNotFound
}
func (f fakeFinder) Count(context.Context) (int, error) { return 1, nil }

type fakeMailer struct {
	mu         sync.Mutex
	configured bool
	sent       chan email.ResetMailData
	invites    []email.InviteMailData
	tests      []string
	testErr    error

	addrChanged []addrMail
	addrSetup   []string
	setupAddrs  []string // the address each setup mail carried
	addrErr     error
}

// addrMail is one admin-address-changed mail the fake received.
type addrMail struct {
	to   string
	data email.AdminAddressChangedData
}

func (f *fakeMailer) SendInvite(_ context.Context, to string, d email.InviteMailData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invites = append(f.invites, d)
	return nil
}

func (f *fakeMailer) SendTest(_ context.Context, to, site string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tests = append(f.tests, to)
	return f.testErr
}

func (f *fakeMailer) SendAdminAddressChanged(_ context.Context, to string, d email.AdminAddressChangedData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrChanged = append(f.addrChanged, addrMail{to: to, data: d})
	return f.addrErr
}

func (f *fakeMailer) SendAdminAddressSetup(_ context.Context, to string, d email.AdminAddressSetupData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrSetup = append(f.addrSetup, to)
	f.setupAddrs = append(f.setupAddrs, d.Address)
	return f.addrErr
}

func (f *fakeMailer) IsConfigured() bool { return f.configured }
func (f *fakeMailer) SendPasswordReset(_ context.Context, to string, d email.ResetMailData) error {
	f.sent <- d
	return nil
}

type authRig struct {
	*rig
	auth   *fakeAuth
	tokens *fakeTokens
	mail   *fakeMailer
}

func newAuthRig(t *testing.T, mailConfigured bool) *authRig {
	t.Helper()
	n := "Olga"
	u := &user.User{ID: uuid.New(), Email: "olga@example.com", Role: user.RoleOwner, Name: &n}
	fa := &fakeAuth{users: map[string]*user.User{u.Email: u}, fails: map[string]int{}}
	ft := &fakeTokens{resets: map[string]*user.User{}, invites: map[string]*auth.Invite{}, user: u}
	fm := &fakeMailer{configured: mailConfigured, sent: make(chan email.ResetMailData, 4)}
	g := newRig(t, func(d *Deps) {
		d.Auth, d.Tokens, d.Finder, d.Mailer = fa, ft, fakeFinder{u}, fm
	})
	return &authRig{rig: g, auth: fa, tokens: ft, mail: fm}
}

func TestLoginPageAccessible(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	for _, path := range []string{"/admin/login", "/admin/login?expired=1&next=/admin/settings/users", "/admin/forgot", "/admin/reset?token=bad", "/admin/accept?token=bad"} {
		rec := g.do("GET", path, "", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", path, strings.Join(issues, "; "))
		}
		name := strings.NewReplacer("/admin/", "", "?", "-", "=", "-", "&", "-", "/", "_").Replace(path)
		compareGolden(t, "pages/auth-"+name+".html", normalizePage(rec.Body.String()))
	}
	body := g.do("GET", "/admin/login?expired=1&next=/admin/settings/users", "", nil, nil).Body.String()
	if !strings.Contains(body, "Your session expired") || !strings.Contains(body, `name="next" value="/admin/settings/users"`) {
		t.Errorf("expired banner or next missing:\n%s", body)
	}
}

func TestLoginSuccessSetsCookiesAndRedirects(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}, "next": {"/admin/settings/users?page=2"}}
	rec := g.do("POST", "/admin/login", "", form, map[string]string{"X-Forwarded-Proto": "https"})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/users?page=2" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	var sess, pre *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "hatamoto_session":
			sess = c
		case "hm_pre":
			pre = c
		case "hatamoto_csrf":
			t.Errorf("sign-in sets the readable csrf cookie: %+v", c)
		}
	}
	if sess == nil || !sess.HttpOnly || sess.SameSite != http.SameSiteStrictMode || sess.Value != "owner-token" {
		t.Fatalf("session cookie: %+v", sess)
	}
	if pre == nil || pre.MaxAge >= 0 || pre.Path != "/admin" {
		t.Fatalf("sign-in must expire the pre-session cookie: %+v", pre)
	}
	// The browser would now be signed in: the session reaches the shell.
	if rec := g.do("GET", "/admin/nope", "owner-token", nil, nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Owner Olga") {
		t.Fatalf("session not usable: %d", rec.Code)
	}
}

// Secure follows the connection: plain HTTP gets a cookie that works; HTTPS (or
// a local proxy saying so) gets Secure. An outside client cannot forge the header.
func TestSessionCookieSecureFollowsRequest(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	form := url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}
	secure := func(remote, proto string) bool {
		req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		hdr := map[string]string{}
		if proto != "" {
			hdr["X-Forwarded-Proto"] = proto
		}
		g.addFetchMeta(req, "", hdr)
		rec := httptest.NewRecorder()
		g.h.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.Name == "hatamoto_session" {
				return c.Secure
			}
		}
		t.Fatal("no session cookie")
		return false
	}
	if secure("127.0.0.1:5000", "") {
		t.Error("plain HTTP must not get a Secure cookie (the browser would drop it)")
	}
	if !secure("127.0.0.1:5000", "https") {
		t.Error("X-Forwarded-Proto: https from the local proxy must set Secure")
	}
	if secure("203.0.113.5:5000", "https") {
		t.Error("a public client's X-Forwarded-Proto must not be trusted")
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	attempt := func(email, pw string) (int, string) {
		rec := g.do("POST", "/admin/login", "", url.Values{"email": {email}, "password": {pw}}, nil)
		return rec.Code, rec.Body.String()
	}
	norm := func(s string) string {
		s = regexp.MustCompile(`nonce="[^"]*"|content="[^"]*"|value="[^"]*"|admin\.[0-9a-f]+|[a-z]+\.[0-9a-f]{12}\.js`).ReplaceAllString(s, "")
		return s
	}
	c1, b1 := attempt("nobody@example.com", "whatever-password")
	c2, b2 := attempt("olga@example.com", "wrong-password-here")
	for i := 0; i < 3; i++ {
		attempt("olga@example.com", "wrong-password-here")
	}
	c3, b3 := attempt("olga@example.com", "correct-horse-battery") // now locked, even with the right password
	if c1 != 422 || c2 != 422 || c3 != 422 {
		t.Fatalf("statuses %d %d %d", c1, c2, c3)
	}
	if !strings.Contains(b1, "Invalid email or password.") {
		t.Fatalf("message missing:\n%s", b1)
	}
	// One status slot: a single flash, wrapped in #login-status and referenced by both inputs.
	if n := strings.Count(b1, `class="hm-flash `); n != 1 || !strings.Contains(b1, `<div id="login-status">`) || strings.Count(b1, `aria-describedby="login-status"`) != 2 {
		t.Errorf("login wants exactly one status slot tied to both inputs (flashes: %d):\n%s", n, b1)
	}
	if norm(b1) != norm(b2) || norm(b2) != norm(b3) {
		t.Error("unknown address, wrong password and locked account render differently")
	}
	for _, b := range []string{b1, b2, b3} {
		if strings.Contains(strings.ToLower(b), "locked") {
			t.Error("the lockout state leaked")
		}
	}
	if issues := a11y.Check(b2, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("error page a11y: %v", issues)
	}
}

func TestLoginNextOpenRedirects(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	for _, bad := range []string{"https://evil.example/", "//evil.example", "/\\evil.example", "/admin//evil.example", "javascript:alert(1)", "/api/v1/users", "/admin/login"} {
		rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}, "next": {bad}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities" {
			t.Errorf("next=%q went to %q", bad, rec.Header().Get("Location"))
		}
	}
	// The Next admin's "redirect" parameter is honoured the same way.
	rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}, "redirect": {"/admin/media"}}, nil)
	if rec.Header().Get("Location") != "/admin/media" {
		t.Errorf("redirect param: %s", rec.Header().Get("Location"))
	}
}

func TestLoginRedirectsSignedInUsersAndFreshInstalls(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	if rec := g.do("GET", "/admin/login?next=/admin/settings/users", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/users" {
		t.Errorf("signed in: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	fresh := newRig(t, func(d *Deps) { d.NeedsSetup = func(context.Context) bool { return true } })
	if rec := fresh.do("GET", "/admin/login", "", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/admin/setup" {
		t.Errorf("fresh install: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogoutPostOnlyRevokesAndClears(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	if rec := g.do("GET", "/admin/logout", "owner-token", nil, nil); rec.Code == 303 && rec.Header().Get("Set-Cookie") != "" {
		t.Error("GET /admin/logout must not sign anyone out")
	}
	rec := g.do("POST", "/admin/logout", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" || len(g.sessions.revoked) != 1 {
		t.Fatalf("%d %s revoked=%v", rec.Code, rec.Header().Get("Location"), g.sessions.revoked)
	}
	cleared := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	if !cleared["hatamoto_session"] || !cleared["hm_pre"] || !cleared["hatamoto_csrf"] { // the last is the legacy cookie of upgraded browsers
		t.Errorf("cookies not cleared: %v", cleared)
	}
	// Without the CSRF token a logout is refused.
	if rec := g.do("POST", "/admin/logout", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
		t.Errorf("csrf-less logout: %d", rec.Code)
	}
}

func TestForgotIsNeutralAndSendsOnlyForRealAccounts(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	post := func(addr string) string {
		rec := g.do("POST", "/admin/forgot", "", url.Values{"email": {addr}}, nil)
		if rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
		return regexp.MustCompile(`nonce="[^"]*"|value="[^"]*"|content="[^"]*"`).ReplaceAllString(rec.Body.String(), "")
	}
	known, unknown := post("olga@example.com"), post("ghost@example.com")
	if known != unknown {
		t.Error("the answer differs between a known and an unknown address")
	}
	if !strings.Contains(known, "If that address has an account") {
		t.Errorf("neutral text missing:\n%s", known)
	}
	select {
	case d := <-g.mail.sent:
		if !strings.HasPrefix(d.URL, "https://acme.example/admin/reset?token=reset-token-") || d.ExpiresIn == "" {
			t.Errorf("mail: %+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no email sent for the real account")
	}
	select {
	case d := <-g.mail.sent:
		t.Fatalf("an email went out for an unknown address: %+v", d)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestForgotWithoutMailExplainsTheCLI(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, false)
	body := g.do("GET", "/admin/forgot", "", nil, nil).Body.String()
	if !strings.Contains(body, "hatamoto user reset-password") || strings.Contains(body, "Send reset link") {
		t.Errorf("no-mail notice wrong:\n%s", body)
	}
	if b := g.do("GET", "/admin/forgot?email=olga@example.com", "", nil, nil).Body.String(); !strings.Contains(b, "--email olga@example.com") {
		t.Errorf("the typed address is not in the command:\n%s", b)
	}
	if b := g.do("GET", "/admin/forgot?email=x@y.z;rm+-rf+/", "", nil, nil).Body.String(); strings.Contains(b, "rm -rf") || !strings.Contains(b, "--email you@example.com") {
		t.Errorf("an unsafe address reached the command line:\n%s", b)
	}
	if rec := g.do("POST", "/admin/forgot", "", url.Values{"email": {"olga@example.com"}}, nil); rec.Code != 200 || len(g.tokens.resets) != 0 {
		t.Errorf("a token was issued without mail: %d %v", rec.Code, g.tokens.resets)
	}
}

func TestResetFlow(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	tok, _ := g.tokens.IssueReset(context.Background(), uuid.Nil)
	rec := g.do("GET", "/admin/reset?token="+tok, "", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="token" value="`+tok+`"`) || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("form: %d %v", rec.Code, rec.Header())
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	// Validation failures re-render with 422 and do not burn the token.
	for name, form := range map[string]url.Values{
		"short":    {"token": {tok}, "password": {"short"}, "confirm": {"short"}},
		"mismatch": {"token": {tok}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-2"}},
	} {
		rec := g.do("POST", "/admin/reset", "", form, nil)
		if rec.Code != 422 || !g.tokens.ResetValid(context.Background(), tok) {
			t.Errorf("%s: %d valid=%v", name, rec.Code, g.tokens.ResetValid(context.Background(), tok))
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	rec = g.do("POST", "/admin/reset", "", url.Values{"token": {tok}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities" || len(g.auth.issued) != 1 {
		t.Fatalf("success: %d %s issued=%d", rec.Code, rec.Header().Get("Location"), len(g.auth.issued))
	}
	// Single use: the same link is now a neutral dead end.
	rec = g.do("POST", "/admin/reset", "", url.Values{"token": {tok}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "This link no longer works") || !strings.Contains(rec.Body.String(), "/admin/forgot") {
		t.Errorf("reused token: %d\n%s", rec.Code, rec.Body.String())
	}
	if rec := g.do("GET", "/admin/reset?token=nope", "", nil, nil); !strings.Contains(rec.Body.String(), "This link no longer works") {
		t.Error("bad token page")
	}
}

func TestAcceptInvite(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.tokens.invites["inv-1"] = &auth.Invite{Email: "new@example.com", Role: user.RoleEditor}
	rec := g.do("GET", "/admin/accept?token=inv-1", "", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "new@example.com") || !strings.Contains(body, "<strong>editor</strong>") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("form: %d\n%s", rec.Code, body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = g.do("POST", "/admin/accept", "", url.Values{"token": {"inv-1"}, "name": {"Nia"}, "password": {"short"}, "confirm": {"short"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "at least 12 characters") {
		t.Errorf("short: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/accept", "", url.Values{"token": {"inv-1"}, "name": {"Nia"}, "password": {"a-long-enough-pw-1"}, "confirm": {"a-long-enough-pw-1"}}, nil)
	if rec.Code != 303 || len(g.auth.issued) != 1 {
		t.Fatalf("accept: %d", rec.Code)
	}
	rec = g.do("GET", "/admin/accept?token=inv-1", "", nil, nil)
	if !strings.Contains(rec.Body.String(), "no longer works") {
		t.Error("an accepted invite must be dead")
	}
}

func TestRateLimiterWrapsPublicAuthScreensOnly(t *testing.T) {
	t.Parallel()

	calls := 0
	g := newAuthRig(t, true)
	g2 := newRig(t, func(d *Deps) {
		*d = withDeps(*d, g)
		d.RateLimit = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; next.ServeHTTP(w, r) })
		}
	})
	for _, p := range []string{"/admin/login", "/admin/forgot", "/admin/reset?token=x", "/admin/accept?token=x"} {
		g2.do("GET", p, "", nil, nil)
	}
	g2.do("GET", "/admin/_/tokens.css", "", nil, nil)
	g2.do("GET", "/admin/nope", "owner-token", nil, nil)
	if calls != 4 {
		t.Fatalf("limiter ran %d times, want 4 (the four public auth screens)", calls)
	}
}

func withDeps(d Deps, g *authRig) Deps {
	d.Auth, d.Tokens, d.Finder, d.Mailer = g.auth, g.tokens, fakeFinder{g.tokens.user}, g.mail
	return d
}

func TestForgotLinkIgnoresForwardedHostHeaders(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.do("POST", "/admin/forgot", "", url.Values{"email": {"olga@example.com"}}, map[string]string{"X-Forwarded-Host": "evil.test", "X-Forwarded-Proto": "http"})
	select {
	case d := <-g.mail.sent:
		if !strings.HasPrefix(d.URL, "https://acme.example/admin/reset?token=") || strings.Contains(d.URL, "evil.test") {
			t.Fatalf("link built from request headers: %s", d.URL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no email")
	}
}

func TestForgotWithoutSiteAddressSendsNothing(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	rec := g.do("POST", "/admin/forgot", "", url.Values{"email": {"olga@example.com"}}, map[string]string{"X-Forwarded-Host": "evil.test"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "If that address has an account") {
		t.Fatalf("the page must answer the same: %d", rec.Code)
	}
	select {
	case d := <-g.mail.sent:
		t.Fatalf("a link was emailed without a configured site address: %s", d.URL)
	case <-time.After(200 * time.Millisecond):
	}
	if len(g.tokens.resets) != 0 {
		t.Error("a reset token was issued although nothing can be sent")
	}
}

func TestInviteWithoutSiteAddressIsNotEmailed(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	rec := g.do("POST", "/admin/settings/users/invite", "owner-token", url.Values{"email": {"new@example.com"}, "role": {"editor"}}, map[string]string{"X-Forwarded-Host": "evil.test"})
	body := g.do("GET", rec.Header().Get("Location"), "owner-token", nil, nil).Body.String()
	if rec.Code != 303 || len(g.mail.invites) != 0 || !strings.Contains(body, "site address is not set") {
		t.Fatalf("invites sent %d\n%s", len(g.mail.invites), body)
	}
	// The link the owner copies comes from their own request, never a forged header
	// (the test peer is not a trusted proxy).
	if strings.Contains(body, "evil.test") {
		t.Fatal("forwarded host reached the page")
	}
}

func TestOwnerSeesSiteAddressBanner(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.BaseURL = func(*http.Request) string { return "" } })
	if body := g.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Set your site address") {
		t.Error("owner banner missing")
	}
	g = newRig(t, func(d *Deps) { d.BaseURL = func(*http.Request) string { return "https://x.example" } })
	if body := g.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String(); strings.Contains(body, "Set your site address") {
		t.Error("banner shown although the address is set")
	}
}

func TestSecureCookieFlagIsAMinimum(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.a.deps.CookieSecure = true
	rec := g.do("POST", "/admin/login", "", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}, nil) // plain HTTP, no proxy header
	for _, c := range rec.Result().Cookies() {
		if !c.Secure {
			t.Errorf("cookie %s lost Secure under ADMIN_COOKIE_SECURE", c.Name)
		}
	}
}

func TestSignInRevokesThePresentedSession(t *testing.T) {
	t.Parallel()

	g := newAuthRig(t, true)
	g.do("POST", "/admin/login", "editor-token", url.Values{"email": {"olga@example.com"}, "password": {"correct-horse-battery"}}, nil)
	if len(g.sessions.revoked) != 1 {
		t.Fatalf("old session not revoked: %v", g.sessions.revoked)
	}
}

func TestLogoutClearSiteData(t *testing.T) {
	t.Parallel()

	const want = `"cache", "storage"`
	for _, form := range []url.Values{{}, {"all": {"1"}}} {
		g := newAuthRig(t, true)
		rec := g.do("POST", "/admin/logout", "owner-token", form, nil)
		// fakeSessions records no revocation for all=1; the revoke itself is pinned above.
		if rec.Code != 303 || rec.Header().Get("Clear-Site-Data") != want || (form.Get("all") == "" && len(g.sessions.revoked) != 1) {
			t.Errorf("%v: %d %q revoked=%v", form, rec.Code, rec.Header().Get("Clear-Site-Data"), g.sessions.revoked)
		}
	}

	g := newAuthRig(t, true)
	rec := g.do("POST", "/admin/logout", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""})
	if rec.Code != 403 || rec.Header().Get("Clear-Site-Data") != "" || len(g.sessions.revoked) != 0 {
		t.Errorf("refused logout: %d %q revoked=%v", rec.Code, rec.Header().Get("Clear-Site-Data"), g.sessions.revoked)
	}
}
