package email

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/runenv"
)

func TestStagingSuppressesMail(t *testing.T) {
	t.Setenv(runenv.EnvVar, "staging")
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	if err := svc.Send(ctx, []string{"a@example.com"}, "Hi", "<p>x</p>", "x"); err != nil {
		t.Fatalf("Send in %s must not fail: %v", runenv.Mail, err)
	}
	if len(rec.entries) != 1 || rec.entries[0].Status != StatusSuppressed {
		t.Fatalf("rows = %+v, want one suppressed", rec.entries)
	}
	err := Check(ctx, sinkConfig(srv, SecurityNone))
	if !errors.Is(err, runenv.ErrSuppressed) {
		t.Fatalf("Check err = %v, want ErrSuppressed", err)
	}
	if len(srv.Messages()) != 0 || len(srv.Commands()) != 0 {
		t.Fatalf("receiver saw traffic: %v", srv.Commands())
	}
}
