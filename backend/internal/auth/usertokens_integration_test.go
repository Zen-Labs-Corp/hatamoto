//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

type tokRig struct {
	db       *database.DB
	tok      *UserTokens
	users    *user.PostgresRepository
	sessions *session.Store
}

func newTokRig(t *testing.T) *tokRig {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := database.New(context.Background(), config.DatabaseConfig{URL: dsn, MaxConnections: 8, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return &tokRig{db: db, tok: NewUserTokens(db), users: user.NewPostgresRepository(db), sessions: session.NewStore(db)}
}

func (r *tokRig) newUser(t *testing.T, role user.Role) *user.User {
	t.Helper()
	ctx := database.WithSuperadmin(context.Background())
	hash, err := HashPassword("an-old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	email := "tok-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12] + "@example.test"
	u, err := r.users.Create(ctx, &user.CreateUserInput{Email: email, Role: role, Name: "T"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.users.Delete(ctx, u.ID) })
	return u
}

func TestResetFlow(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	if err := r.users.RecordFailedLogin(database.WithSuperadmin(ctx), u.ID, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	sessTok, _, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}

	token, err := r.tok.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.tok.ResetValid(ctx, token) || r.tok.ResetValid(ctx, "nope") {
		t.Fatal("ResetValid")
	}
	// A too-short password is refused and does not burn the token.
	if _, err := r.tok.ConsumeReset(ctx, token, "short"); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short password: %v", err)
	}
	got, err := r.tok.ConsumeReset(ctx, token, "a-brand-new-password")
	if err != nil || got.ID != u.ID {
		t.Fatalf("consume: %v", err)
	}
	if _, err := r.tok.ConsumeReset(ctx, token, "another-new-password"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("second use: %v", err)
	}
	after, err := r.users.GetByEmail(database.WithSuperadmin(ctx), u.Email)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword("a-brand-new-password", after.PasswordHash); !ok {
		t.Error("password not changed")
	}
	if after.LockedUntil != nil || after.FailedLoginAttempts != 0 {
		t.Errorf("lockout not cleared: %+v", after)
	}
	if _, err := r.sessions.Validate(ctx, sessTok); err == nil {
		t.Error("old session survived a password reset")
	}
}

func TestResetRaceExactlyOneWins(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleOwner)
	token, err := r.tok.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.tok.ConsumeReset(ctx, token, "race-password-12345"); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrTokenInvalid) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent consumers succeeded, want exactly 1", wins.Load())
	}
}

func TestResetExpiryReplacementAndThrottle(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)

	first, err := r.tok.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.tok.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.tok.ResetValid(ctx, first) || !r.tok.ResetValid(ctx, second) {
		t.Fatal("a new reset must invalidate the earlier one")
	}
	if _, err := r.tok.IssueReset(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tok.IssueReset(ctx, u.ID); !errors.Is(err, ErrResetThrottle) {
		t.Fatalf("4th request in an hour: %v", err)
	}

	// Expiry, with a fake clock.
	late := *r.tok
	late.now = func() time.Time { return time.Now().Add(ResetTokenTTL + time.Minute) }
	if late.ResetValid(ctx, second) {
		t.Error("token valid after its TTL")
	}
	if _, err := late.ConsumeReset(ctx, second, "after-expiry-password"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("expired token: %v", err)
	}
	if _, err := r.tok.ConsumeReset(ctx, "totally-unknown-token", "some-long-password-1"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("unknown token: %v", err)
	}
}

func TestInviteFlow(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	owner := r.newUser(t, user.RoleOwner)
	email := "invitee-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10] + "@example.test"
	t.Cleanup(func() {
		c := database.WithSuperadmin(ctx)
		if u, err := r.users.GetByEmail(c, email); err == nil {
			_ = r.users.Delete(c, u.ID)
		}
	})

	if _, _, err := r.tok.IssueInvite(ctx, owner.Email, user.RoleEditor, owner.ID); !errors.Is(err, user.ErrEmailAlreadyExists) {
		t.Fatalf("existing email: %v", err)
	}
	if _, _, err := r.tok.IssueInvite(ctx, email, "admin", owner.ID); !errors.Is(err, user.ErrInvalidRole) {
		t.Fatalf("bad role: %v", err)
	}
	old, _, err := r.tok.IssueInvite(ctx, strings.ToUpper(email), user.RoleEditor, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	token, inv, err := r.tok.IssueInvite(ctx, email, user.RoleEditor, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.tok.PeekInvite(ctx, old); !errors.Is(err, ErrTokenInvalid) {
		t.Error("re-inviting must replace the earlier invite")
	}
	list, err := r.tok.ListInvites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range list {
		if i.ID == inv.ID && i.Email == email && i.Role == user.RoleEditor {
			found = true
		}
	}
	if !found {
		t.Fatalf("invite missing from the list: %+v", list)
	}
	peek, err := r.tok.PeekInvite(ctx, token)
	if err != nil || peek.Email != email || peek.Role != user.RoleEditor {
		t.Fatalf("peek: %+v %v", peek, err)
	}
	if _, err := r.tok.ConsumeInvite(ctx, token, "Eve", "short"); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short: %v", err)
	}
	u, err := r.tok.ConsumeInvite(ctx, token, "Eve", "invited-password-123")
	if err != nil || u.Email != email || u.Role != user.RoleEditor || u.Name == nil || *u.Name != "Eve" {
		t.Fatalf("consume: %+v %v", u, err)
	}
	if _, err := r.tok.ConsumeInvite(ctx, token, "Eve", "invited-password-123"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("second use: %v", err)
	}
	got, err := r.users.GetByEmail(database.WithSuperadmin(ctx), email)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword("invited-password-123", got.PasswordHash); !ok {
		t.Error("invited user cannot sign in with the chosen password")
	}

	// Revoke
	t2, inv2, err := r.tok.IssueInvite(ctx, "x"+email, user.RoleOwner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.tok.RevokeInvite(ctx, inv2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.tok.PeekInvite(ctx, t2); !errors.Is(err, ErrTokenInvalid) {
		t.Error("revoked invite still valid")
	}
}

func TestPurgeExpiredAndShapeConstraint(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	if _, err := r.tok.IssueReset(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	future := *r.tok
	future.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }
	n, err := future.PurgeExpired(ctx)
	if err != nil || n < 1 {
		t.Fatalf("purge removed %d (%v)", n, err)
	}
	// The shape constraint refuses a reset token without a user.
	_, err = r.db.Pool().Exec(ctx, `INSERT INTO hatamoto_user_tokens (kind, token_hash, expires_at) VALUES ('password_reset', '\xdeadbeef', now())`)
	if err == nil {
		t.Error("shape constraint did not fire")
	}
}

func TestLoginSessionAndLockout(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	svc := NewService(r.users, nil, r.sessions)

	got, token, err := svc.LoginSession(ctx, u.Email, "an-old-password-123", "ua", "203.0.113.1")
	if err != nil || got.ID != u.ID || token == "" || got.PasswordHash != "" {
		t.Fatalf("login: %+v %q %v", got, token, err)
	}
	claims, err := svc.ValidateSession(ctx, token)
	if err != nil || claims.UserID != u.ID {
		t.Fatalf("session: %v", err)
	}
	// Five bad passwords lock the account; the good one is then refused too, with
	// the same errors Login gives.
	for i := 0; i < 5; i++ {
		if _, _, err := svc.LoginSession(ctx, u.Email, "wrong", "", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := svc.LoginSession(ctx, u.Email, "an-old-password-123", "", ""); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked: %v", err)
	}
	if _, _, err := svc.LoginSession(ctx, "nobody@example.test", "x", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
}

// Many people racing through setup: exactly one becomes the owner.
func TestCreateFirstOwnerRace(t *testing.T) {
	r := newTokRig(t)
	ctx := database.WithSuperadmin(context.Background())
	var existing int
	if err := r.db.Pool().QueryRow(ctx, `SELECT count(*) FROM hatamoto_users`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	b := NewBootstrap(r.db)
	if existing > 0 {
		// A shared dev database already has users: the call must refuse.
		if _, err := b.CreateFirstOwner(ctx, "X", "x@example.test", "a-long-enough-password"); !errors.Is(err, ErrSetupDone) {
			t.Fatalf("with users present: %v", err)
		}
		return
	}
	var wins, done atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := b.CreateFirstOwner(ctx, "Owner", "owner"+string(rune('a'+i))+"@example.test", "a-long-enough-password")
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrSetupDone):
				done.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	t.Cleanup(func() {
		_, _ = r.db.Pool().Exec(ctx, `DELETE FROM hatamoto_users WHERE email LIKE 'owner_@example.test'`)
	})
	if wins.Load() != 1 || done.Load() != 5 {
		t.Fatalf("%d winners, %d refused; want 1 and 5", wins.Load(), done.Load())
	}
}

func TestChangePassword(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	keep, _, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "keep", "")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "other", "")
	if err != nil {
		t.Fatal(err)
	}
	keepSess, err := r.sessions.Validate(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.tok.ChangePassword(ctx, u.ID, "wrong-current-pw", "a-brand-new-password", &keepSess.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := r.tok.ChangePassword(ctx, u.ID, "an-old-password-123", "short", &keepSess.ID); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("short: %v", err)
	}
	if err := r.tok.ChangePassword(ctx, u.ID, "an-old-password-123", "a-brand-new-password", &keepSess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.sessions.Validate(ctx, keep); err != nil {
		t.Error("the caller's own session must survive")
	}
	if _, err := r.sessions.Validate(ctx, other); err == nil {
		t.Error("other sessions must be signed out")
	}
	got, _ := r.users.GetByEmail(database.WithSuperadmin(ctx), u.Email)
	if ok, _ := VerifyPassword("a-brand-new-password", got.PasswordHash); !ok {
		t.Error("password not changed")
	}
}

// An idle session is deleted by Validate, so a second request cannot find it
// "freshly used" (Validate touches last_used_at on success), and the password
// change throttle counts wrong guesses.
func TestIdleSessionIsRevokedByValidate(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleOwner)
	tok, sess, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.sessions.Validate(ctx, tok); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	// The touch from that Validate runs in the background and may land before
	// or after the next statement. It is conditional on the last_used_at it
	// saw, so either order leaves the session idle: no sleep needed.
	conn, err := r.db.GetConn(database.WithSuperadmin(ctx))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `UPDATE hatamoto_sessions SET last_used_at = NOW() - interval '8 days' WHERE id = $1`, sess.ID)
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.sessions.Validate(ctx, tok); err == nil {
			t.Fatalf("attempt %d: an idle session validated", i)
		}
	}
	if _, err := r.sessions.Validate(ctx, tok); err == nil {
		t.Fatal("the idle session came back to life")
	}
}

func TestChangePasswordGuessesLockTheAccount(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	for i := 0; i < maxFailedLogins; i++ {
		if err := r.tok.ChangePassword(ctx, u.ID, "wrong-guess-"+uuid.NewString(), "a-new-password-123", nil); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	if err := r.tok.ChangePassword(ctx, u.ID, "an-old-password-123", "a-new-password-123", nil); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("after %d wrong guesses even the right password must wait: %v", maxFailedLogins, err)
	}
}

// With every hash slot busy, junk tokens and a cancelled request must return
// at once: nothing queues for a 64 MB hash it cannot use.
func TestTokenFlowsDoNotQueueUselessHashes(t *testing.T) {
	r := newTokRig(t)
	u := r.newUser(t, user.RoleEditor)
	token, err := r.tok.IssueReset(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range maxConcurrentHashes {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range maxConcurrentHashes {
			<-hashSlots
		}
	}()
	within := func(name string, f func(ctx context.Context) error, want error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- f(ctx) }()
		select {
		case err := <-done:
			if !errors.Is(err, want) {
				t.Errorf("%s: %v, want %v", name, err, want)
			}
		case <-time.After(time.Second):
			t.Errorf("%s waited for a hash slot", name)
		}
	}
	within("junk reset", func(ctx context.Context) error {
		_, err := r.tok.ConsumeReset(ctx, "junk-token", "a-long-enough-password")
		return err
	}, ErrTokenInvalid)
	within("junk invite", func(ctx context.Context) error {
		_, err := r.tok.ConsumeInvite(ctx, "junk-token", "N", "a-long-enough-password")
		return err
	}, ErrTokenInvalid)

	// A valid token with a cancelled request frees the slot wait and keeps the token.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.tok.ConsumeReset(cctx, token, "a-long-enough-password"); err == nil {
		t.Fatal("cancelled reset succeeded")
	}
	if !r.tok.ResetValid(context.Background(), token) {
		t.Error("a cancelled reset burned the token")
	}
	within("change password", func(context.Context) error {
		return r.tok.ChangePassword(cctx, u.ID, "an-old-password-123", "a-long-enough-password", nil)
	}, context.Canceled)
}

// The API login issues a kind-api session; the cookie validator accepts only
// that kind and the bearer validator accepts both kinds.
func TestAPISessionKindValidators(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	u := r.newUser(t, user.RoleEditor)
	svc := NewService(r.users, nil, r.sessions)

	got, apiTok, err := svc.LoginAPISession(ctx, u.Email, "an-old-password-123", "ua", "203.0.113.1")
	if err != nil || got.ID != u.ID || apiTok == "" {
		t.Fatalf("api login: %v", err)
	}
	sess, err := r.sessions.Validate(ctx, apiTok)
	if err != nil || sess.Kind != session.KindAPI {
		t.Fatalf("api login kind = %v, err %v, want api", sess, err)
	}
	adminTok, _, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.ValidateAPICookieSession(ctx, apiTok); err != nil {
		t.Errorf("cookie validator rejected an api session: %v", err)
	}
	if _, err := svc.ValidateAPICookieSession(ctx, adminTok); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("cookie validator on an admin session: %v, want ErrInvalid", err)
	}
	for name, tok := range map[string]string{"admin": adminTok, "api": apiTok} {
		if _, err := svc.ValidateBearerSession(ctx, tok); err != nil {
			t.Errorf("bearer validator rejected a %s session: %v", name, err)
		}
	}
	if _, err := svc.ValidateSession(ctx, adminTok); err != nil {
		t.Errorf("ValidateSession rejected an admin session: %v", err)
	}
	if _, err := svc.ValidateSession(ctx, apiTok); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("ValidateSession on an api session: %v, want ErrInvalid", err)
	}

	resp, err := svc.Login(ctx, &LoginRequest{Email: u.Email, Password: "an-old-password-123"}, "ua", "")
	if err != nil {
		t.Fatal(err)
	}
	if s, err := r.sessions.Validate(ctx, resp.SessionToken); err != nil || s.Kind != session.KindAPI {
		t.Errorf("Login kind = %v, err %v, want api", s, err)
	}
}

// Password reset, password change and RevokeAllForUser all remove api sessions.
func TestAPISessionsRevokedOnCredentialChange(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	issue := func(u *user.User) string {
		t.Helper()
		tok, _, err := r.sessions.IssueAPI(ctx, u.ID, time.Hour, "ua", "")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	gone := func(what, tok string) {
		t.Helper()
		if _, err := r.sessions.Validate(ctx, tok); err == nil {
			t.Errorf("api session survived %s", what)
		}
	}

	u := r.newUser(t, user.RoleEditor)
	tok := issue(u)
	reset, err := r.tok.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.tok.ConsumeReset(ctx, reset, "a-brand-new-password"); err != nil {
		t.Fatal(err)
	}
	gone("a password reset", tok)

	u = r.newUser(t, user.RoleEditor)
	keep, _, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "keep", "")
	if err != nil {
		t.Fatal(err)
	}
	keepSess, err := r.sessions.Validate(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	tok = issue(u)
	if err := r.tok.ChangePassword(ctx, u.ID, "an-old-password-123", "a-brand-new-password", &keepSess.ID); err != nil {
		t.Fatal(err)
	}
	gone("a password change that keeps one session", tok)

	u = r.newUser(t, user.RoleEditor)
	tok = issue(u)
	if err := r.tok.ChangePassword(ctx, u.ID, "an-old-password-123", "a-brand-new-password", nil); err != nil {
		t.Fatal(err)
	}
	gone("a password change without a kept session", tok)

	u = r.newUser(t, user.RoleEditor)
	tok = issue(u)
	if err := r.sessions.RevokeAllForUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	gone("RevokeAllForUser", tok)
}
