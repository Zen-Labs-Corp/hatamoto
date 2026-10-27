package email

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func sendAndFromHeader(t *testing.T, svc *Service, sink *smtptest.Server) string {
	t.Helper()
	if _, err := svc.SendTrackedWith(context.Background(), SendOptions{}, []string{"to@example.test"}, "s", "<p>h</p>", "h"); err != nil {
		t.Fatal(err)
	}
	msgs := sink.Messages()
	for _, line := range strings.Split(msgs[len(msgs)-1].Data, "\r\n") {
		if strings.HasPrefix(line, "From: ") {
			return line
		}
	}
	t.Fatal("no From header")
	return ""
}

// A From name that follows the site is read at send time, so a rename applies
// to every mail with no re-apply; the configured name is only the fallback.
func TestFromNameFollowsSiteNameAtSendTime(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "") // CI sets the kill switch; this test needs real sends
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := NewService(Config{
		SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "hi@example.test",
		FromName: "Placeholder", FromNameFromSite: true, Security: SecurityNone, AllowLocalhost: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	name := "Acme Bakery"
	svc.SetSiteNameSource(func(context.Context) string { return name })

	if got := sendAndFromHeader(t, svc, sink); !strings.Contains(got, "Acme Bakery <hi@example.test>") {
		t.Fatalf("From = %q", got)
	}
	if svc.FromName() != "Acme Bakery" {
		t.Fatalf("FromName() = %q", svc.FromName())
	}
	name = "Renamed"
	if got := sendAndFromHeader(t, svc, sink); !strings.Contains(got, "Renamed <") {
		t.Fatalf("rename not followed: %q", got)
	}
	name = ""
	if got := sendAndFromHeader(t, svc, sink); !strings.Contains(got, "Placeholder <") {
		t.Fatalf("empty lookup must fall back: %q", got)
	}

	// A pinned name is never replaced.
	cfg := svc.Config()
	cfg.FromNameFromSite, cfg.FromName = false, "Pinned"
	svc.Reload(cfg)
	name = "Acme Bakery"
	if got := sendAndFromHeader(t, svc, sink); !strings.Contains(got, "Pinned <") {
		t.Fatalf("pinned name overridden: %q", got)
	}
}
