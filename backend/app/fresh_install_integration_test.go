//go:build integration

package app

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/server"
)

// The v1-10 done-when: a fresh install goes from first visit to a logged-in
// owner with starter content, through the real server and a real database, with
// no Next admin anywhere (the legacy proxy is switched off).
func TestFreshInstallToLoggedInOwnerWithStarterContent_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef-test") // gitleaks:allow (test value)
	t.Setenv("HATAMOTO_SETUP_CODE", "SETUP-CODE-1234")
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_PLUGIN_DIR", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, db.Pool(), cfg.Hatamoto.SiteName, quietLogger()); err != nil {
		t.Fatal(err)
	}
	// As app boot does: a fresh install with no owner gets a random address.
	ap, err := resolveAdminPath(ctx, adminpath.NewStore(db.Pool()), staticSite{}, func(context.Context) (bool, error) { return false, nil }, "", quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.Resolved = ap
	var logs bytes.Buffer
	srv := httptest.NewServer(server.New(cfg, db, slog.New(slog.NewTextHandler(&logs, nil))).Handler())
	defer srv.Close()

	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	c := newClient()
	csrfMeta := regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
	var tokMu sync.Mutex
	tokens := map[*http.Client]string{} // client -> token of the last page it was served
	do := func(c *http.Client, method, path string, form url.Values) (int, string, http.Header) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		// Like a browser: a GET is a navigation (the jar carries hm_pre or the
		// session); a POST is same-origin and carries the token of the last
		// page this client was served.
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if method == "GET" {
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Dest", "document")
		} else {
			tokMu.Lock()
			req.Header.Set("X-CSRF-Token", tokens[c])
			tokMu.Unlock()
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if m := csrfMeta.FindSubmatch(b); m != nil {
			tokMu.Lock()
			tokens[c] = string(m[1])
			tokMu.Unlock()
		}
		return resp.StatusCode, string(b), resp.Header
	}
	expect := func(what string, code, want int, body string) {
		t.Helper()
		if code != want {
			t.Fatalf("%s: status %d, want %d\n%s", what, code, want, body)
		}
	}

	// 1. The default address is the standard 404 on a fresh install; the logged
	// setup URL under the generated address serves the wizard.
	if ap == "/admin" || !strings.HasPrefix(ap, "/") {
		t.Fatalf("a fresh install resolved %q, want a random address", ap)
	}
	if !strings.Contains(logs.String(), "Setup: open "+ap+"/setup and enter the setup code from HATAMOTO_SETUP_CODE") {
		t.Errorf("setup log line does not carry the address:\n%s", logs.String())
	}
	for _, old := range []string{"/admin/setup", "/admin/login"} {
		code, body, _ := do(c, "GET", old, nil)
		if code != 404 {
			t.Fatalf("GET %s on a fresh install = %d, want the standard 404\n%.300s", old, code, body)
		}
	}
	// Login sends a fresh install to the wizard.
	code, body, h := do(c, "GET", ap+"/login", nil)
	expect("login on a fresh install", code, 303, body)
	if h.Get("Location") != ap+"/setup" {
		t.Fatalf("redirected to %s", h.Get("Location"))
	}
	code, body, _ = do(c, "GET", ap+"/setup", nil)
	expect("setup page", code, 200, body)
	if !strings.Contains(body, "Setup code") || !strings.Contains(body, "Set up your site") {
		t.Fatalf("setup page:\n%s", body)
	}

	// 2. The wizard needs the setup code; a wrong one creates nothing.
	account := func(code string) url.Values {
		return url.Values{"code": {code}, "name": {"Olga Owner"}, "email": {"olga@example.test"}, "password": {"a-long-enough-password"}, "confirm": {"a-long-enough-password"}}
	}
	st, body, _ := do(c, "POST", ap+"/setup", account("WRONG-CODE"))
	expect("wrong code", st, 422, body)
	var n int
	_ = db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_users`).Scan(&n)
	if n != 0 {
		t.Fatalf("a wrong code created %d users", n)
	}

	// Several people racing with the right code: exactly one becomes the owner.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins []int
	statuses := map[int]int{}
	clients := []*http.Client{c, newClient(), newClient(), newClient()}
	for _, cl := range clients {
		do(cl, "GET", ap+"/setup", nil) // get a pre-session cookie and token
	}
	for i, cl := range clients {
		wg.Add(1)
		go func(i int, cl *http.Client) {
			defer wg.Done()
			f := account("setup-code-1234") // case and dashes are forgiven
			f.Set("email", "racer"+string(rune('a'+i))+"@example.test")
			st, _, _ := do(cl, "POST", ap+"/setup", f)
			mu.Lock()
			statuses[st]++
			if st == 303 {
				wins = append(wins, i)
			}
			mu.Unlock()
		}(i, cl)
	}
	wg.Wait()
	if len(wins) != 1 {
		t.Fatalf("%d racers became owner (%v), want exactly 1", len(wins), statuses)
	}
	owner := clients[wins[0]]

	// 3. The wizard steps, as the owner.
	for _, step := range []struct {
		path string
		form url.Values
		next string
	}{
		{ap + "/setup/site", url.Values{"name": {"Acme Bakery"}, "url": {"https://acme.example/"}, "language": {"en-AU"}, "timezone": {"Australia/Sydney"}}, ap + "/setup/email"},
		// A local sink needs no TLS or sign-in; Save runs the connection check first.
		{ap + "/setup/email", url.Values{"provider": {"custom"}, "host": {sink.Host()}, "port": {strconv.Itoa(sink.Port())}, "security": {"none"}, "from": {"hello@acme.example"}}, ap + "/setup/theme"},
		{ap + "/setup/theme", url.Values{"colour": {"#aa3300"}}, ap + "/setup/content"},
		{ap + "/setup/content", url.Values{"content": {"starter"}}, ap + "/setup/done"},
	} {
		code, body, _ := do(owner, "GET", step.path, nil)
		expect("GET "+step.path, code, 200, body)
		code, body, h := do(owner, "POST", step.path, step.form)
		expect("POST "+step.path, code, 303, body)
		if h.Get("Location") != step.next {
			t.Fatalf("%s went to %s, want %s", step.path, h.Get("Location"), step.next)
		}
	}
	code, body, _ = do(owner, "GET", ap+"/setup/done", nil)
	expect("done", code, 200, body)
	if !strings.Contains(body, "Email is set up") {
		t.Errorf("the done page does not show email as set up:\n%s", body)
	}
	if !strings.Contains(body, "Starter content installed") {
		t.Errorf("no starter summary on the done page:\n%s", body)
	}

	// The unauthenticated auth screens share one rate limiter per client IP
	// (5 req/s, burst 10); let the wizard's requests drain before probing.
	time.Sleep(3 * time.Second)

	// 4. Setup is over: 404 for everyone, and the owner is signed in without Next.
	for _, who := range []*http.Client{owner, newClient()} {
		code, _, _ = do(who, "GET", ap+"/setup", nil)
		if code != 404 {
			t.Errorf("%s/setup after setup = %d, want 404", ap, code)
		}
	}
	code, body, _ = do(owner, "GET", ap+"/anything", nil)
	expect("signed-in shell", code, 404, body)
	if !strings.Contains(body, "Olga Owner") || !strings.Contains(body, `aria-label="Admin"`) || strings.Contains(body, "Sign in") {
		t.Errorf("owner is not signed in to the Go admin shell:\n%s", body)
	}
	anon := newClient()
	// A signed-out visitor gets the standard 404 for any page but the sign-in ones.
	code, _, _ = do(anon, "GET", ap+"/anything", nil)
	if code != 404 {
		t.Errorf("anonymous visitor at %s/anything: %d, want 404", ap, code)
	}
	if code, _, _ = do(anon, "GET", ap+"/login", nil); code != 200 {
		t.Errorf("anonymous visitor at %s/login: %d, want 200", ap, code)
	}

	// 5. The site renders its starter content, including the blog archive (P6).
	for path, want := range map[string]string{
		"/":                         "Acme Bakery",
		"/about":                    "About Acme Bakery",
		"/contact":                  "Get in touch",
		"/blog":                     "Hello, world",
		"/blog/hello-world":         "Welcome to Acme Bakery",
		"/blog/how-this-site-works": "Next steps",
	} {
		code, body, _ := do(anon, "GET", path, nil)
		if code != 200 || !strings.Contains(body, want) {
			t.Errorf("GET %s = %d, want 200 containing %q\n%.600s", path, code, want, body)
		}
	}
	if code, body, _ := do(anon, "GET", "/", nil); !strings.Contains(body, `lang="en-AU"`) || !strings.Contains(body, "#aa3300") {
		t.Errorf("site language / brand colour from the wizard not applied (code %d)", code)
	}

	// 6. Counts: one of everything, the home page reused (not duplicated).
	count := func(q string) (n int) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM hatamoto_users`:                                                                                       1,
		`SELECT count(*) FROM hatamoto_entities WHERE status='published'`:                                                           5,
		`SELECT count(*) FROM hatamoto_entities WHERE slug='home'`:                                                                  1,
		`SELECT count(*) FROM hatamoto_menus`:                                                                                       2,
		`SELECT count(*) FROM hatamoto_forms WHERE slug='contact'`:                                                                  1,
		`SELECT count(*) FROM hatamoto_site_config WHERE setup_completed_at IS NOT NULL AND public_base_url='https://acme.example'`: 1,
	} {
		if got := count(q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

// P6 of the v1-10 design: what /blog does on a fresh database with no posts and
// no starter content. The archive is generated from the post type, not from a
// record, so it must not 404 just because nothing was installed.
func TestBlogArchiveOnFreshDatabase_Integration(t *testing.T) {
	dsn := scratchDSN(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef-test") // gitleaks:allow (test value)
	t.Setenv("MEDIA_UPLOAD_DIR", t.TempDir())
	t.Setenv("HATAMOTO_PLUGIN_DIR", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateUp(ctx, db.Pool(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, db.Pool(), cfg.Hatamoto.SiteName, quietLogger()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(cfg, db, quietLogger()).Handler())
	defer srv.Close()
	for _, path := range []string{"/", "/blog"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("fresh database GET %s = %d (%d bytes)", path, resp.StatusCode, len(b))
		if resp.StatusCode != 200 {
			t.Errorf("GET %s = %d, want 200 on a fresh database", path, resp.StatusCode)
		}
	}
}
