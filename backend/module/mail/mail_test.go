package mail_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/module/mail"
)

type fake struct {
	calls  int
	module string
	user   string
	msg    mail.Message
}

func (f *fake) ToOwners(_ context.Context, module string, m mail.Message) error {
	f.calls++
	f.module, f.msg = module, m
	return nil
}

func (f *fake) ToUser(_ context.Context, module, userID string, m mail.Message) error {
	f.calls++
	f.module, f.user, f.msg = module, userID, m
	return nil
}

func TestCallsReachTheMailer(t *testing.T) {
	t.Cleanup(mail.ResetForTest)
	ctx := context.Background()
	msg := mail.Message{Subject: "Hi", Text: "body"}
	if err := mail.ToOwners(ctx, "example", msg); !errors.Is(err, mail.ErrNoMailer) {
		t.Fatalf("no mailer: %v", err)
	}
	f := &fake{}
	mail.SetMailerForTest(f)
	if err := mail.ToOwners(ctx, "example", msg); err != nil || f.calls != 1 || f.module != "example" || f.msg != msg {
		t.Fatalf("owners: %v %+v", err, f)
	}
	if err := mail.ToUser(ctx, "example", "u1", msg); err != nil || f.calls != 2 || f.user != "u1" {
		t.Fatalf("user: %v %+v", err, f)
	}
}

func TestInvalidMessageIsRefused(t *testing.T) {
	t.Cleanup(mail.ResetForTest)
	f := &fake{}
	mail.SetMailerForTest(f)
	ctx := context.Background()
	for name, m := range map[string]mail.Message{
		"CR/LF subject": {Subject: "a\r\nBcc: x@example.test", Text: "body"},
		"empty text":    {Subject: "Hi"},
	} {
		if err := mail.ToOwners(ctx, "example", m); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := mail.ToUser(ctx, "Bad Name", "u1", mail.Message{Subject: "Hi", Text: "x"}); err == nil {
		t.Error("bad module name: no error")
	}
	if f.calls != 0 {
		t.Fatalf("mailer called %d times", f.calls)
	}
}
