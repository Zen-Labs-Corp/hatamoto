package email

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/email/smtptest"
)

func TestCheckPassesWithoutData(t *testing.T) {
	cases := []struct {
		name string
		mode smtptest.Mode
		sec  Security
	}{
		{"tls", smtptest.ImplicitTLS, SecurityTLS},
		{"starttls", smtptest.StartTLS, SecurityStarttls},
		{"none", smtptest.Plain, SecurityNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := smtptest.Start(t, smtptest.Options{Mode: tc.mode, User: "u", Pass: "p"})
			cfg := sinkConfig(srv, tc.sec)
			cfg.SMTPUsername, cfg.SMTPPassword = "u", "p"

			if err := Check(context.Background(), cfg); err != nil {
				t.Fatalf("Check: %v", err)
			}
			for _, verb := range []string{"MAIL FROM", "RSET"} {
				if !srv.HasCommand(verb) {
					t.Errorf("expected %s, got %v", verb, srv.Commands())
				}
			}
			if srv.HasCommand("DATA") || len(srv.Messages()) != 0 {
				t.Fatalf("Check must never issue DATA: %v", srv.Commands())
			}
		})
	}
}

func TestCheckReportsRejectedAuth(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{User: "u", Pass: "p", RejectAuth: true})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.SMTPUsername, cfg.SMTPPassword = "u", "p"

	err := Check(context.Background(), cfg)
	var ce *CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *CheckError", err)
	}
	if ce.Stage != StageAuth || ce.Code != 535 {
		t.Fatalf("stage/code = %s/%d, want auth/535", ce.Stage, ce.Code)
	}
	want := "Could not sign in to " + srv.Addr() + " (535 5.7.8 Authentication failed)."
	if got := ce.UserMessage(); got != want {
		t.Fatalf("UserMessage = %q, want %q", got, want)
	}
}

func TestCheckGivesUpOnBlackholedPort(t *testing.T) {
	t.Parallel()
	srv := smtptest.Start(t, smtptest.Options{Blackhole: true})

	start := time.Now()
	err := Check(context.Background(), sinkConfig(srv, SecurityNone))
	elapsed := time.Since(start)

	var ce *CheckError
	if !errors.As(err, &ce) || ce.Stage != StageConnect {
		t.Fatalf("err = %v, want connect-stage CheckError", err)
	}
	if elapsed > 11*time.Second {
		t.Fatalf("Check took %s, want <= 10s cap", elapsed)
	}
}

func TestCheckHonoursContextCancel(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Blackhole: true})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := Check(ctx, sinkConfig(srv, SecurityNone)); err == nil {
		t.Fatal("expected error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancel did not unblock Check")
	}
}

func TestCheckConnectionRefused(t *testing.T) {
	err := Check(context.Background(), Config{SMTPHost: "127.0.0.1", SMTPPort: 1, Security: SecurityNone})
	var ce *CheckError
	if !errors.As(err, &ce) || ce.Stage != StageConnect {
		t.Fatalf("err = %v, want connect-stage CheckError", err)
	}
}

// rawSMTP serves one connection on a loopback port: it sends greeting and then
// answers every command with reply (or hangs up when reply is empty).
func rawSMTP(t *testing.T, greeting, replyToEHLO string) Config {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(greeting))
		if replyToEHLO == "" {
			return
		}
		// Answer every command (EHLO, then net/smtp's HELO fallback) the same way.
		r := bufio.NewReader(c)
		for {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			_, _ = c.Write([]byte(replyToEHLO))
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return Config{SMTPHost: "127.0.0.1", SMTPPort: port, Security: SecurityStarttls}
}

func TestCheckConnectFailuresShowFixedMessage(t *testing.T) {
	want := func(cfg Config) string {
		return "Could not connect to " + net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort)) +
			". Check the host, port and security."
	}
	refused := Config{SMTPHost: "127.0.0.1", SMTPPort: 1, Security: SecurityNone}
	banner := rawSMTP(t, "SSH-2.0-OpenSSH_9.6\r\n", "")
	for name, cfg := range map[string]Config{"refused": refused, "non-SMTP banner": banner} {
		err := Check(context.Background(), cfg)
		var ce *CheckError
		if !errors.As(err, &ce) || ce.Stage != StageConnect {
			t.Fatalf("%s: err = %v, want connect-stage CheckError", name, err)
		}
		if got := ce.UserMessage(); got != want(cfg) {
			t.Errorf("%s: UserMessage = %q, want %q", name, got, want(cfg))
		}
		if ce.Error() == "" || ce.Unwrap() == nil {
			t.Errorf("%s: raw error must stay available for the log", name)
		}
	}
}

// A server that greets but rejects EHLO is a connect failure with its reply,
// not "does not offer STARTTLS".
func TestCheckEHLORejectIsNotMislabelledAsNoStarttls(t *testing.T) {
	cfg := rawSMTP(t, "220 hi\r\n", "554 5.7.1 policy says no\r\n")
	err := Check(context.Background(), cfg)
	var ce *CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *CheckError", err)
	}
	if ce.Stage != StageConnect || ce.Code != 554 || strings.Contains(ce.UserMessage(), "STARTTLS") {
		t.Fatalf("stage=%s code=%d msg=%q; want connect/554 without STARTTLS", ce.Stage, ce.Code, ce.UserMessage())
	}
}

func TestCheckRejectsUnknownSecurityWithoutDialling(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	err := Check(context.Background(), sinkConfig(srv, Security("bogus")))
	var ce *CheckError
	if !errors.As(err, &ce) || ce.Stage != StageTLS {
		t.Fatalf("err = %v, want tls-stage CheckError", err)
	}
	if len(srv.Commands()) != 0 {
		t.Fatalf("unknown security reached the server: %v", srv.Commands())
	}
}

// The context-cancel close and the implicit-TLS wrap must not race on the
// connection variable; run with -race.
func TestDialImplicitTLSCancelRace(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.ImplicitTLS})
	cfg := sinkConfig(srv, SecurityTLS)
	for i := 0; i < 300; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%30)*100*time.Microsecond)
		if _, closeConn, err := dial(ctx, cfg); err == nil {
			closeConn()
		}
		cancel()
	}
}
