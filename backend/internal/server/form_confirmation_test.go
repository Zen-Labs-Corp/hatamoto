package server

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/form"
)

type fakeConfirmationSender struct {
	mu    sync.Mutex
	to    []string
	data  []email.FormConfirmationData
	calls chan struct{}
}

func newFakeConfirmationSender() *fakeConfirmationSender {
	return &fakeConfirmationSender{calls: make(chan struct{}, 10)}
}

func (f *fakeConfirmationSender) SendFormConfirmation(_ context.Context, to string, d email.FormConfirmationData) error {
	f.mu.Lock()
	f.to = append(f.to, to)
	f.data = append(f.data, d)
	f.mu.Unlock()
	f.calls <- struct{}{}
	return nil
}

func testForm(confirm bool, settings func(*form.FormSettings)) *form.Form {
	f := &form.Form{
		ID:   uuid.New(),
		Name: "Contact",
		Slug: "contact",
		Fields: []form.FormField{
			{Name: "name", Type: form.FieldTypeText},
			{Name: "work_email", Type: form.FieldTypeEmail},
			{Name: "message", Type: form.FieldTypeTextarea},
		},
		Settings: form.FormSettings{ConfirmationEmail: confirm, SuccessMessage: "Thanks, we will be in touch."},
	}
	if settings != nil {
		settings(&f.Settings)
	}
	return f
}

func TestFormConfirmation(t *testing.T) {
	data := map[string]interface{}{"name": " Ada ", "work_email": "ada@example.com", "message": "secret body"}

	t.Run("uses the email-typed field, the name field and the success message", func(t *testing.T) {
		to, msg, ok := formConfirmation(testForm(true, nil), data, "Example Site")
		if !ok || to != "ada@example.com" {
			t.Fatalf("got ok=%v to=%q", ok, to)
		}
		want := email.FormConfirmationData{FormName: "Contact", Name: "Ada", Message: "Thanks, we will be in touch.", SiteName: "Example Site"}
		if msg != want {
			t.Fatalf("got %+v want %+v", msg, want)
		}
	})

	t.Run("confirmation_message wins over success_message", func(t *testing.T) {
		f := testForm(true, func(s *form.FormSettings) { s.ConfirmationMessage = "Check your inbox." })
		_, msg, _ := formConfirmation(f, data, "S")
		if msg.Message != "Check your inbox." {
			t.Fatalf("message = %q", msg.Message)
		}
	})

	t.Run("falls back to a default message", func(t *testing.T) {
		f := testForm(true, func(s *form.FormSettings) { s.SuccessMessage = "" })
		_, msg, _ := formConfirmation(f, data, "S")
		if msg.Message != defaultFormConfirmationMessage {
			t.Fatalf("message = %q", msg.Message)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		if _, _, ok := formConfirmation(testForm(false, nil), data, "S"); ok {
			t.Fatal("expected no confirmation")
		}
	})

	for name, d := range map[string]map[string]interface{}{
		"no email field value":       {"name": "Ada"},
		"invalid address":            {"work_email": "not-an-email"},
		"display-name form rejected": {"work_email": "Ada <ada@example.com>"},
		"header injection":           {"work_email": "a@example.com\r\nBcc: x@example.com"},
		"non-string value":           {"work_email": 42},
		"email in a non-email field": {"message": "ada@example.com"},
	} {
		t.Run("skips: "+name, func(t *testing.T) {
			if _, _, ok := formConfirmation(testForm(true, nil), d, "S"); ok {
				t.Fatal("expected no confirmation")
			}
		})
	}

	t.Run("name omitted when multi-line", func(t *testing.T) {
		for _, n := range []string{"line1\nline2"} {
			_, msg, ok := formConfirmation(testForm(true, nil), map[string]interface{}{"work_email": "a@example.com", "name": n}, "S")
			if !ok || msg.Name != "" {
				t.Fatalf("name %q: ok=%v name=%q", n, ok, msg.Name)
			}
		}
	})
}

func TestSendFormConfirmationEmail_OnePerSubmission(t *testing.T) {
	fake := newFakeConfirmationSender()
	s := &Server{
		cfg:                    &config.Config{},
		logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
		formConfirmationSender: fake,
	}
	s.cfg.Hatamoto.SiteName = "Example Site"
	sub := &form.FormSubmission{ID: uuid.New()}
	data := map[string]interface{}{"name": "Ada", "work_email": "ada@example.com"}

	s.sendFormConfirmationEmail(testForm(true, nil), sub, data)
	select {
	case <-fake.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("confirmation was not sent")
	}
	time.Sleep(50 * time.Millisecond)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.to) != 1 || fake.to[0] != "ada@example.com" || fake.data[0].SiteName != "Example Site" {
		t.Fatalf("sent to=%v data=%+v", fake.to, fake.data)
	}
}

func TestSendFormConfirmationEmail_NoSendWhenDisabledOrNoSender(t *testing.T) {
	fake := newFakeConfirmationSender()
	s := &Server{cfg: &config.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), formConfirmationSender: fake}
	sub := &form.FormSubmission{ID: uuid.New()}
	data := map[string]interface{}{"work_email": "ada@example.com"}

	s.sendFormConfirmationEmail(testForm(false, nil), sub, data)
	(&Server{cfg: &config.Config{}, logger: s.logger}).sendFormConfirmationEmail(testForm(true, nil), sub, data) // nil sender: must not panic
	select {
	case <-fake.calls:
		t.Fatal("unexpected send")
	case <-time.After(100 * time.Millisecond):
	}
}
