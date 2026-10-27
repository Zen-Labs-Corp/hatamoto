package email

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestFormConfirmationTemplate(t *testing.T) {
	svc := newTestService(t)
	render := func(d FormConfirmationData) string {
		var b bytes.Buffer
		if err := svc.templates.ExecuteTemplate(&b, "form_confirmation.html", d); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	out := render(FormConfirmationData{FormName: "Contact", Name: "Ada", Message: "Thanks <b>x</b>", SiteName: "Example Site"})
	for _, want := range []string{"Thank you, Ada.", "Thanks &lt;b&gt;x&lt;/b&gt;", "Contact", "Example Site"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}

	if out := render(FormConfirmationData{FormName: "Contact", Message: "Thanks", SiteName: "S"}); strings.Contains(out, "Thank you, .") {
		t.Error("greeting rendered without a name")
	}
}

func TestSendFormConfirmation_UnconfiguredIsNoop(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SendFormConfirmation(context.Background(), "a@example.com", FormConfirmationData{FormName: "F", Message: "M", SiteName: "S"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendFormConfirmation(context.Background(), "", FormConfirmationData{}); err != nil {
		t.Fatal(err)
	}
}
