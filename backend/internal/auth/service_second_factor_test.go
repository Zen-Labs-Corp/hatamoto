package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

type fakeGate struct {
	has bool
	err error
}

func (g fakeGate) HasAnyFactor(context.Context, uuid.UUID) (bool, error) { return g.has, g.err }

type fakeCodes struct {
	good   string
	err    error // returned instead of a verdict
	called int
}

func (c *fakeCodes) Verify(_ context.Context, _ uuid.UUID, code string) error {
	c.called++
	if c.err != nil {
		return c.err
	}
	if code != c.good || code == "" {
		return twofactor.ErrWrongCode
	}
	return nil
}

func factorRig(t *testing.T, gate SecondFactorGate, codes CodeChecker) (*Service, *mockUserRepo, *user.User) {
	t.Helper()
	hash, err := HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := newTestUser(user.RoleOwner, hash)
	repo := &mockUserRepo{getByEmailReturn: u}
	svc := NewService(repo, nil, nil) // no session store: a passed gate ends in "sessions are not configured"
	svc.SetSecondFactor(gate, codes)
	return svc, repo, u
}

// passed is true when the call got past every second-factor check (it then
// stops at the missing session store, which the unit tests do not have).
func passed(err error) bool {
	return err != nil && err.Error() == "sessions are not configured"
}

func TestSessionIssuersCloseForFactorUsers(t *testing.T) {
	ctx := context.Background()
	const pw = "correct-password"
	good := &fakeCodes{good: "123456"}

	type issuer struct {
		name string
		call func(svc *Service, u *user.User) error
	}
	issuers := []issuer{
		{"LoginSession", func(s *Service, u *user.User) error {
			_, _, err := s.LoginSession(ctx, u.Email, pw, "ua", "ip")
			return err
		}},
		{"IssueSession", func(s *Service, u *user.User) error {
			_, err := s.IssueSession(ctx, u.ID, "ua", "ip")
			return err
		}},
		{"LoginAPISession without code", func(s *Service, u *user.User) error {
			_, _, err := s.LoginAPISession(ctx, u.Email, pw, "", "ua", "ip")
			return err
		}},
		{"LoginAPISession wrong code", func(s *Service, u *user.User) error {
			_, _, err := s.LoginAPISession(ctx, u.Email, pw, "000000", "ua", "ip")
			return err
		}},
		{"Login without code", func(s *Service, u *user.User) error {
			_, err := s.Login(ctx, &LoginRequest{Email: u.Email, Password: pw}, "ua", "ip")
			return err
		}},
	}
	for _, is := range issuers {
		t.Run(is.name+"/factor user is refused", func(t *testing.T) {
			svc, _, u := factorRig(t, fakeGate{has: true}, &fakeCodes{good: "123456"})
			err := is.call(svc, u)
			if err == nil || passed(err) {
				t.Fatalf("a factor user got past the gate: %v", err)
			}
			if !errors.Is(err, ErrSecondFactorRequired) && !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("err = %v", err)
			}
		})
		t.Run(is.name+"/no factor passes as before", func(t *testing.T) {
			svc, _, u := factorRig(t, fakeGate{has: false}, good)
			if err := is.call(svc, u); !passed(err) {
				t.Fatalf("err = %v, want to reach the session store", err)
			}
		})
		t.Run(is.name+"/gate error fails closed", func(t *testing.T) {
			svc, _, u := factorRig(t, fakeGate{err: errors.New("db down")}, good)
			if err := is.call(svc, u); err == nil || passed(err) {
				t.Fatalf("a gate error issued a session: %v", err)
			}
		})
		t.Run(is.name+"/nil gate keeps today's behaviour", func(t *testing.T) {
			svc, _, u := factorRig(t, nil, nil)
			if err := is.call(svc, u); !passed(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoginAPISessionWithCode(t *testing.T) {
	ctx := context.Background()
	codes := &fakeCodes{good: "123456"}
	svc, repo, u := factorRig(t, fakeGate{has: true}, codes)

	if _, _, err := svc.LoginAPISession(ctx, u.Email, "correct-password", "123456", "ua", "ip"); !passed(err) {
		t.Fatalf("right code: %v", err)
	}
	if codes.called != 1 {
		t.Fatalf("verify called %d times", codes.called)
	}
	if _, _, err := svc.LoginAPISession(ctx, u.Email, "correct-password", "", "ua", "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty code = %v", err)
	}
	if codes.called != 2 {
		t.Fatal("an empty code must still be counted by the checker")
	}
	// A checker that cannot decide is an error, not a pass and not "wrong".
	svc2, _, u2 := factorRig(t, fakeGate{has: true}, &fakeCodes{err: fmt.Errorf("db down")})
	_, _, err := svc2.LoginAPISession(ctx, u2.Email, "correct-password", "123456", "ua", "ip")
	if err == nil || passed(err) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("undecided = %v", err)
	}
	// A factor user with no checker wired is refused.
	svc3, _, u3 := factorRig(t, fakeGate{has: true}, nil)
	if _, _, err := svc3.LoginAPISession(ctx, u3.Email, "correct-password", "123456", "ua", "ip"); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("no checker = %v", err)
	}
	_ = repo
}

func TestPasswordAloneDoesNotResetFailuresForFactorUser(t *testing.T) {
	ctx := context.Background()
	svc, repo, u := factorRig(t, fakeGate{has: true}, &fakeCodes{good: "1"})
	got, err := svc.CheckPassword(ctx, u.Email, "correct-password")
	if err != nil || got.ID != u.ID || got.PasswordHash != "" {
		t.Fatalf("CheckPassword = %+v %v", got, err)
	}
	if repo.resetFailedCalled {
		t.Fatal("password alone cleared the failure counter of a factor user")
	}

	// Without a factor the reset still happens.
	svc2, repo2, u2 := factorRig(t, fakeGate{has: false}, nil)
	if _, err := svc2.CheckPassword(ctx, u2.Email, "correct-password"); err != nil {
		t.Fatal(err)
	}
	if !repo2.resetFailedCalled {
		t.Fatal("no-factor user must reset as before")
	}
}

func TestCheckPasswordKeepsLockoutAndRefusesBadPassword(t *testing.T) {
	ctx := context.Background()
	svc, repo, u := factorRig(t, fakeGate{has: true}, nil)
	if _, err := svc.CheckPassword(ctx, u.Email, "nope"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad password = %v", err)
	}
	if !repo.recordFailedCalled {
		t.Fatal("a bad password must be recorded")
	}
	svc2, _, u2 := factorRig(t, fakeGate{err: errors.New("down")}, nil)
	if _, err := svc2.CheckPassword(ctx, u2.Email, "correct-password"); err == nil {
		t.Fatal("a gate error must not read as a successful password step")
	}
}

func TestIssueVerifiedSessionNeedsSessionsAndResets(t *testing.T) {
	svc, repo, u := factorRig(t, fakeGate{has: true}, nil)
	if _, err := svc.IssueVerifiedSession(context.Background(), u.ID, "ua", "ip"); !passed(err) {
		t.Fatalf("err = %v", err)
	}
	// Nothing was issued, so nothing was reset or recorded as a login.
	if repo.resetFailedCalled || repo.updateLastLoginCalled {
		t.Fatal("bookkeeping ran although no session was issued")
	}
}
