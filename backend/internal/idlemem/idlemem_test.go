// Package idlemem measures what a booted, set-up, idle site holds in memory.
//
// TestBudgetIdleMemory starts the real binary, finishes the setup wizard over
// HTTP, sends a few hundred public and admin requests, waits, and compares the
// proportional set size (Pss) of the whole process tree against the committed
// baseline. Pss counts shared pages (the binary text) once across processes, so
// it is what a host's memory accounting charges.
//
// It needs Linux, a built binary (HATAMOTO_IDLEMEM_BIN, see `make budgets`) and
// a PostgreSQL server (HATAMOTO_TEST_DB_URL); without any of them it skips.
// The run uploads no media, so no image codec runtime is loaded.
package idlemem

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/budgetfile"
)

const (
	ownerEmail = "owner@idlemem.test"
	ownerPass  = "idlemem-password-123"
	// idleWait outlasts the Go runtime's forced collection (every 2 minutes
	// without one): a sign-in hashes a password in 64 MiB, and until the next
	// collection the heap keeps that room. Measured sooner, the result swings
	// by 100 MiB depending on when the last collection happened.
	idleWait = 150 * time.Second
	// capBytes is the hard ceiling no baseline may exceed.
	capBytes = 200 << 20
)

func TestBudgetIdleMemory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("idle memory is measured from /proc, Linux only")
	}
	if testing.Short() {
		t.Skip("idle memory budget takes over a minute")
	}
	bin := os.Getenv("HATAMOTO_IDLEMEM_BIN")
	if bin == "" {
		t.Skip("HATAMOTO_IDLEMEM_BIN not set (make budgets builds it)")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binary %s not found", bin)
	}
	dsn := os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HATAMOTO_TEST_DB_URL not set")
	}
	dbURL := scratchDB(t, dsn)

	port := freePort(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"DATABASE_URL=" + dbURL,
		"HATAMOTO_ADMIN_PATH=/admin",
		"API_HOST=127.0.0.1",
		"API_PORT=" + strconv.Itoa(port),
		"EMAIL_DISABLED=true",
		"HATAMOTO_DATA=" + filepath.Join(t.TempDir(), "data"),
	}
	run := func(stdin string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	srv := exec.Command(bin, "supervise")
	srv.Dir = t.TempDir()
	srv.Env = env
	var logs strings.Builder
	srv.Stdout, srv.Stderr = &logs, &logs
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killTree(srv.Process.Pid)
		_ = srv.Wait()
	})
	c := newClient(t, base)
	waitHealthy(t, c, base, &logs)

	run(ownerPass, "user", "create", "--email", ownerEmail, "--name", "Idle Owner", "--role", "owner", "--password-stdin")
	setupSite(t, c, base)
	for i := 1; i <= 10; i++ {
		run("", "entity", "create", "--schema", "post", "--name", fmt.Sprintf("Idle Post %d", i), "--publish", "--as", ownerEmail)
	}
	warm(t, c, base)

	time.Sleep(idleWait)

	pids := tree(srv.Process.Pid)
	if len(pids) < 2 {
		t.Fatalf("expected the supervisor and its serve child, got pids %v\n%s", pids, logs.String())
	}
	var total int64
	for _, pid := range pids {
		p, err := pss(pid)
		if err != nil {
			t.Fatalf("pid %d: %v", pid, err)
		}
		t.Logf("pid %d (%s): Pss %.1f MiB", pid, cmdline(pid), float64(p)/(1<<20))
		total += p
	}
	t.Logf("idle Pss of the process tree: %.1f MiB", float64(total)/(1<<20))
	budgetfile.Check(t, "idlemem", "idle_pss_bytes", total, budgetfile.Metric{
		Value: total, Unit: "bytes", TolPct: 10, Cap: capBytes,
	})
}

// scratchDB creates an empty database next to the one in dsn and drops it
// afterwards; the server migrates it on boot.
func scratchDB(t *testing.T, dsn string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("PostgreSQL not reachable: %v", err)
	}
	name := "hm_idlemem_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		// Redirects are followed by hand so a form POST's answer is visible.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func waitHealthy(t *testing.T, c *http.Client, base string, logs *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := c.Get(base + "/health"); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("server did not become healthy\n%s", logs.String())
}

// do sends one request the way a browser navigation does (the admin serves
// documents to nothing else) and follows up to five redirects. It returns the
// final status and body.
func do(t *testing.T, c *http.Client, method, target string, form url.Values) (int, string) {
	t.Helper()
	for range 6 {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, target, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Dest", "document")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			u, _ := url.Parse(target)
			req.Header.Set("Origin", u.Scheme+"://"+u.Host)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" || method != http.MethodGet {
			return resp.StatusCode, string(b)
		}
		next, err := req.URL.Parse(loc)
		if err != nil {
			t.Fatal(err)
		}
		target = next.String()
	}
	return 0, ""
}

var csrfMeta = regexp.MustCompile(`<meta name="csrf-token" content="([^"]*)"`)

func token(t *testing.T, c *http.Client, page string) string {
	t.Helper()
	_, body := do(t, c, http.MethodGet, page, nil)
	m := csrfMeta.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token on %s", page)
	}
	return m[1]
}

// setupSite signs the owner in, completes the profile (the photo is a path, not
// an upload) and finishes the setup wizard with starter content.
func setupSite(t *testing.T, c *http.Client, base string) {
	t.Helper()
	post := func(path string, form url.Values, want ...int) {
		t.Helper()
		form.Set("_csrf", token(t, c, base+"/admin"))
		got, _ := do(t, c, http.MethodPost, base+path, form)
		for _, w := range want {
			if got == w {
				return
			}
		}
		t.Fatalf("POST %s: status %d, want one of %v", path, got, want)
	}
	login := url.Values{"_csrf": {token(t, c, base+"/admin/login")}, "email": {ownerEmail}, "password": {ownerPass}}
	if got, _ := do(t, c, http.MethodPost, base+"/admin/login", login); got != http.StatusSeeOther && got != http.StatusFound {
		t.Fatalf("login: status %d", got)
	}
	post("/admin/profile/complete", url.Values{"name": {"Idle Owner"}, "photo": {"/media/idle.png"}, "public": {"false"}}, http.StatusSeeOther)
	post("/admin/setup/site", url.Values{"name": {"Idle Site"}, "url": {""}, "language": {"en"}, "timezone": {"UTC"}}, http.StatusSeeOther)
	post("/admin/setup/theme", url.Values{"colour": {"#0060df"}}, http.StatusSeeOther)
	post("/admin/setup/content", url.Values{"content": {"starter"}}, http.StatusSeeOther)
}

// warm sends 200 public and admin requests, the traffic of a small site.
func warm(t *testing.T, c *http.Client, base string) {
	t.Helper()
	public := []string{"/", "/about", "/contact", "/blog", "/robots.txt", "/sitemap.xml", "/nope", "/feed.xml"}
	admin := []string{"/admin", "/admin/media", "/admin/theme/themes", "/admin/settings", "/admin/settings/performance", "/admin/settings/general"}
	for range 12 {
		for _, p := range public {
			do(t, c, http.MethodGet, base+p, nil)
		}
		for _, p := range admin {
			do(t, c, http.MethodGet, base+p, nil)
		}
	}
}

// tree returns pid and every descendant, found through the parent pids in
// /proc (the per-task children file is not always compiled in).
func tree(pid int) []int {
	out := []int{pid}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", n))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold spaces and parentheses.
		rest := string(b[strings.LastIndexByte(string(b), ')')+1:])
		f := strings.Fields(rest)
		if len(f) > 1 && f[1] == strconv.Itoa(pid) {
			out = append(out, tree(n)...)
		}
	}
	return out
}

func killTree(pid int) {
	pids := tree(pid)
	for i := len(pids) - 1; i >= 0; i-- {
		if p, err := os.FindProcess(pids[i]); err == nil {
			_ = p.Kill()
		}
	}
}

func cmdline(pid int) string {
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	f := strings.Fields(strings.ReplaceAll(string(b), "\x00", " "))
	if len(f) > 2 {
		f = f[1:2]
	} else if len(f) > 1 {
		f = f[1:]
	}
	return strings.Join(f, " ")
}

// pss is the Pss line of /proc/<pid>/smaps_rollup, in bytes.
func pss(pid int) (int64, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 3 && fields[0] == "Pss:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			return kb << 10, err
		}
	}
	return 0, fmt.Errorf("no Pss line in smaps_rollup")
}
