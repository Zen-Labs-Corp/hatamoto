package smtptest_test

import (
	"crypto/tls"
	"net"
	"net/smtp"
	"strconv"
	"testing"
	"time"

	"hatamoto.dev/core/internal/email/smtptest"
)

func deliver(t *testing.T, c *smtp.Client, auth smtp.Auth, withData bool) error {
	t.Helper()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail("from@example.com"); err != nil {
		return err
	}
	if err := c.Rcpt("to@example.com"); err != nil {
		return err
	}
	if withData {
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte("Subject: hi\r\n\r\nbody\r\n")); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	return c.Quit()
}

func TestPlain(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{})
	c, err := smtp.Dial(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, c, nil, true); err != nil {
		t.Fatal(err)
	}
	msgs := srv.Messages()
	if len(msgs) != 1 || msgs[0].From != "from@example.com" || msgs[0].To[0] != "to@example.com" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestStartTLSWithAuth(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.StartTLS, User: "u", Pass: "p"})
	c, err := smtp.Dial(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartTLS(&tls.Config{ServerName: "127.0.0.1", RootCAs: srv.RootCAs()}); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, c, smtp.PlainAuth("", "u", "p", "127.0.0.1"), true); err != nil {
		t.Fatal(err)
	}
	if len(srv.Messages()) != 1 {
		t.Fatalf("messages = %d", len(srv.Messages()))
	}
}

func TestImplicitTLS(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Mode: smtptest.ImplicitTLS})
	conn, err := tls.Dial("tcp", srv.Addr(), &tls.Config{ServerName: "127.0.0.1", RootCAs: srv.RootCAs()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtp.NewClient(conn, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, c, nil, true); err != nil {
		t.Fatal(err)
	}
}

func TestWrongPasswordAndRejectAuth(t *testing.T) {
	for name, opts := range map[string]smtptest.Options{
		"wrong password": {User: "u", Pass: "p"},
		"reject auth":    {User: "u", Pass: "p", RejectAuth: true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := smtptest.Start(t, opts)
			c, err := smtp.Dial(srv.Addr())
			if err != nil {
				t.Fatal(err)
			}
			pass := "wrong"
			if opts.RejectAuth {
				pass = "p"
			}
			err = c.Auth(smtp.PlainAuth("", "u", pass, "127.0.0.1"))
			if err == nil || err.Error()[:3] != "535" {
				t.Fatalf("err = %v, want 535", err)
			}
		})
	}
}

func TestNoDataUnlessSent(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{})
	c, err := smtp.Dial(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, c, nil, false); err != nil {
		t.Fatal(err)
	}
	if srv.HasCommand("DATA") || len(srv.Messages()) != 0 {
		t.Fatalf("DATA recorded without being sent: %v", srv.Commands())
	}
}

func TestBlackholeNeverReplies(t *testing.T) {
	srv := smtptest.Start(t, smtptest.Options{Blackhole: true})
	conn, err := net.Dial("tcp", net.JoinHostPort(srv.Host(), strconv.Itoa(srv.Port())))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 16)); n != 0 || err == nil {
		t.Fatalf("read %d bytes, err %v; want timeout", n, err)
	}
}
