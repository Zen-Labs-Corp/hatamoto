package modhost

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeMailer struct {
	calls  int
	module string
	user   string
	msg    Message
}

func (f *fakeMailer) ToOwners(_ context.Context, module string, m Message) error {
	f.calls++
	f.module, f.msg = module, m
	return nil
}

func (f *fakeMailer) ToUser(_ context.Context, module, userID string, m Message) error {
	f.calls++
	f.module, f.user, f.msg = module, userID, m
	return nil
}

func TestMail(t *testing.T) {
	t.Cleanup(Reset)
	ctx := context.Background()
	msg := Message{Subject: "Hello", Text: "Body"}
	if err := MailToOwners(ctx, "example", msg); !errors.Is(err, ErrNoMailer) {
		t.Fatalf("owners without mailer: %v", err)
	}
	if err := MailToUser(ctx, "example", "u1", msg); !errors.Is(err, ErrNoMailer) {
		t.Fatalf("user without mailer: %v", err)
	}

	f := &fakeMailer{}
	SetMailer(f)
	if err := MailToOwners(ctx, "example", msg); err != nil || f.calls != 1 || f.module != "example" || f.msg != msg {
		t.Fatalf("owners: err=%v fake=%+v", err, f)
	}
	if err := MailToUser(ctx, "example", "u1", msg); err != nil || f.calls != 2 || f.user != "u1" {
		t.Fatalf("user: err=%v fake=%+v", err, f)
	}

	bad := map[string]struct {
		module string
		msg    Message
	}{
		"bad module":    {"Ex", msg},
		"empty subject": {"example", Message{Text: "x"}},
		"CR subject":    {"example", Message{Subject: "a\rb", Text: "x"}},
		"LF subject":    {"example", Message{Subject: "a\nb", Text: "x"}},
		"long subject":  {"example", Message{Subject: strings.Repeat("a", 256), Text: "x"}},
		"empty text":    {"example", Message{Subject: "a"}},
	}
	for name, c := range bad {
		if err := MailToOwners(ctx, c.module, c.msg); err == nil {
			t.Errorf("%s: owners accepted", name)
		}
		if err := MailToUser(ctx, c.module, "u1", c.msg); err == nil {
			t.Errorf("%s: user accepted", name)
		}
	}
	if f.calls != 2 {
		t.Fatalf("invalid messages reached the mailer: %d calls", f.calls)
	}

	mustPanic(t, "nil mailer", func() { SetMailer(nil) })
	var typedNil *fakeMailer
	mustPanic(t, "typed nil mailer", func() { SetMailer(typedNil) })

	Reset()
	if err := MailToOwners(ctx, "example", msg); !errors.Is(err, ErrNoMailer) {
		t.Fatalf("after reset: %v", err)
	}
}
