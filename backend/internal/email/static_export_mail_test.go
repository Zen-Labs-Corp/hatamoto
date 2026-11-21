package email

import (
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func exportFailedData() StaticExportFailedData {
	return StaticExportFailedData{SiteName: "Site S", When: "20 Nov 2026 09:30", Error: "upload refused.",
		HostingURL: "https://example.test/admin/settings/hosting"}
}

func TestSendStaticExportFailed(t *testing.T) {
	svc, rec, srv := newLoggedSink(t, smtptest.Options{}, nil)
	if err := svc.SendStaticExportFailed(context.Background(), "owner@example.test", exportFailedData()); err != nil {
		t.Fatal(err)
	}
	head, body := oneMessage(t, srv)
	if !strings.Contains(head, "Static export failed on Site S") {
		t.Errorf("subject:\n%s", head)
	}
	if strings.Contains(head, "List-Unsubscribe") {
		t.Errorf("unsubscribe header on a site alarm:\n%s", head)
	}
	for _, want := range []string{"Site S", "20 Nov 2026 09:30", "upload refused.", "Your previous export is still live.", "https://example.test/admin/settings/hosting"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if len(rec.entries) != 1 {
		t.Fatalf("log = %+v", rec.entries)
	}
	e := rec.entries[0]
	if e.Kind != KindStaticExportFailed || e.BodyText != "" || e.BodyHTML != "" {
		t.Errorf("entry = %+v", e)
	}
	if len(e.Recipients) != 1 || e.Recipients[0] != "owner@example.test" {
		t.Errorf("recipients = %v", e.Recipients)
	}
}

func TestStaticExportFailedKindStorage(t *testing.T) {
	if KindStaticExportFailed.StoresBody() {
		t.Error("the failure notice must not store its body")
	}
	if !KindStaticExportFailed.StoresRecipients() {
		t.Error("owners are staff: recipients are kept")
	}
}

func TestSendStaticExportFailed_Injection(t *testing.T) {
	svc, _, srv := newLoggedSink(t, smtptest.Options{}, nil)
	ctx := context.Background()
	d := exportFailedData()
	d.SiteName = "S\r\nBcc: x@example.test"
	if err := svc.SendStaticExportFailed(ctx, "owner@example.test", d); err != nil {
		t.Fatal(err)
	}
	d.SiteName = "<b>S</b>"
	d.Error = "<script>x</script>"
	if err := svc.SendStaticExportFailed(ctx, "owner@example.test", d); err != nil {
		t.Fatal(err)
	}
	for _, m := range srv.Messages() {
		head, body, _ := strings.Cut(m.Data, "\r\n\r\n")
		if strings.Contains(head, "\r\nBcc:") {
			t.Errorf("header injected:\n%s", head)
		}
		if _, html, ok := strings.Cut(body, "text/html"); ok && (strings.Contains(html, "<script>x") || strings.Contains(html, "<b>S</b>")) {
			t.Errorf("HTML not escaped:\n%s", html)
		}
	}
}
