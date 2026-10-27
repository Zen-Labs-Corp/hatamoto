package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

// checkTimeout caps Check so a firewalled port cannot hang a request.
const checkTimeout = 10 * time.Second

// Stages a CheckError can report.
const (
	StageConnect = "connect"
	StageTLS     = "tls"
	StageAuth    = "auth"
	StageSender  = "sender"
)

// CheckError describes why an SMTP connection check (or the connect phase of
// a send) failed. Code and Msg carry the server's reply when there was one.
type CheckError struct {
	Stage string
	Code  int
	Msg   string
	Host  string
	Port  int
	err   error
}

func (e *CheckError) Error() string {
	return fmt.Sprintf("smtp %s %s:%d: %s", e.Stage, e.Host, e.Port, e.detail())
}

func (e *CheckError) Unwrap() error { return e.err }

func (e *CheckError) detail() string {
	if e.Code != 0 {
		return strconv.Itoa(e.Code) + " " + e.Msg
	}
	return e.Msg
}

// UserMessage is the sentence shown to the owner, for example
// "Could not sign in to smtp.x:587 (535 Authentication failed)."
func (e *CheckError) UserMessage() string {
	where := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	switch e.Stage {
	case StageAuth:
		return fmt.Sprintf("Could not sign in to %s (%s).", where, e.detail())
	case StageTLS:
		return fmt.Sprintf("Could not set up a secure connection to %s (%s).", where, e.detail())
	case StageSender:
		return fmt.Sprintf("%s did not accept the From address (%s).", where, e.detail())
	default:
		if e.Code == 0 {
			// A refused or timed-out dial, or something that is not an SMTP
			// server: never echo the raw error (it can carry another
			// service's banner and tells which ports are open).
			return fmt.Sprintf("Could not connect to %s. Check the host, port and security.", where)
		}
		return fmt.Sprintf("Could not connect to %s (%s).", where, e.detail())
	}
}

func newCheckError(stage string, c Config, err error) *CheckError {
	ce := &CheckError{Stage: stage, Host: c.SMTPHost, Port: c.SMTPPort, Msg: err.Error(), err: err}
	var te *textproto.Error
	if errors.As(err, &te) {
		ce.Code, ce.Msg = te.Code, te.Msg
	}
	return ce
}

// Check verifies c without sending a message: connect, TLS, AUTH, MAIL FROM
// and RSET (never DATA). It gives up after 10 seconds. A failure is a
// *CheckError.
func Check(ctx context.Context, c Config) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	client, closeConn, err := dial(ctx, c)
	if err != nil {
		return err
	}
	defer closeConn()

	if err := client.Mail(c.FromAddress); err != nil {
		return newCheckError(StageSender, c, err)
	}
	if err := client.Reset(); err != nil {
		return newCheckError(StageSender, c, err)
	}
	_ = client.Quit()
	return nil
}

// dial opens an SMTP session per c.Security and authenticates when
// credentials are set. The caller must call the returned close function. All
// I/O is bounded by ctx. Failures are *CheckError.
func dial(ctx context.Context, c Config) (*smtp.Client, func(), error) {
	if !c.Security.Valid() {
		// Never fall through to plain text for a value we do not know.
		return nil, nil, newCheckError(StageTLS, c, fmt.Errorf("unknown security mode %q", c.Security))
	}
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	tlsCfg := &tls.Config{ServerName: c.SMTPHost, RootCAs: c.RootCAs, MinVersion: tls.VersionTLS12}

	conn, err := (&net.Dialer{Timeout: checkTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, newCheckError(StageConnect, c, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Cancelling ctx closes the connection, which unblocks any pending I/O.
	// Closing the raw connection also tears down a TLS session on top of it,
	// and the closure never sees conn reassigned below.
	raw := conn
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	closeAll := func() {
		stop()
		_ = raw.Close()
	}

	if c.Security == SecurityTLS {
		tc := tls.Client(raw, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			closeAll()
			return nil, nil, newCheckError(StageTLS, c, err)
		}
		conn = tc
	}

	client, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		closeAll()
		return nil, nil, newCheckError(StageConnect, c, err)
	}

	if c.Security == "" || c.Security == SecurityStarttls {
		// Say hello first: Extension swallows the EHLO error, which would
		// otherwise read as "does not offer STARTTLS".
		if err := client.Hello("localhost"); err != nil {
			closeAll()
			return nil, nil, newCheckError(StageConnect, c, err)
		}
		if ok, _ := client.Extension("STARTTLS"); !ok {
			closeAll()
			return nil, nil, newCheckError(StageTLS, c, errors.New("the server does not offer STARTTLS"))
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			closeAll()
			return nil, nil, newCheckError(StageTLS, c, err)
		}
	}

	if c.SMTPUsername != "" && c.SMTPPassword != "" {
		auth := smtp.PlainAuth("", c.SMTPUsername, c.SMTPPassword, c.SMTPHost)
		if err := client.Auth(auth); err != nil {
			closeAll()
			return nil, nil, newCheckError(StageAuth, c, err)
		}
	}
	return client, closeAll, nil
}
