package email

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"hatamoto.dev/core/internal/email/smtptest"
)

func allowAll(netip.Addr) bool { return true }
func denyAll(netip.Addr) bool  { return false }
func refusedErr(t *testing.T, err error) (*CheckError, *AddrRefusedError) {
	t.Helper()
	var ce *CheckError
	var ar *AddrRefusedError
	if !errors.As(err, &ce) || ce.Stage != StageConnect || !errors.As(err, &ar) {
		t.Fatalf("err = %v, want connect-stage CheckError wrapping AddrRefusedError", err)
	}
	return ce, ar
}

func TestNoneDialHookAllowsPrivate(t *testing.T) {
	t.Parallel()
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.NoneAddrAllowed = allowAll
	if err := Check(context.Background(), cfg); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestNoneRefusalMessage(t *testing.T) {
	t.Parallel()
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.NoneAddrAllowed = denyAll
	ce, ar := refusedErr(t, Check(context.Background(), cfg))
	if ar.Addr != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("Addr = %v", ar.Addr)
	}
	msg := ce.UserMessage()
	if !strings.Contains(msg, "Could not connect to 127.0.0.1:") || !strings.Contains(msg, "resolved to 127.0.0.1") ||
		strings.Contains(strings.ToLower(msg), "smtptest") {
		t.Fatalf("message = %q", msg)
	}
	if len(srv.Messages()) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestNoneDialHookRefusesResolvedName(t *testing.T) {
	t.Parallel()
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.SMTPHost = "localhost"
	cfg.NoneAddrAllowed = denyAll
	refusedErr(t, Check(context.Background(), cfg))
}

func TestNoneDialHookDefaultRefusesPublic(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"0.0.0.0", "203.0.113.5"} {
		cfg := Config{SMTPHost: host, SMTPPort: 25, FromAddress: "from@example.com", Security: SecurityNone}
		_, ar := refusedErr(t, Check(context.Background(), cfg))
		if ar.Addr != netip.MustParseAddr(host) {
			t.Errorf("%s: Addr = %v", host, ar.Addr)
		}
	}
}

func TestNoneControlUnmapsIPv4Mapped(t *testing.T) {
	t.Parallel()
	ctl := noneControl(func(ip netip.Addr) bool { return !ip.Is4In6() && ip.String() != "203.0.113.5" })
	err := ctl("tcp", "[::ffff:203.0.113.5]:25", syscall.RawConn(nil))
	var ar *AddrRefusedError
	if !errors.As(err, &ar) || ar.Addr != netip.MustParseAddr("203.0.113.5") {
		t.Fatalf("err = %v", err)
	}
	if err := noneControl(allowAll)("tcp", "[fe80::1%eth0]:25", nil); err != nil {
		t.Fatalf("zoned address: %v", err)
	}
}

func TestNoneDialHookOnSendPath(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.NoneAddrAllowed = denyAll
	svc := newSink(t, cfg)
	err := svc.Send(context.Background(), []string{"to@example.com"}, "s", "h", "t")
	refusedErr(t, err)
	if len(srv.Messages()) != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestNoneLoopbackStillAuthenticates(t *testing.T) {
	t.Parallel()
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain, User: "u", Pass: "p"})
	cfg := sinkConfig(srv, SecurityNone)
	cfg.SMTPUsername, cfg.SMTPPassword = "u", "p"
	if err := Check(context.Background(), cfg); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestNoneCredentialsGuardBeforeDial(t *testing.T) {
	t.Parallel()
	cfg := Config{SMTPHost: "10.0.0.5", SMTPPort: 25, SMTPUsername: "u", SMTPPassword: "p", FromAddress: "from@example.com", Security: SecurityNone}
	err := Check(context.Background(), cfg)
	var ce *CheckError
	if !errors.Is(err, ErrNoneCredentials) || !errors.As(err, &ce) || !strings.Contains(ce.UserMessage(), "only works for localhost") {
		t.Fatalf("err = %v", err)
	}
}
