// Package smtptest is a minimal in-process SMTP server for tests. It speaks
// just enough of the protocol (EHLO, STARTTLS, AUTH PLAIN, MAIL, RCPT, RSET,
// DATA, QUIT) to exercise the email package over plain, STARTTLS and implicit
// TLS connections, and records everything it receives.
package smtptest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mode selects how the server protects connections.
type Mode int

const (
	// Plain accepts unencrypted connections and does not offer STARTTLS.
	Plain Mode = iota
	// StartTLS offers STARTTLS on a plain connection.
	StartTLS
	// ImplicitTLS wraps every connection in TLS from the first byte.
	ImplicitTLS
)

// Options configures Start.
type Options struct {
	Mode Mode
	// User and Pass, when User is set, are the AUTH PLAIN credentials the
	// server accepts.
	User, Pass string
	// RejectAuth answers every AUTH attempt with 535, whatever the credentials.
	RejectAuth bool
	// Blackhole accepts TCP connections and never replies.
	Blackhole bool
}

// Message is one message accepted through DATA.
type Message struct {
	From string
	To   []string
	Data string
}

// Server is a running fake SMTP server bound to 127.0.0.1.
type Server struct {
	opts Options
	ln   net.Listener
	cert tls.Certificate
	pool *x509.CertPool

	mu       sync.Mutex
	commands []string
	messages []Message
	conns    []net.Conn
	closed   bool
	wg       sync.WaitGroup
}

// Start listens on an ephemeral loopback port and serves until Close (also
// registered with t.Cleanup).
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	cert, pool := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("smtptest: listen: %v", err)
	}
	if opts.Mode == ImplicitTLS {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	s := &Server{opts: opts, ln: ln, cert: cert, pool: pool}
	s.wg.Add(1)
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

// Host returns "127.0.0.1".
func (s *Server) Host() string { return "127.0.0.1" }

// Port returns the listening port.
func (s *Server) Port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

// RootCAs returns a pool trusting the server's self-signed certificate.
func (s *Server) RootCAs() *x509.CertPool { return s.pool }

// Messages returns the messages received so far.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// Commands returns every command line received, verb first (for example
// "MAIL FROM:<a@b>"). AUTH credentials are not recorded.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// HasCommand reports whether any received command starts with verb
// (case-insensitive).
func (s *Server) HasCommand(verb string) bool {
	for _, c := range s.Commands() {
		if strings.HasPrefix(strings.ToUpper(c), strings.ToUpper(verb)) {
			return true
		}
	}
	return false
}

// Close stops the listener and drops open connections.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := s.conns
	s.mu.Unlock()
	_ = s.ln.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		if s.opts.Blackhole {
			continue // hold the connection open, say nothing
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer c.Close()
			s.serve(c)
		}()
	}
}

func (s *Server) record(line string) {
	s.mu.Lock()
	s.commands = append(s.commands, line)
	s.mu.Unlock()
}

func (s *Server) serve(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	var conn net.Conn = c
	r := bufio.NewReader(conn)
	reply := func(format string, a ...any) bool {
		_, err := fmt.Fprintf(conn, format+"\r\n", a...)
		return err == nil
	}
	isTLS := s.opts.Mode == ImplicitTLS
	authed := s.opts.User == "" && !s.opts.RejectAuth
	var from string
	var to []string

	if !reply("220 smtptest ready") {
		return
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.Fields(line + " ")[0])
		if verb != "AUTH" {
			s.record(line)
		} else {
			s.record("AUTH")
		}
		switch verb {
		case "EHLO", "HELO":
			lines := []string{"smtptest"}
			if s.opts.Mode == StartTLS && !isTLS {
				lines = append(lines, "STARTTLS")
			}
			if s.opts.User != "" || s.opts.RejectAuth {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				if !reply("250%s%s", sep, l) {
					return
				}
			}
		case "STARTTLS":
			if s.opts.Mode != StartTLS || isTLS {
				reply("502 STARTTLS not available")
				continue
			}
			reply("220 ready to start TLS")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, isTLS = tc, true
			r = bufio.NewReader(conn)
			from, to = "", nil
		case "AUTH":
			if s.opts.RejectAuth {
				reply("535 5.7.8 Authentication failed")
				continue
			}
			if !s.checkAuth(line, conn, r) {
				reply("535 5.7.8 Authentication failed")
				continue
			}
			authed = true
			reply("235 2.7.0 Authentication successful")
		case "MAIL":
			if !authed {
				reply("530 5.7.0 Authentication required")
				continue
			}
			from = between(line, '<', '>')
			reply("250 OK")
		case "RCPT":
			to = append(to, between(line, '<', '>'))
			reply("250 OK")
		case "RSET":
			from, to = "", nil
			reply("250 OK")
		case "DATA":
			if from == "" || len(to) == 0 {
				reply("503 need MAIL and RCPT first")
				continue
			}
			reply("354 end data with <CR><LF>.<CR><LF>")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(strings.TrimPrefix(l, "."))
			}
			s.mu.Lock()
			s.messages = append(s.messages, Message{From: from, To: to, Data: sb.String()})
			s.mu.Unlock()
			from, to = "", nil
			reply("250 OK queued")
		case "NOOP":
			reply("250 OK")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 command not implemented")
		}
	}
}

// checkAuth handles "AUTH PLAIN [initial-response]".
func (s *Server) checkAuth(line string, conn net.Conn, r *bufio.Reader) bool {
	f := strings.Fields(line)
	if len(f) < 2 || !strings.EqualFold(f[1], "PLAIN") {
		return false
	}
	var enc string
	if len(f) >= 3 {
		enc = f[2]
	} else {
		fmt.Fprint(conn, "334 \r\n")
		l, err := r.ReadString('\n')
		if err != nil {
			return false
		}
		enc = strings.TrimSpace(l)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "\x00")
	return len(parts) == 3 && parts[1] == s.opts.User && parts[2] == s.opts.Pass
}

func between(s string, open, end byte) string {
	i := strings.IndexByte(s, open)
	j := strings.LastIndexByte(s, end)
	if i < 0 || j <= i {
		return ""
	}
	return s[i+1 : j]
}

func selfSigned(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("smtptest: key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "smtptest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("smtptest: cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("smtptest: parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// Addr returns "host:port" for convenience in assertions.
func (s *Server) Addr() string { return s.Host() + ":" + strconv.Itoa(s.Port()) }
