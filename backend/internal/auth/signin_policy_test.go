package auth

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/authstate"
)

// refuseMethods refuses the listed methods and records every attempt.
type refuseMethods struct {
	refuse map[authstate.Method]bool
	err    error
	seen   []authstate.SignInAttempt
}

func (p *refuseMethods) AllowSignIn(_ context.Context, a authstate.SignInAttempt) (bool, error) {
	p.seen = append(p.seen, a)
	if p.err != nil {
		return false, p.err
	}
	return !p.refuse[a.Method], nil
}

func registerPolicy(t *testing.T, p authstate.SignInPolicy) {
	t.Helper()
	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterSignInPolicy(p)
}

func TestSignInPolicyRefusesPasswordEntryPoints(t *testing.T) {
	ctx := context.Background()
	pol := &refuseMethods{refuse: map[authstate.Method]bool{authstate.MethodPassword: true}}
	registerPolicy(t, pol)
	svc, repo, u := factorRig(t, fakeGate{}, nil)

	if _, err := svc.CheckPassword(ctx, u.Email, "correct-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CheckPassword: %v", err)
	}
	if _, _, err := svc.LoginSession(ctx, u.Email, "correct-password", "ua", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("LoginSession: %v", err)
	}
	if _, _, err := svc.LoginAPISession(ctx, u.Email, "correct-password", "", "ua", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("LoginAPISession: %v", err)
	}
	if repo.recordFailedCalled {
		t.Fatal("a policy refusal counted as a failed login")
	}
	if repo.resetFailedCalled {
		t.Fatal("a correct password refused by the policy reset the failure counter")
	}
	// Sudo re-confirmation is not a sign-in.
	if err := svc.ConfirmPassword(ctx, u.ID, u.Email, "correct-password"); err != nil {
		t.Fatalf("ConfirmPassword: %v", err)
	}
	if len(pol.seen) == 0 || pol.seen[0].User.Email != u.Email || pol.seen[0].Method != authstate.MethodPassword {
		t.Fatalf("attempt = %+v", pol.seen)
	}
}

func TestSignInPolicyAdmitByMethod(t *testing.T) {
	ctx := context.Background()
	registerPolicy(t, &refuseMethods{refuse: map[authstate.Method]bool{authstate.MethodPasskey: true}})
	svc, repo, u := factorRig(t, fakeGate{}, nil)
	repo.getByIDReturn = u

	if err := svc.admit(ctx, u.ID, authstate.Via{Method: authstate.MethodPasskey}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("passkey: %v", err)
	}
	if err := svc.admit(ctx, u.ID, viaPassword); err != nil {
		t.Fatalf("password: %v", err)
	}
	if err := svc.admit(ctx, u.ID, authstate.Via{Method: authstate.MethodProvider, Provider: "x"}); err != nil {
		t.Fatalf("provider: %v", err)
	}
}

func TestSignInPolicyErrorFailsClosed(t *testing.T) {
	registerPolicy(t, &refuseMethods{err: errors.New("policy store down")})
	svc, repo, u := factorRig(t, fakeGate{}, nil)
	repo.getByIDReturn = u
	if err := svc.admit(context.Background(), u.ID, viaPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestSignInPolicyNoneRegisteredAllows(t *testing.T) {
	authstate.Reset()
	svc, repo, u := factorRig(t, fakeGate{}, nil)
	repo.getByIDReturn = u
	if err := svc.admit(context.Background(), u.ID, authstate.Via{Method: authstate.MethodProvider, Provider: "x"}); err != nil {
		t.Fatal(err)
	}
}
