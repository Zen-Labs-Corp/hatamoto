//go:build integration

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/user"
)

// browser is a cookie-keeping client that sends what a browser sends: fetch
// metadata on every request and, on a POST, the CSRF token of the last page it
// was served (the sealed-admin guard refuses anything else).
type browser struct {
	t     *testing.T
	base  string
	c     *http.Client
	token string
}

var csrfMetaRE = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type reply struct {
	Code   int
	Body   string
	Header http.Header
}

func (b *browser) do(method, path string, form url.Values) reply {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method == "GET" {
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", "document")
	} else {
		req.Header.Set("X-CSRF-Token", b.token)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if m := csrfMetaRE.FindSubmatch(raw); m != nil {
		b.token = string(m[1])
	}
	return reply{resp.StatusCode, string(raw), resp.Header}
}

func (b *browser) get(path string) reply { return b.do("GET", path, nil) }

// expect fails unless r has the status and, for a redirect, the location.
func expectReply(t *testing.T, what string, r reply, code int, location string) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("%s: status %d, want %d\n%.400s", what, r.Code, code, r.Body)
	}
	if location != "" && r.Header.Get("Location") != location {
		t.Fatalf("%s: redirected to %q, want %q", what, r.Header.Get("Location"), location)
	}
}

// addOwnerAs creates a user with a known password.
func (e *wireEnv) addUser(t *testing.T, s *Server, mail, name string, role user.Role) {
	t.Helper()
	hash, _ := auth.HashPassword("a-long-enough-password")
	u, err := s.userRepo.Create(context.Background(), &user.CreateUserInput{Email: mail, Role: role, Name: name}, hash)
	if err != nil {
		t.Fatal(err)
	}
	// The admin sends a user without a name and photo to "Complete your profile".
	if _, err := person.NewProfiles(s.people(), s.entityService).Save(context.Background(), u.ID, person.ProfileInput{Name: name, Photo: "/media/test.png"}); err != nil {
		t.Fatal(err)
	}
}

func (b *browser) login(t *testing.T, ap, mail string) {
	t.Helper()
	b.get(ap + "/login")
	expectReply(t, "login "+mail, b.do("POST", ap+"/login", url.Values{"email": {mail}, "password": {"a-long-enough-password"}}), 303, "")
}

// withMailSink points the server's mail at a local plain sink.
func withMailSink(t *testing.T, s *Server) *smtptest.Server {
	t.Helper()
	t.Setenv("EMAIL_DISABLED", "") // the gate sets the kill switch, which short-circuits sends
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	s.emailService.Reload(email.Config{SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "admin@example.test", FromName: "Wire IT", Security: email.SecurityNone})
	return sink
}

// A fresh install: the default address is the standard 404, the logged setup
// URL serves the wizard, a weak address typed with its phrase is live at once,
// and the record names who chose it.
func TestAddressChangeFreshWizardWeakChoice(t *testing.T) {
	e := newWireEnv(t, map[string]string{"HATAMOTO_SETUP_CODE": "SETUP-CODE-1234"})
	ctx := context.Background()
	st := adminpath.NewStore(e.db.Pool())
	ap, err := adminpath.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := st.SetIfPending(ctx, ap); err != nil || !ok {
		t.Fatalf("SetIfPending = %v, %v", ok, err)
	}
	s, logs := e.server(t, ap)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	b := newBrowser(t, srv.URL)

	if !strings.Contains(logs.String(), "Setup: open "+ap+"/setup") {
		t.Fatalf("setup log line missing:\n%s", logs.String())
	}
	if r := b.get("/admin/setup"); r.Code != 404 {
		t.Fatalf("/admin/setup = %d, want the standard 404", r.Code)
	}
	expectReply(t, "wizard page", b.get(ap+"/setup"), 200, "")
	acct := url.Values{"code": {"setup-code-1234"}, "name": {"Olga Owner"}, "email": {"olga@example.test"}, "password": {"a-long-enough-password"}, "confirm": {"a-long-enough-password"}}
	expectReply(t, "account", b.do("POST", ap+"/setup", acct), 303, ap+"/setup/admin-address")
	expectReply(t, "address step", b.get(ap+"/setup/admin-address"), 200, "")

	weak := url.Values{"mode": {"custom"}, "candidate": {ap}, "custom": {"/my-place"}}
	r := b.do("POST", ap+"/setup/admin-address", weak)
	expectReply(t, "weak without the phrase", r, 422, "")
	if !strings.Contains(r.Body, "/my-place") {
		t.Errorf("the typed confirm does not name the address:\n%.600s", r.Body)
	}
	weak.Set("confirm_phrase", "/my-place")
	expectReply(t, "weak with the phrase", b.do("POST", ap+"/setup/admin-address", weak), 303, "/my-place/setup/site")

	// No restart: the new prefix serves the next step, the old one is gone.
	expectReply(t, "next step at the new address", b.get("/my-place/setup/site"), 200, "")
	if r := newBrowser(t, srv.URL).get(ap + "/setup/site"); r.Code != 404 {
		t.Errorf("the generated address after the change = %d, want 404", r.Code)
	}
	got, err := st.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path == nil || *got.Path != "/my-place" || got.SetBy == nil || *got.SetBy != "Olga Owner" || got.SetAt == nil ||
		got.AckBy == nil || *got.AckBy != "Olga Owner" || got.AckAt == nil {
		t.Errorf("stored row = %+v", got)
	}
}

// Changing the address from Security: sudo first, then a live swap that hides
// the old address, tells signed-in admins, mails every owner and no editor,
// audits without the address and leaves the owner's sudo intact.
func TestAddressChangeFromSecurityEndToEnd(t *testing.T) {
	e := newWireEnv(t, nil)
	ctx := context.Background()
	st := adminpath.NewStore(e.db.Pool())
	ap, _ := adminpath.Generate()
	if err := st.Set(ctx, ap, "install"); err != nil {
		t.Fatal(err)
	}
	s, _ := e.server(t, ap)
	e.addUser(t, s, "olga@example.test", "Olga Owner", user.RoleOwner)
	e.addUser(t, s, "omar@example.test", "Omar Owner", user.RoleOwner)
	e.addUser(t, s, "edie@example.test", "Edie Editor", user.RoleEditor)
	sink := withMailSink(t, s)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	owner := newBrowser(t, srv.URL)
	owner.login(t, ap, "olga@example.test")
	editor := newBrowser(t, srv.URL)
	editor.login(t, ap, "edie@example.test")
	changeURL := ap + "/settings/security/admin-address/change"
	if r := editor.get(changeURL); r.Code != 403 {
		t.Errorf("an editor at the change page = %d, want 403", r.Code)
	}

	// Sudo gate: the change page sends the owner to confirm the password.
	r := owner.get(changeURL)
	if r.Code != 303 || !strings.Contains(r.Header.Get("Location"), ap+"/sudo") {
		t.Fatalf("change page without sudo = %d %q", r.Code, r.Header.Get("Location"))
	}
	owner.get(r.Header.Get("Location"))
	sudoURL, _ := url.Parse(r.Header.Get("Location"))
	r = owner.do("POST", sudoURL.RequestURI(), url.Values{"password": {"a-long-enough-password"}})
	expectReply(t, "sudo", r, 303, "")
	expectReply(t, "change page with sudo", owner.get(changeURL), 200, "")

	next, _ := adminpath.Generate()
	r = owner.do("POST", changeURL, url.Values{"mode": {"custom"}, "candidate": {next}, "custom": {next}})
	expectReply(t, "change", r, 303, next+"/settings/security/admin-address")

	// Live swap: the new address serves, and the owner still has sudo there.
	expectReply(t, "security page at the new address", owner.get(next+"/settings/security/admin-address"), 200, "")
	expectReply(t, "owner keeps sudo", owner.get(next+"/settings/security/admin-address/change"), 200, "")

	// The old address, signed out, is the answer for a path that never existed.
	anon := newBrowser(t, srv.URL)
	old := anon.get(ap + "/login")
	never := anon.get("/zz-never-existed/login")
	if old.Code != 404 {
		t.Fatalf("old address anonymous = %d, want 404", old.Code)
	}
	if never.Code != old.Code || old.Body != strings.ReplaceAll(never.Body, "/zz-never-existed", ap) && old.Body != never.Body {
		t.Errorf("old address body differs from a never-existed path\n--- old ---\n%.400s\n--- never ---\n%.400s", old.Body, never.Body)
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "Location", "Set-Cookie", "Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if a, b := old.Header.Get(h), never.Header.Get(h); a != b {
			t.Errorf("header %s differs: old %q, never %q", h, a, b)
		}
	}
	// A signed-in session is told the address changed, and not where.
	hint := owner.get(ap + "/settings")
	if hint.Code != 404 || !strings.Contains(hint.Body, "The admin address changed") || strings.Contains(hint.Body, next) {
		t.Errorf("signed-in hint at the old address = %d\n%.400s", hint.Code, hint.Body)
	}

	// Every owner was mailed, the editor was not.
	deadline := time.Now().Add(10 * time.Second)
	for len(sink.Messages()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	to := map[string]bool{}
	for _, m := range sink.Messages() {
		for _, rcpt := range m.To {
			to[strings.Trim(rcpt, "<> ")] = true
		}
	}
	if !to["olga@example.test"] || !to["omar@example.test"] || to["edie@example.test"] || len(to) != 2 {
		t.Errorf("mail recipients = %v", to)
	}

	// The audit row exists and never carries either address.
	var detail string
	var n int
	if err := e.db.Pool().QueryRow(ctx, `SELECT count(*), coalesce(max(detail::text), '') FROM hatamoto_admin_audit WHERE action = 'admin_address.change'`).Scan(&n, &detail); err != nil {
		t.Fatal(err)
	}
	if n != 1 || strings.Contains(detail, next) || strings.Contains(detail, ap) {
		t.Errorf("audit rows = %d, detail %s", n, detail)
	}
}

// An upgraded install on /admin with no acknowledgement: owners see the
// banner, editors do not; Keep with the phrase clears it and records who.
func TestAddressChangeUpgradeBannerAndKeep(t *testing.T) {
	e := newWireEnv(t, nil)
	ctx := context.Background()
	// As an upgrade leaves it: the default address, no record of who or when.
	if _, err := e.db.Pool().Exec(ctx, `UPDATE hatamoto_site_config SET admin_path = '/admin', admin_path_set_by = NULL, admin_path_set_at = NULL, admin_path_ack_by = NULL, admin_path_ack_at = NULL`); err != nil {
		t.Fatal(err)
	}
	s, _ := e.server(t, "/admin")
	e.addUser(t, s, "olga@example.test", "Olga Owner", user.RoleOwner)
	e.addUser(t, s, "edie@example.test", "Edie Editor", user.RoleEditor)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	const marker = "data-hm-address-banner"

	owner := newBrowser(t, srv.URL)
	owner.login(t, "/admin", "olga@example.test")
	editor := newBrowser(t, srv.URL)
	editor.login(t, "/admin", "edie@example.test")
	if r := owner.get("/admin/settings/general"); r.Code != 200 || !strings.Contains(r.Body, marker) {
		t.Fatalf("owner banner: %d, present=%v", r.Code, strings.Contains(r.Body, marker))
	}
	if r := editor.get("/admin/settings/general"); strings.Contains(r.Body, marker) {
		t.Error("the editor sees the owner's banner")
	}

	keep := "/admin/settings/security/admin-address/keep"
	expectReply(t, "keep page", owner.get(keep), 200, "")
	expectReply(t, "wrong phrase", owner.do("POST", keep, url.Values{"confirm_phrase": {"/nope"}}), 422, "")
	expectReply(t, "keep", owner.do("POST", keep, url.Values{"confirm_phrase": {"/admin"}}), 303, "/admin/settings/security/admin-address")

	if r := owner.get("/admin/settings/general"); strings.Contains(r.Body, marker) {
		t.Error("banner still shown after Keep")
	}
	if r := owner.get("/admin/settings/security/admin-address"); !strings.Contains(r.Body, "weak (accepted by Olga Owner on ") {
		t.Errorf("Security page does not show the acceptance:\n%.600s", r.Body)
	}
	got, err := adminpath.NewStore(e.db.Pool()).Get(ctx)
	if err != nil || got.AckBy == nil || *got.AckBy != "Olga Owner" || got.AckAt == nil {
		t.Errorf("stored row = %+v, %v", got, err)
	}
}
