package database

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

func TestSocketEligible(t *testing.T) {
	for _, tc := range []struct {
		dsn  string
		want bool
	}{
		{"postgres://u:p@127.0.0.1:5432/db", true},
		{"postgres://u:p@127.0.0.2:5432/db", true},
		{"postgres://u:p@localhost:5432/db", true},
		{"postgres://u:p@[::1]:5432/db", true},
		{"postgres://u:p@db:5432/db", false},
		{"postgres://u:p@db.example.net:5432/db", false},
		{"postgres://u:p@10.0.0.5:5432/db", false},
		{"postgres://u:p@127.0.0.1:5432,127.0.0.2:5432/db", false},
		{"postgres://u:p@127.0.0.1:5432/db?host=/var/run/postgresql", false},
		{"host=/var/run/postgresql user=u dbname=db", false},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=require", false},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=verify-ca", false},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=verify-full", false},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=prefer", true},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=allow", true},
		{"postgres://u:p@127.0.0.1:5432/db?sslmode=disable", true},
	} {
		cc, err := pgconn.ParseConfig(tc.dsn)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsn, err)
		}
		if got := socketEligible(cc); got != tc.want {
			t.Errorf("%s: eligible = %v, want %v", tc.dsn, got, tc.want)
		}
	}
}

// shortDir makes a directory with a short path: unix socket paths are limited
// to about 108 bytes, which t.TempDir() can exceed.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "pgs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// fakePG listens on <dir>/.s.PGSQL.<port> and answers the startup message per
// mode: "ok" authenticates, "28000"/"28P01" refuse with that code, "hang"
// never answers. Dials counts accepted connections.
type fakePGServer struct{ dials chan struct{} }

func fakePG(t *testing.T, dir string, port uint16, mode string) *fakePGServer {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(dir, fmt.Sprintf(".s.PGSQL.%d", port)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &fakePGServer{dials: make(chan struct{}, 16)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.dials <- struct{}{}
			go func() {
				defer conn.Close()
				be := pgproto3.NewBackend(conn, conn)
				for {
					msg, err := be.ReceiveStartupMessage()
					if err != nil {
						return
					}
					switch msg.(type) {
					case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
						if _, err := conn.Write([]byte{'N'}); err != nil {
							return
						}
						continue
					}
					break
				}
				switch mode {
				case "hang":
					time.Sleep(5 * time.Second)
				case "ok":
					be.Send(&pgproto3.AuthenticationOk{})
					be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
					be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
					_ = be.Flush()
					// Hold the connection until the client terminates.
					_, _ = be.Receive()
				default:
					be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", Code: mode, Message: "refused"})
					_ = be.Flush()
				}
			}()
		}
	}()
	return srv
}

func parseCfg(t *testing.T, dsn string) *pgconn.Config {
	t.Helper()
	cc, err := pgconn.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return cc
}

const loopbackDSN = "postgres://u:s3cret@127.0.0.1:54399/db"

func TestPreferUnixSocketSwitchesOnAuthenticatedProbe(t *testing.T) {
	dir := shortDir(t)
	fakePG(t, dir, 54399, "ok")
	cc := parseCfg(t, loopbackDSN+"?sslmode=prefer")
	origFallbacks := len(cc.Fallbacks)
	origTLS := cc.TLSConfig

	out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{dir}})
	if !out.Used {
		t.Fatalf("expected the socket to be used, reason %q", out.Reason)
	}
	if cc.Host != dir || cc.TLSConfig != nil {
		t.Fatalf("host %q tls %v", cc.Host, cc.TLSConfig)
	}
	if len(cc.Fallbacks) != origFallbacks+1 {
		t.Fatalf("fallbacks = %d, want %d", len(cc.Fallbacks), origFallbacks+1)
	}
	if fb := cc.Fallbacks[0]; fb.Host != "127.0.0.1" || fb.Port != 54399 || fb.TLSConfig != origTLS {
		t.Fatalf("first fallback = %+v", fb)
	}
	note := out.note("db")
	if !strings.Contains(note, "db: using unix socket "+filepath.Join(dir, ".s.PGSQL.54399")) {
		t.Fatalf("note %q", note)
	}
	if strings.Contains(note, "s3cret") {
		t.Fatal("note leaks the password")
	}

	// Copy keeps the fallbacks, deep-copied.
	cp := cc.Copy()
	if cp.Host != dir || len(cp.Fallbacks) != len(cc.Fallbacks) {
		t.Fatalf("copy: host %q fallbacks %d", cp.Host, len(cp.Fallbacks))
	}
	cp.Fallbacks[0].Host = "changed"
	if cc.Fallbacks[0].Host != "127.0.0.1" {
		t.Fatal("Copy shares fallback entries")
	}
}

func TestPreferUnixSocketRefusedKeepsTCP(t *testing.T) {
	for _, code := range []string{"28000", "28P01"} {
		t.Run(code, func(t *testing.T) {
			dir := shortDir(t)
			fakePG(t, dir, 54399, code)
			cc := parseCfg(t, loopbackDSN)
			origFallbacks := len(cc.Fallbacks)
			out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{dir}})
			if out.Used || cc.Host != "127.0.0.1" || len(cc.Fallbacks) != origFallbacks {
				t.Fatalf("config changed: %+v host %q", out, cc.Host)
			}
			want := "db: unix socket " + filepath.Join(dir, ".s.PGSQL.54399") + " refused (auth); using tcp 127.0.0.1:54399"
			if got := out.note("db"); got != want {
				t.Fatalf("note %q, want %q", got, want)
			}
		})
	}
}

func TestPreferUnixSocketTimeout(t *testing.T) {
	old := probeTimeout
	probeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })
	dir := shortDir(t)
	fakePG(t, dir, 54399, "hang")
	cc := parseCfg(t, loopbackDSN)
	start := time.Now()
	out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{dir}})
	if time.Since(start) > time.Second {
		t.Fatalf("probe took %s", time.Since(start))
	}
	if out.Used || out.Reason != "timeout" || cc.Host != "127.0.0.1" {
		t.Fatalf("outcome %+v host %q", out, cc.Host)
	}
}

func TestPreferUnixSocketNoSocketFileNoDial(t *testing.T) {
	empty := shortDir(t)
	cc := parseCfg(t, loopbackDSN)
	out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{empty}})
	if out != (socketOutcome{}) || out.note("db") != "" || cc.Host != "127.0.0.1" {
		t.Fatalf("outcome %+v host %q", out, cc.Host)
	}
}

func TestPreferUnixSocketRegularFileIsNotASocket(t *testing.T) {
	dir := shortDir(t)
	if err := os.WriteFile(filepath.Join(dir, ".s.PGSQL.54399"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := candidateSocket([]string{dir}, 54399); ok {
		t.Fatal("a regular file was taken for a socket")
	}
}

func TestPreferUnixSocketDisabledDoesNotDial(t *testing.T) {
	dir := shortDir(t)
	srv := fakePG(t, dir, 54399, "ok")
	cc := parseCfg(t, loopbackDSN)
	out := preferUnixSocket(context.Background(), cc, SocketOptions{Disabled: true, Dirs: []string{dir}})
	if out != (socketOutcome{}) || cc.Host != "127.0.0.1" {
		t.Fatalf("outcome %+v host %q", out, cc.Host)
	}
	select {
	case <-srv.dials:
		t.Fatal("disabled option still dialled")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPreferUnixSocketIneligibleDoesNotDial(t *testing.T) {
	dir := shortDir(t)
	srv := fakePG(t, dir, 54399, "ok")
	cc := parseCfg(t, "postgres://u:p@db.example.net:54399/db")
	if out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{dir}}); out != (socketOutcome{}) {
		t.Fatalf("outcome %+v", out)
	}
	select {
	case <-srv.dials:
		t.Fatal("remote host was probed over the socket")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPreferUnixSocketFirstExistingCandidate(t *testing.T) {
	empty, live := shortDir(t), shortDir(t)
	fakePG(t, live, 54399, "ok")
	cc := parseCfg(t, loopbackDSN)
	out := preferUnixSocket(context.Background(), cc, SocketOptions{Dirs: []string{empty, live}})
	if !out.Used || cc.Host != live {
		t.Fatalf("outcome %+v host %q", out, cc.Host)
	}
}

func TestDefaultSocketDirsExcludeTmp(t *testing.T) {
	for _, d := range DefaultSocketDirs {
		if d == "/tmp" || strings.HasPrefix(d, "/tmp/") {
			t.Fatalf("default dir %q is world-writable", d)
		}
	}
}
