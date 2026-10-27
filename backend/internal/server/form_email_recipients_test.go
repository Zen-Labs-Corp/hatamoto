package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/user"
)

// notifyUsers is a user.Repository that knows a few accounts by exact email.
type notifyUsers struct {
	user.Repository
	byEmail map[string]user.Role
}

func (r notifyUsers) GetByEmail(_ context.Context, addr string) (*user.User, error) {
	if addr == "boom@x.test" {
		return nil, errors.New("db down")
	}
	if role, ok := r.byEmail[addr]; ok {
		return &user.User{Email: addr, Role: role}, nil
	}
	return nil, user.ErrUserNotFound
}

// notifyOutcome is what each recipient was sent.
type notifyOutcome map[string]string

// runNotify sends one submission to the comma-separated notify list and returns
// the raw message each recipient got, plus the log.
func runNotify(t *testing.T, repo user.Repository, notify string, want int) (notifyOutcome, string) {
	t.Helper()
	return runNotifyPage(t, repo, notify, "", want)
}

// runNotifyPage is runNotify for a submission sent from pageURL.
func runNotifyPage(t *testing.T, repo user.Repository, notify, pageURL string, want int) (notifyOutcome, string) {
	t.Helper()
	t.Setenv("EMAIL_DISABLED", "") // CI sets the kill switch, which short-circuits sends
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	svc, err := email.NewService(email.Config{
		SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{}, logger: logger, emailService: svc, userRepo: repo}
	s.adminBundle.Store(&adminBundle{prefix: "/hq-x7k"})

	f := &form.Form{ID: uuid.New(), Name: "Contact", Slug: "contact", Settings: form.FormSettings{NotifyEmail: &notify}}
	sub := &form.FormSubmission{ID: uuid.New(), FormID: f.ID}
	sub.Metadata.PageURL = pageURL
	s.sendFormSubmissionEmail(f, sub, nil)

	deadline := time.Now().Add(10 * time.Second)
	for len(sink.Messages()) < want && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a surplus message would land here
	got := notifyOutcome{}
	msgs := sink.Messages()
	if len(msgs) != want {
		t.Fatalf("messages = %d, want %d", len(msgs), want)
	}
	for _, m := range msgs {
		for _, rcpt := range m.To {
			got[strings.Trim(rcpt, "<> ")] = m.Data
		}
	}
	return got, logs.String()
}

func hasLink(data string) bool {
	return strings.Contains(data, "/hq-x7k/settings/submissions/")
}

func TestFormEmailAdminLinkOnlyForAccounts(t *testing.T) {
	repo := notifyUsers{byEmail: map[string]user.Role{"admin@x.test": user.RoleOwner, "ed@x.test": user.RoleEditor}}
	cases := []struct {
		name, notify string
		repo         user.Repository
		msgs         int
		link         map[string]bool
		logWant      string
	}{
		{"mixed", "admin@x.test, ext@y.test", repo, 2, map[string]bool{"admin@x.test": true, "ext@y.test": false}, ""},
		{"all external", "a@y.test, b@y.test", repo, 1, map[string]bool{"a@y.test": false, "b@y.test": false}, ""},
		{"all admin", "admin@x.test, ed@x.test", repo, 1, map[string]bool{"admin@x.test": true, "ed@x.test": true}, ""},
		{"lookup error", "boom@x.test", repo, 1, map[string]bool{"boom@x.test": false}, "lookup failed"},
		{"nil repo", "admin@x.test", nil, 1, map[string]bool{"admin@x.test": false}, ""},
		{"case", "ADMIN@X.TEST, ext@y.test", repo, 2, map[string]bool{"ADMIN@X.TEST": true, "ext@y.test": false}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, logs := runNotify(t, c.repo, c.notify, c.msgs)
			for rcpt, wantLink := range c.link {
				data, ok := got[rcpt]
				if !ok {
					t.Fatalf("%s got no message; recipients %v", rcpt, got)
				}
				if hasLink(data) != wantLink {
					t.Errorf("%s: admin link present = %v, want %v", rcpt, !wantLink, wantLink)
				}
			}
			if c.logWant != "" && !strings.Contains(logs, c.logWant) {
				t.Errorf("log lacks %q:\n%s", c.logWant, logs)
			}
		})
	}
}

// A submission sent from inside the admin carries the admin address in its
// page URL; no recipient may get it.
func TestFormEmailRedactsAdminSubmitterPage(t *testing.T) {
	repo := notifyUsers{byEmail: map[string]user.Role{"admin@x.test": user.RoleOwner}}
	got, _ := runNotifyPage(t, repo, "admin@x.test, ext@y.test", "https://site.test/hq-x7k/entities/1/edit/preview/tok", 2)
	for rcpt, data := range got {
		if strings.Contains(data, "hq-x7k/entities") {
			t.Errorf("%s: submitter page leaks the admin address:\n%s", rcpt, data)
		}
		if !strings.Contains(data, "site.test") {
			t.Errorf("%s: submitter page host missing", rcpt)
		}
	}
}

// The admin's "Email this" forward redacts an admin submitter page too.
func TestFormForwardRedactsAdminSubmitterPage(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "") // CI sets the kill switch, which short-circuits sends
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{
		SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), emailService: svc}
	s.adminBundle.Store(&adminBundle{prefix: "/hq-x7k"})
	f := &form.Form{ID: uuid.New(), Name: "Contact", Slug: "contact"}
	sub := &form.FormSubmission{ID: uuid.New(), FormID: f.ID}
	sub.Metadata.PageURL = "https://site.test/hq-x7k/entities/1/edit/preview/tok"
	if err := (formMailer{s: s}).Forward(context.Background(), f, sub, []string{"ext@y.test"}, "Ana"); err != nil {
		t.Fatal(err)
	}
	msgs := sink.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if strings.Contains(msgs[0].Data, "hq-x7k/entities") || !strings.Contains(msgs[0].Data, "site.test") {
		t.Errorf("forward leaks the admin address or lost the host:\n%s", msgs[0].Data)
	}
}
