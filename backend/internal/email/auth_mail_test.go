package email

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/email/smtptest"
)

func TestAuthMailTemplates(t *testing.T) {
	svc := newTestService(t)
	render := func(name string, data any) string {
		var b bytes.Buffer
		if err := svc.templates.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	url := "https://example.test/admin/reset?token=abc_DEF-123"
	out := render("password_reset.html", ResetMailData{SiteName: "Acme <Co>", URL: url, ExpiresIn: "1 hour"})
	for _, want := range []string{`href="` + url + `"`, "Acme &lt;Co&gt;", "expires in 1 hour", "Choose a new password"} {
		if !strings.Contains(out, want) {
			t.Errorf("reset: missing %q in\n%s", want, out)
		}
	}
	out = render("invite.html", InviteMailData{SiteName: "Acme", InviterName: "Olga", Role: "editor", URL: url, ExpiresIn: "7 days"})
	for _, want := range []string{"Olga invited you", "as editor", `href="` + url + `"`, "expires in 7 days"} {
		if !strings.Contains(out, want) {
			t.Errorf("invite: missing %q", want)
		}
	}
	if out := render("invite.html", InviteMailData{SiteName: "Acme", Role: "owner", URL: url, ExpiresIn: "7 days"}); !strings.Contains(out, "You are invited") {
		t.Error("invite without an inviter name")
	}
}

// An unconfigured service accepts the sends without error (nothing is sent).
func TestAuthMailSendsAreNoOpsWhenUnconfigured(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if err := svc.SendPasswordReset(ctx, "a@example.test", ResetMailData{SiteName: "S", URL: "https://x/y", ExpiresIn: "1 hour"}); err != nil {
		t.Error(err)
	}
	if err := svc.SendInvite(ctx, "a@example.test", InviteMailData{SiteName: "S", Role: "editor", URL: "https://x/y", ExpiresIn: "7 days"}); err != nil {
		t.Error(err)
	}
	if err := svc.SendTest(ctx, "a@example.test", "S <x>"); err != nil {
		t.Error(err)
	}
}

func TestAdminAddressMailTemplates(t *testing.T) {
	svc := newTestService(t)
	render := func(name string, data any) string {
		var b bytes.Buffer
		if err := svc.templates.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render("admin_address_changed.html", AdminAddressChangedData{
		SiteName: "Acme", Address: "https://acme.test/k7qm-x2rf-9tdc", ChangedBy: "<script>alert(1)</script>",
		When: "25 Oct 2026, 14:02 UTC", IP: "203.0.113.7",
	})
	for _, want := range []string{"https://acme.test/k7qm-x2rf-9tdc", "25 Oct 2026, 14:02 UTC", "203.0.113.7", "&lt;script&gt;", "hatamoto admin-url"} {
		if !strings.Contains(out, want) {
			t.Errorf("changed: missing %q", want)
		}
	}
	if strings.Contains(out, "<script>") {
		t.Error("changed: ChangedBy not escaped")
	}
	out2 := render("admin_address_setup.html", AdminAddressSetupData{SiteName: "Acme <Co>", Address: "/k7qm-x2rf-9tdc"})
	for _, want := range []string{"/k7qm-x2rf-9tdc", "Acme &lt;Co&gt;"} {
		if !strings.Contains(out2, want) {
			t.Errorf("setup: missing %q", want)
		}
	}
	for _, s := range []string{out, out2} {
		if strings.ContainsAny(s, "\u2013\u2014") {
			t.Error("template contains an en or em dash")
		}
	}
}

func TestAdminAddressMailsReachTheSink(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "") // the gate sets the kill switch, which short-circuits sends
	sink := smtptest.Start(t, smtptest.Options{})
	svc := newTestService(t)
	svc.Reload(Config{SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "from@example.test", Security: SecurityNone})
	ctx := context.Background()
	if err := svc.SendAdminAddressChanged(ctx, "owner@example.test", AdminAddressChangedData{
		SiteName: "Acme", Address: "https://acme.test/k7qm-x2rf-9tdc", ChangedBy: "Olga", When: time.Date(2026, 10, 25, 14, 2, 0, 0, time.UTC).Format("2 Jan 2006, 15:04 MST"), IP: "203.0.113.7",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendAdminAddressSetup(ctx, "owner@example.test", AdminAddressSetupData{SiteName: "Acme", Address: "https://acme.test/k7qm-x2rf-9tdc"}); err != nil {
		t.Fatal(err)
	}
	msgs := sink.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	for _, want := range []string{"Olga", "25 Oct 2026, 14:02 UTC", "203.0.113.7", "https://acme.test/k7qm-x2rf-9tdc", "The admin address of Acme changed"} {
		if !strings.Contains(msgs[0].Data, want) {
			t.Errorf("changed mail: missing %q", want)
		}
	}
	if !strings.Contains(msgs[1].Data, "Your admin address for Acme") {
		t.Errorf("setup mail subject missing")
	}
	for _, m := range msgs {
		if strings.ContainsAny(m.Data, "\u2013\u2014") {
			t.Error("mail contains an en or em dash")
		}
	}
}
