//go:build integration

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
)

// recMailer records every confirmation mail.
type recMailer struct {
	mu      sync.Mutex
	calls   []sent
	signIns []email.CommentSignInData
}

func (m *recMailer) SendCommentSignIn(_ context.Context, _ string, d email.CommentSignInData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signIns = append(m.signIns, d)
	return nil
}

// lastSignIn returns the raw token of the most recent sign-in mail.
func (m *recMailer) lastSignIn(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.signIns) == 0 {
		t.Fatal("no sign-in mail was sent")
	}
	url := m.signIns[len(m.signIns)-1].URL
	return url[strings.LastIndex(url, "/")+1:]
}

func (m *recMailer) SendCommentConfirmation(_ context.Context, to string, d email.CommentConfirmationData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, sent{to, d})
	return nil
}

// last returns the raw token of the most recent mail.
func (m *recMailer) last(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		t.Fatal("no mail was sent")
	}
	url := m.calls[len(m.calls)-1].d.URL
	return url[strings.LastIndex(url, "/")+1:]
}

func (m *recMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// clock is an injectable wall clock that starts at the real time.
type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off += d
}

type idEnv struct {
	*fixture
	id    *Service
	mail  *recMailer
	clock *clock
}

func newIDEnv(t *testing.T) *idEnv {
	t.Helper()
	f := newFixture(t)
	set := comment.Defaults()
	set.Enabled = true
	if err := f.svc.SaveSettings(f.ctx, set, nil); err != nil {
		t.Fatal(err)
	}
	e := &idEnv{fixture: f, mail: &recMailer{}, clock: &clock{}}
	id, err := New(Config{
		DB: f.db, Comments: f.svc, Mailer: e.mail,
		Site:   func(context.Context) (string, string) { return "Site S", "https://example.test" },
		Secret: "integration-test-secret", Now: e.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.id = id
	return e
}

func (e *idEnv) request(c comment.Comment, addr string) (Outcome, error) {
	return e.id.RequestConfirmation(e.ctx, ConfirmRequest{Comment: c, Email: addr, EntityTitle: "Hello"})
}

// confirmDraft requests and confirms c, returning the result.
func (e *idEnv) confirmDraft(c comment.Comment, addr string) Confirmed {
	e.t.Helper()
	if out, err := e.request(c, addr); out != OutcomeCheckEmail || err != nil {
		e.t.Fatalf("request: (%v, %v)", out, err)
	}
	got, err := e.id.Confirm(e.ctx, e.mail.last(e.t))
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func TestIdentityIntegration_FourthRequestSendsNothing(t *testing.T) {
	e := newIDEnv(t)
	for i := 0; i < 4; i++ {
		c := e.draft("a@example.test", "192.0.2.1")
		if out, err := e.request(c, "a@example.test"); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("request %d: (%v, %v)", i, out, err)
		}
	}
	if n := e.mail.count(); n != 3 {
		t.Errorf("mails = %d, want 3", n)
	}
	if n := e.confirmations(); n != 3 {
		t.Errorf("confirmation rows = %d, want 3", n)
	}
}

func TestIdentityIntegration_LostCookieReconfirmKeepsTrust(t *testing.T) {
	e := newIDEnv(t)
	const addr = "a@example.test"
	d1 := e.draft(addr, "192.0.2.1")
	if err := e.svc.Vouch(e.ctx, *d1.CommenterID, comment.TrustKnown); err != nil {
		t.Fatal(err)
	}
	got1 := e.confirmDraft(d1, addr)
	if got1.Comment.Status != comment.StatusApproved {
		t.Fatalf("comment 1 status = %s, want approved", got1.Comment.Status)
	}
	c1 := e.id.Cookie(got1, true)

	d2 := e.draft(addr, "192.0.2.1")
	got2 := e.confirmDraft(d2, addr)
	if got2.CommenterID != got1.CommenterID {
		t.Errorf("commenter changed: %v -> %v", got1.CommenterID, got2.CommenterID)
	}
	var trust string
	e.scalar(&trust, `SELECT trust_level FROM hatamoto_commenters WHERE id = $1`, got2.CommenterID)
	if trust != "known" {
		t.Errorf("trust = %s, want known", trust)
	}
	if got2.Comment.Status != comment.StatusApproved {
		t.Errorf("comment 2 status = %s, want approved", got2.Comment.Status)
	}
	c2 := e.id.Cookie(got2, true)
	for name, ck := range map[string]string{"C1": c1.Value, "C2": c2.Value} {
		if id, ok := e.id.Device(e.ctx, ck); !ok || id != got1.CommenterID {
			t.Errorf("%s not valid: (%v, %v)", name, id, ok)
		}
	}
}

func TestIdentityIntegration_RevokeInvalidatesCookies(t *testing.T) {
	e := newIDEnv(t)
	const addr = "a@example.test"
	got := e.confirmDraft(e.draft(addr, "192.0.2.1"), addr)
	old := e.id.Cookie(got, false)
	if _, ok := e.id.Device(e.ctx, old.Value); !ok {
		t.Fatal("fresh cookie rejected")
	}
	if _, err := e.svc.RevokeDevices(e.ctx, got.CommenterID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.id.Device(e.ctx, old.Value); ok {
		t.Error("cookie valid after RevokeDevices")
	}
	got2 := e.confirmDraft(e.draft(addr, "192.0.2.1"), addr)
	if got2.TokenVersion <= got.TokenVersion {
		t.Errorf("token version %d, want above %d", got2.TokenVersion, got.TokenVersion)
	}
	fresh := e.id.Cookie(got2, false)
	if id, ok := e.id.Device(e.ctx, fresh.Value); !ok || id != got.CommenterID {
		t.Errorf("new cookie rejected: (%v, %v)", id, ok)
	}
	if err := e.svc.Block(e.ctx, got.CommenterID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.id.Device(e.ctx, fresh.Value); ok {
		t.Error("cookie valid after Block")
	}
}

func TestIdentityIntegration_PurgesDraftsOlderThan24h(t *testing.T) {
	e := newIDEnv(t)
	exists := func(table, col string, id any) bool {
		t.Helper()
		var n int
		e.scalar(&n, `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, id)
		return n == 1
	}
	old := e.draft("", "192.0.2.1")
	young := e.draft("", "192.0.2.2")
	pend := e.draft("", "192.0.2.3")
	e.exec(`UPDATE hatamoto_comments SET status = 'pending' WHERE id = $1`, pend.ID)

	for _, c := range []comment.Comment{old, young, pend} {
		if _, err := e.request(c, "x@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	oldTok := e.mail.calls[0].d.URL[strings.LastIndex(e.mail.calls[0].d.URL, "/")+1:]

	e.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '25 hours' WHERE id = ANY($1)`, []uuid.UUID{old.ID, pend.ID})
	e.exec(`UPDATE hatamoto_comments SET created_at = now() - interval '23 hours' WHERE id = $1`, young.ID)
	e.exec(`UPDATE hatamoto_comment_confirmations SET created_at = now() - interval '25 hours' WHERE comment_id = $1`, old.ID)
	e.exec(`UPDATE hatamoto_commenters SET last_seen_at = now() - interval '25 hours' WHERE id = $1`, *old.CommenterID)

	if err := e.id.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}
	if exists("hatamoto_comments", "id", old.ID) {
		t.Error("25 h old draft survived")
	}
	if !exists("hatamoto_comments", "id", young.ID) {
		t.Error("23 h old draft was purged")
	}
	if !exists("hatamoto_comments", "id", pend.ID) {
		t.Error("25 h old pending comment was purged")
	}
	if exists("hatamoto_comment_confirmations", "comment_id", old.ID) {
		t.Error("25 h old token survived")
	}
	if !exists("hatamoto_comment_confirmations", "comment_id", young.ID) {
		t.Error("fresh token was purged")
	}
	if exists("hatamoto_commenters", "id", *old.CommenterID) {
		t.Error("idle draft-only commenter survived")
	}
	if !exists("hatamoto_commenters", "id", *young.CommenterID) {
		t.Error("commenter with a draft was purged")
	}
	if _, err := e.id.Confirm(e.ctx, oldTok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("confirm after purge: %v, want ErrTokenInvalid", err)
	}
}

func TestIdentityIntegration_DisposableDomainRejectedNeutrally(t *testing.T) {
	e := newIDEnv(t)
	var d string
	e.scalar(&d, `SELECT value FROM hatamoto_comment_blocks WHERE kind = 'email_domain' AND source = 'seed' ORDER BY value LIMIT 1`)
	for _, addr := range []string{"x@" + d, "x@sub." + d} {
		if err := e.id.CheckEmail(e.ctx, addr); !errors.Is(err, ErrEmailNotAccepted) {
			t.Errorf("CheckEmail(%s) = %v, want ErrEmailNotAccepted", addr, err)
		}
	}
	c := e.draft("x@"+d, "192.0.2.1")
	out, err := e.request(c, "x@"+d)
	if !errors.Is(err, ErrEmailNotAccepted) || out != 0 {
		t.Errorf("request = (%v, %v)", out, err)
	}
	if e.mail.count() != 0 || e.confirmations() != 0 {
		t.Errorf("mails %d, rows %d; want none", e.mail.count(), e.confirmations())
	}
}

func TestIdentityIntegration_TokenSingleUse(t *testing.T) {
	e := newIDEnv(t)
	const addr = "a@example.test"

	c := e.draft(addr, "192.0.2.1")
	if _, err := e.request(c, addr); err != nil {
		t.Fatal(err)
	}
	tok := e.mail.last(t)
	if _, err := e.id.Confirm(e.ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := e.id.Confirm(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("second use: %v, want ErrTokenInvalid", err)
	}

	c = e.draft(addr, "192.0.2.1")
	if _, err := e.request(c, addr); err != nil {
		t.Fatal(err)
	}
	tok = e.mail.last(t)
	e.clock.advance(TokenTTL + time.Second)
	if _, err := e.id.Confirm(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expired: %v, want ErrTokenInvalid", err)
	}
	e.clock.advance(-(TokenTTL + time.Second))

	c = e.draft(addr, "192.0.2.1")
	if _, err := e.request(c, addr); err != nil {
		t.Fatal(err)
	}
	tok = e.mail.last(t)
	if _, err := e.svc.Approve(e.ctx, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.id.Confirm(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("after moderator approval: %v, want ErrTokenInvalid", err)
	}
}

func TestSignInWithoutComment(t *testing.T) {
	e := newIDEnv(t)
	signIn := func(addr string) (Outcome, error) {
		return e.id.RequestSignIn(e.ctx, SignInRequest{Email: addr, IPHash: "ip-s", NetHash: "net-s"})
	}
	// A known address (it has a comment) and an unknown one look the same.
	known := e.draft("known@example.test", "192.0.2.1")
	if known.CommenterID == nil {
		t.Fatal("no commenter")
	}
	var comments int
	e.scalar(&comments, `SELECT count(*) FROM hatamoto_comments`)
	for _, addr := range []string{"known@example.test", "new@example.test"} {
		if out, err := signIn(addr); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("%s: (%v, %v)", addr, out, err)
		}
	}
	if n := len(e.mail.signIns); n != 2 {
		t.Fatalf("sign-in mails = %d, want 2", n)
	}
	var after int
	e.scalar(&after, `SELECT count(*) FROM hatamoto_comments`)
	if after != comments {
		t.Errorf("comments %d -> %d: sign-in must not create one", comments, after)
	}

	tok := e.mail.lastSignIn(t)
	if err := e.id.PeekSignIn(e.ctx, tok); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if err := e.id.PeekSignIn(e.ctx, tok); err != nil {
		t.Fatalf("second peek (must not consume): %v", err)
	}
	if _, err := e.id.Confirm(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("sign-in token accepted by Confirm: %v", err)
	}
	got, err := e.id.ConfirmSignIn(e.ctx, tok)
	if err != nil || got.CommenterID == uuid.Nil || got.CookieLifetime <= 0 {
		t.Fatalf("ConfirmSignIn = %+v, %v", got, err)
	}
	if _, err := e.id.ConfirmSignIn(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("replay: %v", err)
	}
	if err := e.id.PeekSignIn(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("peek after use: %v", err)
	}

	// A comment token is refused by ConfirmSignIn.
	c := e.draft("known@example.test", "198.51.100.9")
	if out, err := e.request(c, "known@example.test"); out != OutcomeCheckEmail || err != nil {
		t.Fatal(out, err)
	}
	if _, err := e.id.ConfirmSignIn(e.ctx, e.mail.last(t)); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("comment token accepted by ConfirmSignIn: %v", err)
	}

	// Disposable and malformed addresses are visible errors.
	if _, err := signIn("x@mailinator.com"); !errors.Is(err, ErrEmailNotAccepted) {
		t.Errorf("disposable: %v", err)
	}
	if _, err := signIn("nope"); !errors.Is(err, ErrEmailInvalid) {
		t.Errorf("malformed: %v", err)
	}
}

func TestSignInSharesLimits(t *testing.T) {
	e := newIDEnv(t)
	for i := 0; i < 4; i++ {
		if out, err := e.id.RequestSignIn(e.ctx, SignInRequest{Email: "a@example.test"}); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("request %d: (%v, %v)", i, out, err)
		}
	}
	if n := len(e.mail.signIns); n != 3 {
		t.Errorf("sign-in mails = %d, want 3 (4th is silent)", n)
	}
	// The same budget counts confirmation mails.
	c := e.draft("a@example.test", "192.0.2.50")
	if out, err := e.request(c, "a@example.test"); out != OutcomeCheckEmail || err != nil {
		t.Fatal(out, err)
	}
	if e.mail.count() != 0 {
		t.Errorf("confirmation sent past the shared limit")
	}
}

func TestSignInAcceptsOrphanedConfirmation(t *testing.T) {
	e := newIDEnv(t)
	c := e.draft("o@example.test", "192.0.2.7")
	if out, err := e.request(c, "o@example.test"); out != OutcomeCheckEmail || err != nil {
		t.Fatal(out, err)
	}
	tok := e.mail.last(t)
	e.exec(`DELETE FROM hatamoto_comments WHERE id = $1`, c.ID) // comment_id becomes NULL
	if _, err := e.id.Confirm(e.ctx, tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("Confirm on an orphan: %v", err)
	}
	// Confirm consumed nothing it could use; the row is still unused.
	got, err := e.id.ConfirmSignIn(e.ctx, tok)
	if err != nil || got.CommenterID != *c.CommenterID {
		t.Errorf("orphan via sign-in = %+v, %v", got, err)
	}
}

func TestSignInOnlyCommenterSurvivesPurge(t *testing.T) {
	e := newIDEnv(t)
	signIn := func(addr string) {
		t.Helper()
		if out, err := e.id.RequestSignIn(e.ctx, SignInRequest{Email: addr}); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("request %s: (%v, %v)", addr, out, err)
		}
	}
	signIn("kept@example.test")
	got, err := e.id.ConfirmSignIn(e.ctx, e.mail.lastSignIn(t))
	if err != nil {
		t.Fatal(err)
	}
	signIn("never@example.test") // link never clicked
	var neverID uuid.UUID
	e.scalar(&neverID, `SELECT id FROM hatamoto_commenters WHERE id <> $1`, got.CommenterID)
	before, _, err := e.id.store.TokenVersion(e.ctx, got.CommenterID)
	if err != nil {
		t.Fatal(err)
	}

	e.clock.advance(25 * time.Hour)
	e.exec(`UPDATE hatamoto_comment_confirmations SET created_at = created_at - interval '25 hours'`)
	e.exec(`UPDATE hatamoto_commenters SET last_seen_at = last_seen_at - interval '25 hours'`)
	if err := e.id.Purge(e.ctx); err != nil {
		t.Fatal(err)
	}

	v, found, err := e.id.store.TokenVersion(e.ctx, got.CommenterID)
	if err != nil || !found || v != before {
		t.Fatalf("signed-in commenter after purge: version %d found %v err %v, want %d", v, found, err, before)
	}
	var n int
	e.scalar(&n, `SELECT count(*) FROM hatamoto_commenters WHERE id = $1`, neverID)
	if n != 0 {
		t.Error("commenter who never clicked the link survived the purge")
	}
}

func TestSignInOverLimitStoresNoCommenter(t *testing.T) {
	e := newIDEnv(t)
	count := func() (n int) {
		e.scalar(&n, `SELECT count(*) FROM hatamoto_commenters`)
		return n
	}
	for i := 0; i < DefaultLimits.PerIP; i++ {
		addr := fmt.Sprintf("u%d@example.test", i)
		if out, err := e.id.RequestSignIn(e.ctx, SignInRequest{Email: addr, IPHash: "ip-x", NetHash: "net-x"}); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("request %d: (%v, %v)", i, out, err)
		}
	}
	before := count()
	out, err := e.id.RequestSignIn(e.ctx, SignInRequest{Email: "over@example.test", IPHash: "ip-x", NetHash: "net-x"})
	if out != OutcomeCheckEmail || err != nil {
		t.Fatalf("over the limit: (%v, %v), want the neutral outcome", out, err)
	}
	if got := count(); got != before {
		t.Errorf("commenters %d -> %d: a limited request stored a row", before, got)
	}
	if n := len(e.mail.signIns); n != DefaultLimits.PerIP {
		t.Errorf("sign-in mails = %d, want %d", n, DefaultLimits.PerIP)
	}
}
