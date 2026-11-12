//go:build integration

package auth

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

func (r *tokRig) storedHash(t *testing.T, u *user.User) *user.User {
	t.Helper()
	got, err := r.users.GetByEmail(database.WithSuperadmin(context.Background()), u.Email)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A sign-in with a legacy hash rewrites the stored hash to the current
// parameters, leaves lockout state alone and the user can sign in again.
func TestLoginRehashesLegacyHash(t *testing.T) {
	r := newTokRig(t)
	ctx := context.Background()
	svc := NewService(r.users, nil, r.sessions)
	t.Cleanup(svc.rehashes.Wait)
	u := r.newUser(t, user.RoleEditor)
	legacy := encodeWith(t, accessPassword, 65536, 1, 4)
	if err := r.users.SetPassword(database.WithSuperadmin(ctx), u.ID, legacy); err != nil {
		t.Fatal(err)
	}

	// A wrong password leaves the legacy hash alone.
	if _, _, err := svc.LoginSession(ctx, u.Email, "not-the-password", "ua", ""); err == nil {
		t.Fatal("wrong password signed in")
	}
	svc.rehashes.Wait()
	if got := r.storedHash(t, u); got.PasswordHash != legacy {
		t.Fatalf("wrong password changed the hash to %q", got.PasswordHash)
	}

	if _, _, err := svc.LoginSession(ctx, u.Email, accessPassword, "ua", ""); err != nil {
		t.Fatal(err)
	}
	svc.rehashes.Wait() // the upgrade runs after sign-in returns
	got := r.storedHash(t, u)
	if !strings.Contains(got.PasswordHash, "$m=19456,t=2,p=1$") {
		t.Fatalf("stored hash = %q, want the new parameters", got.PasswordHash)
	}
	if _, _, err := svc.LoginSession(ctx, u.Email, accessPassword, "ua", ""); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	svc.rehashes.Wait()
	// The CAS swap leaves the lockout counter alone (a successful sign-in resets it, this does not).
	if err := r.users.RecordFailedLogin(database.WithSuperadmin(ctx), u.ID, 5, 0); err != nil {
		t.Fatal(err)
	}
	counted := r.storedHash(t, u)
	if ok, err := r.users.UpdatePasswordHash(database.WithSuperadmin(ctx), u.ID, got.PasswordHash, got.PasswordHash); err != nil || !ok {
		t.Fatalf("CAS on the current hash = %v, %v", ok, err)
	}
	if after := r.storedHash(t, u); after.FailedLoginAttempts != counted.FailedLoginAttempts || counted.FailedLoginAttempts == 0 {
		t.Errorf("failed attempts %d -> %d: the swap touched lockout state", counted.FailedLoginAttempts, after.FailedLoginAttempts)
	}
	// The CAS refuses to overwrite a hash that changed meanwhile.
	ok, err := r.users.UpdatePasswordHash(database.WithSuperadmin(ctx), u.ID, legacy, "x")
	if err != nil || ok {
		t.Errorf("stale CAS = %v, %v; want false, nil", ok, err)
	}
}
