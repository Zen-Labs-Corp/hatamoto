package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/user"
)

// restLoginLog collects the "admin audit" log lines adminAudit writes when the
// server has no database.
type restLoginLog struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (l *restLoginLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *restLoginLog) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *restLoginLog) WithGroup(string) slog.Handler            { return l }
func (l *restLoginLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "admin audit" {
		return nil
	}
	m := map[string]any{}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
	l.mu.Lock()
	l.recs = append(l.recs, m)
	l.mu.Unlock()
	return nil
}

// restLoginRepo knows one user; any other address is unknown.
type restLoginRepo struct {
	user.Repository
	u *user.User
}

func (r restLoginRepo) GetByEmail(_ context.Context, email string) (*user.User, error) {
	if !strings.EqualFold(email, r.u.Email) {
		return nil, user.ErrUserNotFound
	}
	c := *r.u
	return &c, nil
}
func (r restLoginRepo) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if id != r.u.ID {
		return nil, user.ErrUserNotFound
	}
	c := *r.u
	return &c, nil
}
func (r restLoginRepo) RecordFailedLogin(context.Context, uuid.UUID, int, time.Duration) error {
	return nil
}
func (r restLoginRepo) ResetFailedLogins(context.Context, uuid.UUID) error { return nil }

var restLoginSecret = strings.Repeat("ab", 16)

func restLoginServer(t *testing.T, locked bool) (*Server, *restLoginLog, *user.User) {
	t.Helper()
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	u := &user.User{ID: uuid.New(), Email: "ana@example.com", PasswordHash: hash, Role: user.RoleOwner}
	if locked {
		until := time.Now().Add(time.Hour)
		u.LockedUntil = &until
	}
	lg := &restLoginLog{}
	s := &Server{
		authService: auth.NewService(restLoginRepo{u: u}, nil, nil),
		userRepo:    restLoginRepo{u: u},
		logger:      slog.New(lg),
		cfg:         &config.Config{Secret: restLoginSecret},
	}
	return s, lg, u
}

func restLoginPost(s *Server, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.handleLogin(rr, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body)))
	return rr
}

func TestRestLoginAuditFailedKnownAndUnknownEmailLookAlike(t *testing.T) {
	t.Parallel()

	var rows []map[string]any
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		s, lg, _ := restLoginServer(t, false)
		body := `{"email":"` + email + `","password":"typed-wrong-pw"}`
		if rr := restLoginPost(s, body); rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", email, rr.Code)
		}
		if len(lg.recs) != 1 {
			t.Fatalf("%s: rows = %v", email, lg.recs)
		}
		rec := lg.recs[0]
		dump := rec["actor"].(string) + rec["detail"].(string)
		if rec["action"] != admin.AuditSignInFailed || !strings.HasPrefix(rec["actor"].(string), auditlog.EmailHashPrefix) ||
			rec["result"] != "denied" || strings.Contains(dump, email) || strings.Contains(dump, "typed-wrong-pw") {
			t.Fatalf("%s: row = %v", email, rec)
		}
		if !strings.Contains(rec["detail"].(string), `"reason":"credentials"`) || !strings.Contains(rec["detail"].(string), `"via":"api"`) {
			t.Fatalf("%s: detail = %v", email, rec["detail"])
		}
		rows = append(rows, rec)
	}
	if rows[0]["detail"] != rows[1]["detail"] || rows[0]["actor"] == rows[1]["actor"] {
		t.Fatalf("shape differs: %v vs %v", rows[0], rows[1])
	}
}

func TestRestLoginAuditLockedAccountAnd2FA(t *testing.T) {
	t.Parallel()

	s, lg, _ := restLoginServer(t, true)
	if rr := restLoginPost(s, `{"email":"ana@example.com","password":"correct-password","code":"123456"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("locked: %d", rr.Code)
	}
	if len(lg.recs) != 1 {
		t.Fatalf("rows = %v", lg.recs)
	}
	d := lg.recs[0]["detail"].(string)
	if !strings.Contains(d, `"reason":"locked"`) || !strings.Contains(d, `"method":"totp"`) || strings.Contains(d, "123456") {
		t.Fatalf("detail = %s", d)
	}
}

func TestRestLoginAuditSuccessAndLogoutRows(t *testing.T) {
	t.Parallel()

	s, lg, u := restLoginServer(t, false)
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	s.restLoginAuditOK(r, u, false)
	s.restLoginAuditOK(r, nil, false) // a response without a user writes nothing
	s.restLogoutAudit(r, u.ID)
	if len(lg.recs) != 2 {
		t.Fatalf("rows = %v", lg.recs)
	}
	in, out := lg.recs[0], lg.recs[1]
	if in["action"] != admin.AuditSignIn || in["actor"] != u.Email || !strings.Contains(in["detail"].(string), `"method":"password"`) {
		t.Fatalf("sign-in = %v", in)
	}
	if out["action"] != admin.AuditSignOut || out["actor"] != u.Email || !strings.Contains(out["detail"].(string), `"via":"api"`) {
		t.Fatalf("sign-out = %v", out)
	}
}

func TestRestLoginAuditFailedWithoutEmailWritesNothing(t *testing.T) {
	t.Parallel()

	s, lg, _ := restLoginServer(t, false)
	if rr := restLoginPost(s, `{"email":"","password":"typed-wrong-pw"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
	if len(lg.recs) != 0 {
		t.Fatalf("rows = %v", lg.recs)
	}
}
