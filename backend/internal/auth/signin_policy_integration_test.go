//go:build integration

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

var viaFake = authstate.Via{Method: authstate.MethodProvider, Provider: "fake"}

// A provider sign-in gets a session for an active user and is refused, with no
// session row, for an ended or locked one.
func TestIssueVerifiedSessionProvider(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	svc := NewService(r.users, nil, r.sessions)
	u := r.newUser(t, user.RoleEditor)

	if _, _, err := svc.IssueVerifiedSession(ctx, u.ID, viaFake, "ua", ""); err != nil {
		t.Fatalf("active user: %v", err)
	}
	if n := r.sessionCount(t, u.ID); n != 1 {
		t.Fatalf("%d sessions, want 1", n)
	}

	past := time.Now().Add(-time.Minute)
	r.setEnd(t, u.ID, &past)
	if _, _, err := svc.IssueVerifiedSession(ctx, u.ID, viaFake, "ua", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ended user: %v", err)
	}
	r.setEnd(t, u.ID, nil)

	if err := r.users.RecordFailedLogin(database.WithSuperadmin(ctx), u.ID, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.IssueVerifiedSession(ctx, u.ID, viaFake, "ua", ""); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("locked user: %v", err)
	}
	// Ending access also revoked the first session; the refusals added none.
	if n := r.sessionCount(t, u.ID); n != 0 {
		t.Fatalf("%d sessions after refusals, want 0", n)
	}
}

// A policy refusing a method refuses it on every session entry point and
// leaves no session row.
func TestSignInPolicyRefusesSessionRows(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	svc := NewService(r.users, nil, r.sessions)
	u := r.newUser(t, user.RoleEditor)
	registerPolicy(t, &refuseMethods{refuse: map[authstate.Method]bool{
		authstate.MethodPassword: true, authstate.MethodProvider: true,
	}})

	calls := map[string]func() error{
		"LoginSession": func() error { _, _, err := svc.LoginSession(ctx, u.Email, "an-old-password-123", "ua", ""); return err },
		"LoginAPISession": func() error {
			_, _, err := svc.LoginAPISession(ctx, u.Email, "an-old-password-123", "", "ua", "")
			return err
		},
		"IssueSession":         func() error { _, err := svc.IssueSession(ctx, u.ID, "ua", ""); return err },
		"IssueVerifiedSession": func() error { _, _, err := svc.IssueVerifiedSession(ctx, u.ID, viaFake, "ua", ""); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: %v, want ErrInvalidCredentials", name, err)
		}
	}
	if n := r.sessionCount(t, u.ID); n != 0 {
		t.Fatalf("%d sessions were created under a refusing policy", n)
	}
	// The passkey method is not refused here.
	if _, _, err := svc.IssueVerifiedSession(ctx, u.ID, authstate.Via{Method: authstate.MethodPasskey}, "ua", ""); err != nil {
		t.Fatalf("passkey: %v", err)
	}
}
