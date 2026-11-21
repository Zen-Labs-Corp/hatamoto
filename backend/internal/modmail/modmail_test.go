package modmail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/modhost"
	"hatamoto.dev/core/internal/runenv"
)

type recorder struct {
	mu      sync.Mutex
	entries []email.LogEntry
}

func (r *recorder) Record(_ context.Context, e email.LogEntry) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return int64(len(r.entries)), nil
}

func newSink(t *testing.T) (*email.Service, *recorder, *smtptest.Server) {
	t.Helper()
	t.Setenv("EMAIL_DISABLED", "")
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{
		SMTPHost: srv.Host(), SMTPPort: srv.Port(), FromAddress: "from@example.com",
		Security: email.SecurityNone, RootCAs: srv.RootCAs(),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	svc.SetRecorder(rec)
	return svc, rec, srv
}

func adapter(s Sender, owners ...string) *Adapter {
	return &Adapter{
		Sender: func() Sender { return s },
		Owners: func(context.Context) ([]string, error) { return owners, nil },
		UserEmail: func(context.Context, uuid.UUID) (string, error) {
			return "u@example.test", nil
		},
	}
}

var msg = modhost.Message{Subject: "Hello", Text: "body text"}

func TestToOwnersSendsAndLogsOnePerOwner(t *testing.T) {
	svc, rec, srv := newSink(t)
	a := adapter(svc, "a@example.test", "b@example.test")
	if err := a.ToOwners(context.Background(), "example", msg); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Messages()); n != 2 {
		t.Fatalf("messages = %d, want 2", n)
	}
	if len(rec.entries) != 2 {
		t.Fatalf("log = %+v", rec.entries)
	}
	for _, e := range rec.entries {
		if e.Kind != email.KindModule || e.Plugin != "example" || e.Status != email.StatusSent || e.BodyText != "" {
			t.Errorf("entry = %+v", e)
		}
	}
}

func TestToUserSendsOnce(t *testing.T) {
	svc, rec, srv := newSink(t)
	if err := adapter(svc).ToUser(context.Background(), "example", uuid.NewString(), msg); err != nil {
		t.Fatal(err)
	}
	if len(srv.Messages()) != 1 || len(rec.entries) != 1 {
		t.Fatalf("messages = %d, log = %d", len(srv.Messages()), len(rec.entries))
	}
}

func TestNoOwners(t *testing.T) {
	svc, _, srv := newSink(t)
	if err := adapter(svc).ToOwners(context.Background(), "example", msg); !errors.Is(err, modhost.ErrNoRecipients) {
		t.Fatalf("err = %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("mail sent to nobody")
	}
}

func TestNoSender(t *testing.T) {
	a := adapter(nil, "a@example.test")
	a.Sender = func() Sender { return nil }
	ctx := context.Background()
	if err := a.ToOwners(ctx, "example", msg); !errors.Is(err, modhost.ErrNoMailer) {
		t.Errorf("owners err = %v", err)
	}
	if err := a.ToUser(ctx, "example", uuid.NewString(), msg); !errors.Is(err, modhost.ErrNoMailer) {
		t.Errorf("user err = %v", err)
	}
}

func TestBadUserID(t *testing.T) {
	svc, _, srv := newSink(t)
	if err := adapter(svc).ToUser(context.Background(), "example", "not-a-uuid", msg); !errors.Is(err, modhost.ErrUnknownUser) {
		t.Fatalf("err = %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("mail sent to an unknown user")
	}
}

func TestUnknownUserFromLookup(t *testing.T) {
	svc, _, _ := newSink(t)
	a := adapter(svc)
	a.UserEmail = func(context.Context, uuid.UUID) (string, error) { return "", modhost.ErrUnknownUser }
	if err := a.ToUser(context.Background(), "example", uuid.NewString(), msg); !errors.Is(err, modhost.ErrUnknownUser) {
		t.Fatalf("err = %v", err)
	}
}

func TestUserWithoutEmail(t *testing.T) {
	svc, _, srv := newSink(t)
	a := adapter(svc)
	a.UserEmail = func(context.Context, uuid.UUID) (string, error) { return "", nil }
	if err := a.ToUser(context.Background(), "example", uuid.NewString(), msg); !errors.Is(err, errNoAddress) {
		t.Fatalf("err = %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("mail sent without an address")
	}
}

type flaky struct {
	failFor string
	sent    []string
}

func (f *flaky) SendTrackedWith(_ context.Context, _ email.SendOptions, to []string, _, _, _ string) (string, error) {
	if to[0] == f.failFor {
		return "", errors.New("smtp down")
	}
	f.sent = append(f.sent, to[0])
	return "id", nil
}

func TestOneFailingOwnerDoesNotStopTheRest(t *testing.T) {
	f := &flaky{failFor: "a@example.test"}
	err := adapter(f, "a@example.test", "b@example.test").ToOwners(context.Background(), "example", msg)
	if err == nil {
		t.Fatal("want the failure returned")
	}
	if len(f.sent) != 1 || f.sent[0] != "b@example.test" {
		t.Fatalf("sent = %v", f.sent)
	}
}

func TestStagingSuppressesModuleMail(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	svc, rec, srv := newSink(t)
	if err := adapter(svc).ToUser(context.Background(), "example", uuid.NewString(), msg); err != nil {
		t.Fatalf("staging must not fail the call: %v", err)
	}
	if len(srv.Messages()) != 0 || len(srv.Commands()) != 0 {
		t.Fatalf("receiver saw traffic: %v", srv.Commands())
	}
	if len(rec.entries) != 1 || rec.entries[0].Kind != email.KindModule || rec.entries[0].Plugin != "example" || rec.entries[0].Status != email.StatusSuppressed {
		t.Fatalf("log = %+v", rec.entries)
	}
}
