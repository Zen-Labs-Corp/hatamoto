//go:build integration

package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/user"
)

const accessPassword = "an-old-password-123"

func (r *tokRig) setEnd(t *testing.T, id uuid.UUID, end *time.Time) {
	t.Helper()
	_, err := r.users.Update(database.WithSuperadmin(context.Background()), id,
		&user.UpdateUserInput{AccessEndsAt: user.AccessEnd{Set: true, At: end}})
	if err != nil {
		t.Fatal(err)
	}
}

func (r *tokRig) sessionCount(t *testing.T, id uuid.UUID) int {
	t.Helper()
	conn, err := r.db.GetConn(database.WithSuperadmin(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var n int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_sessions WHERE user_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Every way to start a session refuses an ended user with the wrong-password
// error and leaves no session row; an extension lets the user in again.
func TestAccessEndedRefusesEverySignIn(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	svc := NewService(r.users, nil, r.sessions)
	u := r.newUser(t, user.RoleEditor)
	past := time.Now().Add(-time.Minute)
	r.setEnd(t, u.ID, &past)

	_, wrongErr := svc.CheckPassword(ctx, u.Email, "not-the-password")
	if !errors.Is(wrongErr, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", wrongErr)
	}
	calls := map[string]func() error{
		"CheckPassword": func() error { _, err := svc.CheckPassword(ctx, u.Email, accessPassword); return err },
		"LoginSession":  func() error { _, _, err := svc.LoginSession(ctx, u.Email, accessPassword, "ua", ""); return err },
		"LoginAPISession": func() error {
			_, _, err := svc.LoginAPISession(ctx, u.Email, accessPassword, "", "ua", "")
			return err
		},
		"IssueSession":         func() error { _, err := svc.IssueSession(ctx, u.ID, "ua", ""); return err },
		"IssueVerifiedSession": func() error { _, err := svc.IssueVerifiedSession(ctx, u.ID, "ua", ""); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil || err.Error() != wrongErr.Error() {
			t.Errorf("%s: %v, want %v", name, err, wrongErr)
		}
	}
	if n := r.sessionCount(t, u.ID); n != 0 {
		t.Fatalf("%d sessions were created for an ended user", n)
	}
	// The refusals did not count against the lockout.
	got, _ := r.users.GetByID(database.WithSuperadmin(ctx), u.ID)
	if got.LockedUntil != nil {
		t.Error("ended sign-in locked the account")
	}

	future := time.Now().Add(time.Hour)
	r.setEnd(t, u.ID, &future)
	if _, _, err := svc.LoginSession(ctx, u.Email, accessPassword, "ua", ""); err != nil {
		t.Fatalf("after extension: %v", err)
	}
}

// A live session of a user whose end moves into the past is refused on the
// next request on every validator, before any worker runs.
func TestAccessEndedRefusesLiveSessions(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	svc := NewService(r.users, nil, r.sessions)
	u := r.newUser(t, user.RoleEditor)

	adminTok, adminSess, err := r.sessions.IssueAdmin(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}
	apiTok, _, err := r.sessions.IssueAPI(ctx, u.ID, time.Hour, "ua", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateSession(ctx, adminTok); err != nil {
		t.Fatalf("live admin session: %v", err)
	}
	if _, ok := r.sessions.LiveAdminUser(ctx, adminSess.ID); !ok {
		t.Fatal("LiveAdminUser false for an open user")
	}

	past := time.Now().Add(-time.Minute)
	r.setEnd(t, u.ID, &past)

	if _, err := svc.ValidateSession(ctx, adminTok); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("admin cookie: %v", err)
	}
	if _, err := svc.ValidateBearerSession(ctx, adminTok); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("bearer: %v", err)
	}
	if _, err := svc.ValidateAPICookieSession(ctx, apiTok); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("api cookie: %v", err)
	}
	if _, ok := r.sessions.LiveAdminUser(ctx, adminSess.ID); ok {
		t.Error("LiveAdminUser true for an ended user")
	}
	// The request path does not delete rows; the worker does.
	if n := r.sessionCount(t, u.ID); n != 2 {
		t.Errorf("sessions: %d, want 2 untouched", n)
	}

	future := time.Now().Add(time.Hour)
	r.setEnd(t, u.ID, &future)
	if _, err := svc.ValidateSession(ctx, adminTok); err != nil {
		t.Errorf("after extension: %v", err)
	}
}

func TestAccessEndedAPIKeys(t *testing.T) {
	r := newTokRig(t)
	ctx := database.WithSuperadmin(context.Background())
	keys := NewAPIKeyService(r.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ended := r.newUser(t, user.RoleEditor)
	other := r.newUser(t, user.RoleEditor)

	mk := func(by uuid.UUID) *APIKeyWithSecret {
		k, err := keys.Create(ctx, &by, &CreateAPIKeyInput{Name: "k", Scopes: []APIKeyScope{ScopeReadEntities}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = keys.Delete(ctx, k.ID) })
		return k
	}
	endedKey, otherKey := mk(ended.ID), mk(other.ID)
	if _, err := keys.ValidateKey(ctx, endedKey.PlainKey); err != nil {
		t.Fatalf("live key: %v", err)
	}

	past := time.Now().Add(-time.Minute)
	r.setEnd(t, ended.ID, &past)
	if _, err := keys.ValidateKey(ctx, endedKey.PlainKey); !errors.Is(err, ErrAPIKeyCreatorGone) {
		t.Errorf("ended creator's key: %v", err)
	}
	if _, err := keys.ValidateKey(ctx, otherKey.PlainKey); err != nil {
		t.Errorf("other creator's key: %v", err)
	}

	n, err := keys.RevokeAllForCreator(ctx, ended.ID)
	if err != nil || n != 1 {
		t.Fatalf("RevokeAllForCreator: %d %v", n, err)
	}
	got, err := keys.GetByID(ctx, endedKey.ID)
	if err != nil || got.RevokedAt == nil || got.IsActive {
		t.Fatalf("revoked key: %+v %v", got, err)
	}
	// An extension does not un-revoke.
	future := time.Now().Add(time.Hour)
	r.setEnd(t, ended.ID, &future)
	if _, err := keys.ValidateKey(ctx, endedKey.PlainKey); !errors.Is(err, ErrAPIKeyRevoked) {
		t.Errorf("after extension: %v", err)
	}
	if _, err := keys.ValidateKey(ctx, otherKey.PlainKey); err != nil {
		t.Errorf("other key untouched: %v", err)
	}
}
