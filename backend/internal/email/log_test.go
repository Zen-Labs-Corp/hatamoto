package email

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

type fakeRecorder struct {
	mu      sync.Mutex
	entries []LogEntry
	err     error
}

func (f *fakeRecorder) Record(_ context.Context, e LogEntry) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	return int64(len(f.entries)), f.err
}

func newLoggedSink(t *testing.T, opts smtptest.Options, cfg func(*Config)) (*Service, *fakeRecorder, *smtptest.Server) {
	t.Helper()
	opts.Mode = smtptest.Plain
	srv := smtptest.Start(t, opts)
	c := sinkConfig(srv, SecurityNone)
	if cfg != nil {
		cfg(&c)
	}
	svc := newSink(t, c)
	rec := &fakeRecorder{}
	svc.SetRecorder(rec)
	return svc, rec, srv
}

func TestEveryKindLeavesOneRow(t *testing.T) {
	svc, rec, _ := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	fd := FormSubmissionData{FormName: "Contact", SubmissionID: "12345678-aaaa", Fields: []FormFieldData{{Label: "Name", Value: "Ann"}}}
	steps := []struct {
		kind Kind
		send func() error
	}{
		{KindFormNotification, func() error { return svc.SendFormSubmission(ctx, []string{"o@example.com"}, fd) }},
		{KindFormForward, func() error { return svc.SendFormSubmissionForward(ctx, []string{"o@example.com"}, fd, "Bob") }},
		{KindFormConfirmation, func() error {
			return svc.SendFormConfirmation(ctx, "s@example.com", FormConfirmationData{FormName: "Contact", Message: "Thanks", SiteName: "S"})
		}},
		{KindPasswordReset, func() error {
			return svc.SendPasswordReset(ctx, "a@example.com", ResetMailData{SiteName: "S", URL: "https://x/reset?token=SECRET", ExpiresIn: "1 hour"})
		}},
		{KindInvite, func() error {
			return svc.SendInvite(ctx, "a@example.com", InviteMailData{SiteName: "S", Role: "editor", URL: "https://x/invite?token=SECRET", ExpiresIn: "7 days"})
		}},
		{KindAdminAddressChanged, func() error {
			return svc.SendAdminAddressChanged(ctx, "a@example.com", AdminAddressChangedData{SiteName: "S", Address: "/secret-path", ChangedBy: "x", When: "now", IP: "1.2.3.4"})
		}},
		{KindAdminAddressSetup, func() error {
			return svc.SendAdminAddressSetup(ctx, "a@example.com", AdminAddressSetupData{SiteName: "S", Address: "/secret-path"})
		}},
		{KindTest, func() error { return svc.SendTest(ctx, "a@example.com", "S") }},
		{KindRunDigest, func() error {
			return svc.SendRunDigest(ctx, "a@example.com", RunDigestData{SiteName: "S", HistoryURL: "/x"})
		}},
		{KindPlugin, func() error { return svc.SendPlugin(ctx, "acme", []string{"a@example.com"}, "Hi", "<p>x</p>", "x") }},
		{KindOther, func() error { return svc.Send(ctx, []string{"a@example.com"}, "Hi", "<p>x</p>", "x") }},
	}
	for i, st := range steps {
		if err := st.send(); err != nil {
			t.Fatalf("%s: %v", st.kind, err)
		}
		if len(rec.entries) != i+1 {
			t.Fatalf("%s: %d rows, want %d", st.kind, len(rec.entries), i+1)
		}
		e := rec.entries[i]
		if e.Kind != st.kind || e.Status != StatusSent || e.Subject == "" || len(e.Recipients) != 1 || e.MessageID == "" {
			t.Errorf("%s: row = %+v", st.kind, e)
		}
		hasBody := e.BodyText != "" || e.BodyHTML != ""
		if hasBody != st.kind.StoresBody() {
			t.Errorf("%s: body stored = %v, want %v", st.kind, hasBody, st.kind.StoresBody())
		}
		if strings.Contains(e.BodyText+e.BodyHTML, "SECRET") || strings.Contains(e.BodyText+e.BodyHTML, "secret-path") {
			t.Errorf("%s: link stored in the log", st.kind)
		}
	}
	if got := rec.entries[9].Plugin; got != "acme" {
		t.Errorf("plugin = %q, want acme", got)
	}
	if rec.entries[0].BodyText == "" || rec.entries[0].BodyHTML == "" {
		t.Error("form_notification must keep both bodies")
	}
}

func TestRefusedSendIsLoggedFailedAndStillReturnsTheError(t *testing.T) {
	svc, rec, _ := newLoggedSink(t, smtptest.Options{RejectRcpt: true, User: "hunter-user", Pass: "hunter2"}, func(c *Config) {
		c.SMTPUsername, c.SMTPPassword = "hunter-user", "hunter2"
	})
	err := svc.Send(context.Background(), []string{"nobody@example.com"}, "Hi", "<p>x</p>", "x")
	if err == nil {
		t.Fatal("Send must return the refusal")
	}
	if len(rec.entries) != 1 {
		t.Fatalf("rows = %d, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Status != StatusFailed || !strings.Contains(e.Error, "550") || e.MessageID != "" {
		t.Errorf("row = %+v", e)
	}
	if strings.Contains(e.Error, "hunter") || strings.ContainsAny(e.Error, "\r\n") || len(e.Error) > maxLoggedError {
		t.Errorf("error text not clean: %q", e.Error)
	}
}

func TestSuppressedSendsAreLogged(t *testing.T) {
	ctx := context.Background()
	unconfigured := newTestService(t)
	rec := &fakeRecorder{}
	unconfigured.SetRecorder(rec)
	if err := unconfigured.Send(ctx, []string{"a@example.com"}, "Hi", "<p>x</p>", "x"); err != nil {
		t.Fatal(err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != StatusSuppressed {
		t.Fatalf("unconfigured rows = %+v", rec.entries)
	}

	svc, rec2, srv := newLoggedSink(t, smtptest.Options{}, nil)
	t.Setenv("EMAIL_DISABLED", "true")
	if err := svc.Send(ctx, []string{"a@example.com"}, "Hi", "<p>x</p>", "x"); err != nil {
		t.Fatal(err)
	}
	if len(rec2.entries) != 1 || rec2.entries[0].Status != StatusSuppressed || len(srv.Messages()) != 0 {
		t.Fatalf("disabled rows = %+v, delivered %d", rec2.entries, len(srv.Messages()))
	}
}

func TestRecorderFailureDoesNotFailTheSend(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	rec.err = errors.New("db down")
	if err := svc.Send(context.Background(), []string{"a@example.com"}, "Hi", "<p>x</p>", "x"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(srv.Messages()) != 1 {
		t.Errorf("delivered = %d, want 1", len(srv.Messages()))
	}
}

func TestResentFromAndBodyCap(t *testing.T) {
	svc, rec, _ := newLoggedSink(t, smtptest.Options{}, nil)
	big := strings.Repeat("a", maxLoggedBody+10)
	if _, err := svc.SendTrackedWith(context.Background(), SendOptions{Kind: KindFormNotification, ResentFrom: 7}, []string{"a@example.com"}, "Hi", "<p>x</p>", big); err != nil {
		t.Fatal(err)
	}
	e := rec.entries[0]
	if e.ResentFrom != 7 || len(e.BodyText) != maxLoggedBody {
		t.Errorf("resent_from %d, body len %d", e.ResentFrom, len(e.BodyText))
	}
}

func TestCleanError(t *testing.T) {
	c := Config{SMTPUsername: "AKIAUSER", SMTPPassword: "s3cret"}
	got := cleanError(errors.New("auth AKIAUSER  s3cret\r\nsecond   line"), c)
	if got != "auth [redacted] [redacted]" {
		t.Errorf("got %q", got)
	}
	long := cleanError(errors.New(strings.Repeat("é", 500)), Config{})
	if r := []rune(long); len(r) != maxLoggedError {
		t.Errorf("cap = %d runes", len(r))
	}
	if cleanError(nil, c) != "" {
		t.Error("nil error must give empty text")
	}
}
