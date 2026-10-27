package email

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func sinkConfig(srv *smtptest.Server, sec Security) Config {
	return Config{
		SMTPHost:    srv.Host(),
		SMTPPort:    srv.Port(),
		FromAddress: "from@example.com",
		Security:    sec,
		RootCAs:     srv.RootCAs(),
	}
}

func newSink(t *testing.T, cfg Config) *Service {
	t.Helper()
	t.Setenv("EMAIL_DISABLED", "")
	svc, err := NewService(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestSendThroughEverySecurityMode(t *testing.T) {
	cases := []struct {
		name string
		mode smtptest.Mode
		sec  Security
		user string
	}{
		{"implicit TLS", smtptest.ImplicitTLS, SecurityTLS, "u"},
		{"STARTTLS", smtptest.StartTLS, SecurityStarttls, "u"},
		{"STARTTLS by zero value", smtptest.StartTLS, "", "u"},
		{"none on loopback", smtptest.Plain, SecurityNone, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := smtptest.Start(t, smtptest.Options{Mode: tc.mode, User: tc.user, Pass: "p"})
			cfg := sinkConfig(srv, tc.sec)
			if tc.user != "" {
				cfg.SMTPUsername, cfg.SMTPPassword = tc.user, "p"
			}
			svc := newSink(t, cfg)

			if err := svc.Send(context.Background(), []string{"to@example.com"}, "Hello there", "<p>hi</p>", "hi"); err != nil {
				t.Fatalf("send: %v", err)
			}
			msgs := srv.Messages()
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			if msgs[0].To[0] != "to@example.com" || !strings.Contains(msgs[0].Data, "Subject: Hello there") {
				t.Fatalf("unexpected message: %+v", msgs[0])
			}
		})
	}
}

func TestStarttlsRefusedWhenServerDoesNotOfferIt(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc := newSink(t, sinkConfig(srv, SecurityStarttls))
	err := svc.Send(context.Background(), []string{"to@example.com"}, "s", "h", "t")
	var ce *CheckError
	if !errors.As(err, &ce) || ce.Stage != StageTLS {
		t.Fatalf("err = %v, want tls-stage CheckError", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("message must not be sent in the clear")
	}
}
