package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/form"
)

// countingFormRepo adds the recent-send count the rate limit reads: every
// stored submission from the address counts, spam included.
type countingFormRepo struct{ *fakeFormRepo }

func (r countingFormRepo) CountRecentSubmissionsByIP(_ context.Context, _ uuid.UUID, ip string, _ time.Time) (int, error) {
	n := 0
	for _, sub := range r.subs {
		if sub.Metadata.IPAddress == ip {
			n++
		}
	}
	return n, nil
}

// settingsServer serves one active contact form with the given settings.
func settingsServer(t *testing.T, set form.FormSettings) (*Server, *fakeFormRepo) {
	t.Helper()
	repo := &fakeFormRepo{forms: map[string]*form.Form{"contact": {
		ID: uuid.New(), Slug: "contact", Name: "Contact", Status: form.StatusActive,
		Fields: []form.FormField{
			{Name: "name", Label: "Name", Type: form.FieldTypeText},
			{Name: "message", Label: "Message", Type: form.FieldTypeTextarea},
		},
		Settings: set,
	}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{cfg: &config.Config{}, logger: logger, formService: form.NewService(countingFormRepo{repo}, logger)}, repo
}

// sendJSON posts a JSON submission from ip and returns the status code.
func sendJSON(srv *Server, ip, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/forms/contact/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":4000"
	req.SetPathValue("slug", "contact")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	return rec.Code
}

func TestFormSettings_WordListStoresSpam(t *testing.T) {
	t.Parallel()
	srv, repo := settingsServer(t, form.FormSettings{BlockedWords: []string{"casino"}})
	if code := sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"Best CASINO deals"}`); code != http.StatusCreated {
		t.Fatalf("status = %d, a spam match must not be refused", code)
	}
	if code := sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"hello"}`); code != http.StatusCreated {
		t.Fatalf("clean status = %d", code)
	}
	if len(repo.subs) != 2 || repo.subs[0].Status != form.SubmissionStatusSpam || repo.subs[1].Status != form.SubmissionStatusNew {
		t.Fatalf("stored = %+v", repo.subs)
	}
}

func TestFormSettings_WordSpamSendsNoNotification(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	notify := "team@example.test"
	srv, _ := settingsServer(t, form.FormSettings{BlockedWords: []string{"casino"}, NotifyEmail: &notify})
	srv.emailService = svc

	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"casino"}`)
	time.Sleep(300 * time.Millisecond) // a surplus message would land here
	if n := len(sink.Messages()); n != 0 {
		t.Fatalf("spam sent %d notifications", n)
	}
}

func TestFormSettings_RateLimit(t *testing.T) {
	t.Parallel()
	for _, noIP := range []bool{false, true} {
		name := "ip stored"
		if noIP {
			name = "no ip"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, repo := settingsServer(t, form.FormSettings{RateLimitPerHour: 2, NoIP: noIP})
			for i := 0; i < 3; i++ {
				if code := sendJSON(srv, "203.0.113.7", `{"name":"Ada"}`); code != http.StatusCreated {
					t.Fatalf("send %d: status %d", i+1, code)
				}
			}
			sendJSON(srv, "203.0.113.8", `{"name":"Bob"}`) // another visitor is unaffected
			var got []string
			for _, sub := range repo.subs {
				got = append(got, sub.Status)
			}
			want := []string{"new", "new", "spam", "new"}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("statuses = %v, want %v", got, want)
			}
		})
	}
}

func TestFormSettings_NoIPStoresNoAddress(t *testing.T) {
	t.Parallel()
	srv, repo := settingsServer(t, form.FormSettings{NoIP: true})
	sendJSON(srv, "203.0.113.7", `{"name":"Ada"}`)
	if len(repo.subs) != 1 || repo.subs[0].Metadata.IPAddress != "" {
		t.Fatalf("stored = %+v", repo.subs)
	}
	srv, repo = settingsServer(t, form.FormSettings{})
	sendJSON(srv, "203.0.113.7", `{"name":"Ada"}`)
	if repo.subs[0].Metadata.IPAddress != "203.0.113.7" {
		t.Errorf("default form lost the address: %+v", repo.subs[0].Metadata)
	}
}

func TestFormSettings_Honeypot(t *testing.T) {
	t.Parallel()
	body := `{"name":"Ada","hm_website":"http://spam.test"}`
	srv, repo := settingsServer(t, form.FormSettings{})
	if code := sendJSON(srv, "203.0.113.1", body); code != http.StatusCreated || len(repo.subs) != 0 {
		t.Errorf("default: status %d, stored %d, want the trap to hold", code, len(repo.subs))
	}
	srv, repo = settingsServer(t, form.FormSettings{HoneypotOff: true})
	if code := sendJSON(srv, "203.0.113.1", body); code != http.StatusCreated || len(repo.subs) != 1 {
		t.Errorf("honeypot off: status %d, stored %d, want the send stored", code, len(repo.subs))
	}
	if _, ok := repo.subs[0].Data["hm_website"]; ok {
		t.Error("trap field leaked into the stored data")
	}
}

func TestFormSettings_StoreOffKeepsNoAnswersButStillMails(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	notify := "team@example.test"
	srv, repo := settingsServer(t, form.FormSettings{NotifyEmail: &notify, NotifySubject: "Enquiry from {{name}}"})
	srv.emailService = svc
	repo.forms["contact"].Settings.StoreOff = true

	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"secret words"}`)
	if len(repo.subs) != 1 || len(repo.subs[0].Data) != 0 {
		t.Fatalf("stored data = %+v, want a row with no answers", repo.subs)
	}
	if m := repo.subs[0].Metadata; m != (form.SubmissionMetadata{}) || repo.subs[0].Attribution != nil {
		t.Fatalf("stored metadata = %+v attribution = %+v, want nothing about the visitor", m, repo.subs[0].Attribution)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(sink.Messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	msgs := sink.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Data, "Subject: Enquiry from Ada") || !strings.Contains(msgs[0].Data, "secret words") {
		t.Fatalf("notification = %+v", msgs)
	}
}

// A store-off row holds no address, so the limit must not rely on stored IPs.
func TestFormSettings_StoreOffStillRateLimits(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	notify := "team@example.test"
	srv, repo := settingsServer(t, form.FormSettings{StoreOff: true, RateLimitPerHour: 1, NotifyEmail: &notify})
	srv.emailService = svc
	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"one"}`)
	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"two"}`)
	if len(repo.subs) != 2 || repo.subs[1].Status != form.SubmissionStatusSpam {
		t.Fatalf("stored = %+v, want the second send stored as spam", repo.subs)
	}
}

// An API-created form has no store keys at all (StoreSubmissions false): it keeps its answers.
func TestFormSettings_AbsentStoreKeyKeepsAnswers(t *testing.T) {
	srv, repo := settingsServer(t, form.FormSettings{})
	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"hello"}`)
	if len(repo.subs) != 1 || repo.subs[0].Data["message"] != "hello" {
		t.Fatalf("stored = %+v, want the answers kept", repo.subs)
	}
}

// Store off with no mail path would lose the answers for good, so they are kept.
func TestFormSettings_StoreOffWithoutMailKeepsAnswers(t *testing.T) {
	notify := "team@example.test"
	srv, repo := settingsServer(t, form.FormSettings{StoreOff: true, NotifyEmail: &notify})
	sendJSON(srv, "203.0.113.1", `{"name":"Ada","message":"hello"}`)
	if len(repo.subs) != 1 || repo.subs[0].Data["message"] != "hello" {
		t.Fatalf("stored = %+v, want the answers kept without SMTP", repo.subs)
	}
}

func TestFormSettings_ConfirmationSubject(t *testing.T) {
	t.Parallel()
	f := &form.Form{Name: "Contact", Fields: []form.FormField{{Name: "email", Type: form.FieldTypeEmail}},
		Settings: form.FormSettings{ConfirmationEmail: true, ConfirmationSubject: " Got it "}}
	_, msg, ok := formConfirmation(f, map[string]any{"email": "v@x.test"}, "Site")
	if !ok || msg.Subject != "Got it" {
		t.Fatalf("ok=%v subject=%q", ok, msg.Subject)
	}
}
