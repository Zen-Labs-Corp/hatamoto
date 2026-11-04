package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

type loginRepo struct {
	user.Repository
	u *user.User
}

func (r loginRepo) GetByEmail(context.Context, string) (*user.User, error) { c := *r.u; return &c, nil }
func (r loginRepo) RecordFailedLogin(context.Context, uuid.UUID, int, time.Duration) error {
	return nil
}
func (r loginRepo) ResetFailedLogins(context.Context, uuid.UUID) error { return nil }

type allowFactor struct{ has bool }

func (g allowFactor) HasAnyFactor(context.Context, uuid.UUID) (bool, error) { return g.has, nil }

type codeIs string

func (c codeIs) Verify(_ context.Context, _ uuid.UUID, code string) error {
	if code != "" && code == string(c) {
		return nil
	}
	return twofactor.ErrWrongCode
}

// The JSON login answers the generic 401 for a user with a second factor
// until a valid code is sent, and never says a code is needed.
func TestHandleLoginRequiresCodeForFactorUser(t *testing.T) {
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &user.User{ID: uuid.New(), Email: "ana@example.com", PasswordHash: hash, Role: user.RoleOwner}
	svc := auth.NewService(loginRepo{u: u}, nil, nil)
	svc.SetSecondFactor(allowFactor{has: true}, codeIs("123456"))
	s := &Server{authService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.handleLogin(rr, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body)))
		return rr
	}
	bad := post(`{"email":"ana@example.com","password":"wrong"}`)
	for name, body := range map[string]string{
		"no code":    `{"email":"ana@example.com","password":"correct-password"}`,
		"wrong code": `{"email":"ana@example.com","password":"correct-password","code":"000000"}`,
	} {
		rr := post(body)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rr.Code)
		}
		if rr.Body.String() != bad.Body.String() {
			t.Errorf("%s: body %q differs from the bad-password body %q", name, rr.Body.String(), bad.Body.String())
		}
	}
	// A valid code passes the factor check; this unit rig has no session store,
	// so it ends in the "login failed" 500 instead of a 401.
	if rr := post(`{"email":"ana@example.com","password":"correct-password","code":"123456"}`); rr.Code == http.StatusUnauthorized {
		t.Errorf("a valid code was refused: %d %s", rr.Code, rr.Body)
	}
}

// Under a policy that requires a factor, the JSON login refuses a password-only
// session for a user without one and says why.
func TestHandleLoginRefusesPasswordOnlyUnderRequiredPolicy(t *testing.T) {
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &user.User{ID: uuid.New(), Email: "ana@example.com", PasswordHash: hash, Role: user.RoleEditor}
	svc := auth.NewService(loginRepo{u: u}, nil, nil)
	svc.SetSecondFactor(allowFactor{has: false}, codeIs("123456"))
	svc.SetFactorRequirement(func(context.Context, *user.User) (bool, error) { return true, nil })
	s := &Server{authService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rr := httptest.NewRecorder()
	s.handleLogin(rr, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"email":"ana@example.com","password":"correct-password"}`)))
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "two-step") {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
}
