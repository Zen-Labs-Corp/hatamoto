package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/session"
)

func TestSudoPageRenders(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	rec := g.do("GET", g.a.sudoURL(g.a.url("people"), true), "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Confirm it&#39;s you", "owner@example.com", `name="password"`, `name="next" value="/admin/people"`, "submit the form again"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	a11yOK(t, body)
}

// The real service must satisfy the narrow interface the sudo handler asserts.
var _ passwordConfirmer = (*auth.Service)(nil)

// sudoAuth is an Authenticator that also confirms passwords: it models
// auth.Service for owner@example.com, locked after 3 bad attempts.
type sudoAuth struct {
	Authenticator
	mu       sync.Mutex
	fails    int
	calls    int // ConfirmPassword calls
	lastUser uuid.UUID
}

func (s *sudoAuth) ConfirmPassword(_ context.Context, userID uuid.UUID, email, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastUser = userID
	if email != "owner@example.com" {
		return auth.ErrInvalidCredentials
	}
	if s.fails >= 3 {
		return auth.ErrAccountLocked
	}
	if password != "correct-horse-battery" {
		s.fails++
		return auth.ErrInvalidCredentials
	}
	return nil
}

type fakeFactor struct {
	has    bool
	hasErr error
	code   string
	// verifyErr, when set, is returned by Verify instead of judging the code.
	verifyErr error
	// verifies counts Verify calls when set.
	verifies *atomic.Int32
	// gate, when set, makes Verify wait for it to close before it answers.
	gate chan struct{}
	// during, when set, runs inside Verify and its error is returned.
	during func(context.Context) error
}

func (f fakeFactor) Has(context.Context, uuid.UUID) (bool, error) { return f.has, f.hasErr }
func (f fakeFactor) Verify(ctx context.Context, _ uuid.UUID, c string) error {
	if f.verifies != nil {
		f.verifies.Add(1)
	}
	if f.gate != nil {
		<-f.gate
	}
	if f.during != nil {
		return f.during(ctx)
	}
	if f.verifyErr != nil {
		return f.verifyErr
	}
	if c == f.code {
		return nil
	}
	return ErrWrongCode
}

func newSudoRig(t *testing.T, mutate ...func(*Deps)) *rig {
	t.Helper()
	return newRig(t, append([]func(*Deps){func(d *Deps) { d.Auth = &sudoAuth{} }}, mutate...)...)
}

func (g *rig) elevated(token string) bool { return g.a.sudo.store.Active(g.sessions.byToken[token].ID) }

func TestSudoCorrectPasswordElevatesAndGoesToNext(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	p := g.a.url("sudo-test/page")
	sudoGuarded(g, "GET "+p)
	if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("before: %d", rec.Code)
	}
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "next": {p}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != p {
		t.Fatalf("confirm: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if !g.elevated("owner-token") {
		t.Fatal("not elevated")
	}
	if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("after: %d", rec.Code)
	}
}

func TestSudoWrongPasswordIsRefusedWithTheGenericMessage(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), sudoMessage) {
		t.Fatal("generic message missing")
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated after a wrong password")
	}
}

func TestSudoLockedAccountLooksLikeAWrongPassword(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	wrong := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}}, nil)
	for range 3 {
		g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}}, nil)
	}
	// Locked now: even the right password is refused, with the same page.
	locked := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}}, nil)
	if locked.Code != wrong.Code || !strings.Contains(locked.Body.String(), sudoMessage) {
		t.Fatalf("locked: %d", locked.Code)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated while locked")
	}
	if strings.Contains(locked.Body.String(), "locked") {
		t.Fatal("the lockout is visible in the page")
	}
}

func TestSudoNextAtTheSudoPageGoesHome(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	for _, next := range []string{g.a.sudoPath(), "https://evil.example/", ""} {
		rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "next": {next}}, nil)
		if rec.Header().Get("Location") != g.a.url() {
			t.Errorf("next %q went to %q, want home", next, rec.Header().Get("Location"))
		}
	}
	// A sudo link that wraps a target goes to the target, never back to sudo.
	wrapped := g.a.sudoURL(g.a.url("people"), false)
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "next": {wrapped}}, nil)
	if got := rec.Header().Get("Location"); got != g.a.url("people") {
		t.Errorf("next %q went to %q, want the people page", wrapped, got)
	}
}

func TestSudoRateLimiterWrapsGetAndPost(t *testing.T) {
	t.Parallel()

	calls := 0
	g := newSudoRig(t, func(d *Deps) {
		d.RateLimit = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; next.ServeHTTP(w, r) })
		}
	})
	g.do("GET", g.a.sudoPath(), "owner-token", nil, nil)
	g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}}, nil)
	if calls != 2 {
		t.Fatalf("limiter ran %d times, want 2", calls)
	}
}

func TestSudoCodeFieldFollowsTheProvider(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	const field = "Code from your app"
	if body := g.do("GET", g.a.sudoPath(), "owner-token", nil, nil).Body.String(); strings.Contains(body, field) {
		t.Fatal("code field without a provider")
	}
	g.a.SetSecondFactor(fakeFactor{has: false})
	if body := g.do("GET", g.a.sudoPath(), "owner-token", nil, nil).Body.String(); strings.Contains(body, field) {
		t.Fatal("code field for a user without a factor")
	}
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456"})
	rec := g.do("GET", g.a.sudoPath(), "owner-token", nil, nil)
	if !strings.Contains(rec.Body.String(), field) {
		t.Fatal("code field missing for a user with a factor")
	}
	a11yOK(t, rec.Body.String())
}

func TestSudoWrongCodeIsRefusedAndRightCodeElevates(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456"})
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "code": {"000000"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), sudoMessage) {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated on a wrong code")
	}
	rec = g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "code": {"123456"}}, nil)
	if rec.Code != http.StatusSeeOther || !g.elevated("owner-token") {
		t.Fatalf("right code: %d, elevated %v", rec.Code, g.elevated("owner-token"))
	}
}

func TestSudoWithoutPasswordConfirmerIs503(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.Auth = &fakeAuth{} })
	if rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"x"}}, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated")
	}
}

// A provider that cannot say whether the user has a factor must not let the
// password alone elevate, and the page keeps asking for a code.
func TestSudoFactorLookupErrorRefuses(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{hasErr: errors.New("store down")})
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), sudoMessage) {
		t.Fatalf("lookup error: %d, want 422 with the generic message", rec.Code)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated although the second factor lookup failed")
	}
	if body := g.do("GET", g.a.sudoPath(), "owner-token", nil, nil).Body.String(); !strings.Contains(body, "Code from your app") {
		t.Error("the page must keep the code field when the lookup fails")
	}
}

func TestSudoSessionWithoutUserIsRefused(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	r := httptest.NewRequest("POST", g.a.sudoPath(), nil)
	if err := g.a.checkSecondFactor(r, &session.Session{}); err == nil {
		t.Fatal("a session without a user passed the second factor check")
	}
}

func TestSudoWrongCodesLockTheSessionAndSkipThePasswordCheck(t *testing.T) {
	t.Parallel()

	au := &sudoAuth{}
	g := newSudoRig(t, func(d *Deps) { d.Auth = au })
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456"})
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g.a.sudo.store.now = clock.now
	post := func(code string) *httptest.ResponseRecorder {
		return g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "code": {code}}, nil)
	}
	for i := range sudoMaxCodeFails {
		if rec := post("000000"); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("wrong code %d: %d", i, rec.Code)
		}
	}
	callsBefore := au.calls
	rec := post("123456")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), sudoMessage) || g.elevated("owner-token") {
		t.Fatalf("locked session with the right code: %d, elevated %v", rec.Code, g.elevated("owner-token"))
	}
	if au.calls != callsBefore {
		t.Error("the password was checked while the session was locked")
	}
	clock.t = clock.t.Add(sudoFailWindow)
	if rec := post("123456"); rec.Code != http.StatusSeeOther || !g.elevated("owner-token") {
		t.Fatalf("after the lockout: %d, elevated %v", rec.Code, g.elevated("owner-token"))
	}
}

func TestSudoResubmitNoticeSurvivesAWrongPassword(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	const notice = "submit the form again"
	page := g.do("GET", g.a.sudoPath()+"?resubmit=1&next=%2Fadmin%2Fpeople", "owner-token", nil, nil).Body.String()
	if !strings.Contains(page, notice) || !strings.Contains(page, `name="resubmit" value="1"`) {
		t.Fatalf("the page lacks the notice or the hidden field:\n%s", page)
	}
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}, "resubmit": {"1"}, "next": {"/admin/people"}}, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, sudoMessage) || !strings.Contains(body, notice) {
		t.Fatalf("the re-render must show the error and keep the notice: %d\n%s", rec.Code, body)
	}
	if !strings.Contains(body, `name="resubmit" value="1"`) {
		t.Error("the re-render must carry the flag again")
	}
	a11yOK(t, body)
	if plain := g.do("GET", g.a.sudoPath(), "owner-token", nil, nil).Body.String(); strings.Contains(plain, notice) || strings.Contains(plain, `name="resubmit"`) {
		t.Error("a plain visit must not show the notice")
	}
}

func TestSudoCodeFieldIsTiedToTheStatus(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456"})
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "code": {"000000"}}, nil)
	body := rec.Body.String()
	i := strings.Index(body, `id="code"`)
	if i < 0 {
		t.Fatal("no code field")
	}
	start := strings.LastIndex(body[:i], "<input")
	end := i + strings.Index(body[i:], ">")
	if input := body[start:end]; !strings.Contains(input, `aria-describedby="sudo-status"`) || !strings.Contains(body, `id="sudo-status"`) {
		t.Errorf("the code input must point at the status line: %s", input)
	}
	a11yOK(t, body)
}

// The password is confirmed against the session's own user, not just an email.
func TestSudoConfirmsAgainstTheSessionUser(t *testing.T) {
	t.Parallel()

	au := &sudoAuth{}
	g := newSudoRig(t, func(d *Deps) { d.Auth = au })
	g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}}, nil)
	if want := *g.sessions.byToken["owner-token"].UserID; au.lastUser != want {
		t.Errorf("ConfirmPassword got user %v, want the session user %v", au.lastUser, want)
	}
}

// Parallel requests on one session must not all pass the lock check and guess:
// the attempt is reserved before the slow checks.
func TestSudoParallelRequestsRunAtMostFiveCodeVerifications(t *testing.T) {
	t.Parallel()

	const n = 40
	g := newSudoRig(t)
	verifies := new(atomic.Int32)
	gate := make(chan struct{})
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", verifies: verifies, gate: gate})
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g.a.sudo.store.now = clock.now
	form := url.Values{"password": {"correct-horse-battery"}, "code": {"000000"}}

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.do("POST", g.a.sudoPath(), "owner-token", form, nil)
		}()
	}
	// Let the attempts that were admitted reach Verify, and the rest be refused.
	deadline := time.Now().Add(10 * time.Second)
	for verifies.Load() < sudoMaxCodeFails && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := verifies.Load(); got != sudoMaxCodeFails {
		t.Fatalf("%d code verifications in one window, want %d", got, sudoMaxCodeFails)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated on wrong codes")
	}
	// The window passing frees the session again.
	clock.t = clock.t.Add(sudoFailWindow)
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"correct-horse-battery"}, "code": {"123456"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("after the window: %d", rec.Code)
	}
}

// Only a definite wrong code counts. A provider error answers 503 and leaves
// the cap untouched.
func TestSudoProviderErrorIsUnavailableAndDoesNotCount(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	verifies := new(atomic.Int32)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", verifyErr: errors.New("provider timeout"), verifies: verifies})
	form := url.Values{"password": {"correct-horse-battery"}, "code": {"123456"}}
	for i := range 2 * sudoMaxCodeFails {
		rec := g.do("POST", g.a.sudoPath(), "owner-token", form, nil)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), sudoUnavailableMessage) {
			t.Fatalf("provider error %d: %d", i, rec.Code)
		}
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated on a provider error")
	}
	// The provider recovers: the session was never locked.
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456"})
	if rec := g.do("POST", g.a.sudoPath(), "owner-token", form, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("after recovery: %d", rec.Code)
	}
}

func TestSudoWrappedWrongCodeCounts(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", verifyErr: fmt.Errorf("totp: %w", ErrWrongCode)})
	form := url.Values{"password": {"correct-horse-battery"}, "code": {"1"}}
	for range sudoMaxCodeFails {
		if rec := g.do("POST", g.a.sudoPath(), "owner-token", form, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if !g.a.sudo.store.codeCapReached(g.sessions.byToken["owner-token"].ID) {
		t.Fatal("wrapped wrong codes were not counted")
	}
}

// postCancelled sends a code POST whose context is cancelled by cancel.
func postCancelled(g *rig, ctx context.Context) {
	req := httptest.NewRequest("POST", g.a.sudoPath(), strings.NewReader(url.Values{"password": {"correct-horse-battery"}, "code": {"000000"}}.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", nil)
	g.h.ServeHTTP(httptest.NewRecorder(), req)
}

// A request that dies before the provider decides gives its attempt back.
func TestSudoCancelledRequestDoesNotCount(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", during: func(ctx context.Context) error { return ctx.Err() }})
	id := g.sessions.byToken["owner-token"].ID
	for range 2 * sudoMaxCodeFails {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		postCancelled(g, ctx)
	}
	if g.a.sudo.store.codeCapReached(id) || !g.a.sudo.store.BeginCode(id) {
		t.Fatal("cancelled requests counted toward the cap")
	}
}

// Cancelling after the provider said "wrong code" must not make guesses free.
func TestSudoCancelledAfterWrongCodeStillCounts(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	var cancel context.CancelFunc
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", during: func(context.Context) error {
		cancel()
		return ErrWrongCode
	}})
	id := g.sessions.byToken["owner-token"].ID
	for range sudoMaxCodeFails {
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		postCancelled(g, ctx)
		cancel()
	}
	if !g.a.sudo.store.codeCapReached(id) || g.a.sudo.store.BeginCode(id) {
		t.Fatal("cancelled wrong codes were not counted: the 6th attempt was allowed")
	}
}

// A provider panic must not leak the reserved attempt.
func TestSudoProviderPanicReleasesAttempt(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", during: func(context.Context) error { panic("provider exploded") }})
	id := g.sessions.byToken["owner-token"].ID
	for range 2 * sudoMaxCodeFails {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected the panic to propagate")
				}
			}()
			postCancelled(g, context.Background())
		}()
	}
	if g.a.sudo.store.codeCapReached(id) || !g.a.sudo.store.BeginCode(id) {
		t.Fatal("panicking provider leaked attempts")
	}
}

// A wrong password never reaches the code, even with the right one.
func TestSudoWrongPasswordWithRightCodeNeverVerifiesTheCode(t *testing.T) {
	t.Parallel()

	g := newSudoRig(t)
	verifies := new(atomic.Int32)
	g.a.SetSecondFactor(fakeFactor{has: true, code: "123456", verifies: verifies})
	rec := g.do("POST", g.a.sudoPath(), "owner-token", url.Values{"password": {"nope"}, "code": {"123456"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), sudoMessage) {
		t.Fatalf("status %d", rec.Code)
	}
	if g.elevated("owner-token") {
		t.Fatal("elevated on a wrong password")
	}
	if verifies.Load() != 0 {
		t.Fatal("the code was verified after a wrong password")
	}
}
