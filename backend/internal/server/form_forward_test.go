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

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/testutil"
)

type fwdFormRepo struct {
	form.Repository
	f *form.Form
	s *form.FormSubmission
}

func (r *fwdFormRepo) GetFormByID(context.Context, uuid.UUID) (*form.Form, error) { return r.f, nil }
func (r *fwdFormRepo) GetSubmissionByID(context.Context, uuid.UUID) (*form.FormSubmission, error) {
	return r.s, nil
}

func forwardServer(t *testing.T, smtpHost string, logger *slog.Logger) *Server {
	t.Helper()
	svc, err := email.NewService(email.Config{SMTPHost: smtpHost, SMTPPort: 1, FromAddress: "a@example.com"}, logger)
	testutil.NoError(t, err)
	f := &form.Form{ID: uuid.New(), Name: "Contact", Slug: "contact"}
	return &Server{
		cfg:          &config.Config{},
		logger:       logger,
		emailService: svc,
		formService:  form.NewService(&fwdFormRepo{f: f, s: &form.FormSubmission{ID: uuid.New(), FormID: f.ID}}, logger),
	}
}

// A forward that cannot be sent must surface as an error status, never an
// empty 200 the admin UI would read as success.
func TestForwardSubmission_SendFailureIsNon2xx(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")                                                     // CI sets the kill switch, which short-circuits sends
	s := forwardServer(t, "127.0.0.1", slog.New(slog.NewTextHandler(io.Discard, nil))) // port 1: refused
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("POST /forms/{id}/submissions/{submissionId}/email", s.handleEmailFormSubmission)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/forms/"+uuid.NewString()+"/submissions/"+uuid.NewString()+"/email", strings.NewReader(`{"to":"x@example.com"}`)))
	if rec.Code < 400 {
		t.Fatalf("status = %d, want non-2xx; body %q", rec.Code, rec.Body)
	}
}

func TestForwardSubmission_UnconfiguredSMTPIs503(t *testing.T) {
	s := forwardServer(t, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("POST /forms/{id}/submissions/{submissionId}/email", s.handleEmailFormSubmission)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/forms/"+uuid.NewString()+"/submissions/"+uuid.NewString()+"/email", strings.NewReader(`{"to":"x@example.com"}`)))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "email is not configured") {
		t.Fatalf("status = %d body %q, want 503 not configured", rec.Code, rec.Body)
	}
}

func TestSendFormSubmissionEmail_WarnsWhenUnconfigured(t *testing.T) {
	var buf bytes.Buffer
	s := forwardServer(t, "localhost", slog.New(slog.NewTextHandler(&buf, nil)))
	to := "ops@example.com"
	f := &form.Form{ID: uuid.New(), Settings: form.FormSettings{NotifyEmail: &to}}
	s.sendFormSubmissionEmail(f, &form.FormSubmission{ID: uuid.New()}, nil)
	if !strings.Contains(buf.String(), "SMTP is not configured") {
		t.Fatalf("no warning logged: %q", buf.String())
	}
}
