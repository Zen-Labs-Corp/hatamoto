package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/authstate"
	"hatamoto.dev/core/internal/user"
)

// refusePassword is a sign-in policy that refuses the password method.
type refusePassword struct{}

func (refusePassword) AllowSignIn(_ context.Context, a authstate.SignInAttempt) (bool, error) {
	return a.Method != authstate.MethodPassword, nil
}

// Under a policy refusing password sign-in, the JSON login answers the same
// generic 401 as a wrong password, even with the right password.
func TestHandleLoginPolicyRefusesPassword(t *testing.T) {
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &user.User{ID: uuid.New(), Email: "ana@example.com", PasswordHash: hash, Role: user.RoleOwner}
	svc := auth.NewService(loginRepo{u: u}, nil, nil) // no session store: only a refusal gives 401
	s := &Server{authService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	post := func(pw string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.handleLogin(rr, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"email":"ana@example.com","password":"`+pw+`"}`)))
		return rr
	}

	if rr := post("correct-password"); rr.Code == http.StatusUnauthorized {
		t.Fatalf("without a policy the right password was refused: %d", rr.Code)
	}

	authstate.Reset()
	t.Cleanup(authstate.Reset)
	authstate.RegisterSignInPolicy(refusePassword{})
	bad, refused := post("wrong-password"), post("correct-password")
	if refused.Code != http.StatusUnauthorized || refused.Body.String() != bad.Body.String() {
		t.Fatalf("refused login: %d %q, want the wrong-password answer %d %q", refused.Code, refused.Body, bad.Code, bad.Body)
	}
}
