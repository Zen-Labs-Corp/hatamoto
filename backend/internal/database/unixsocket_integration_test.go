//go:build integration

package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/testutil/pgbouncertest"
)

// socketEnv returns the test database URL and the directory holding that
// cluster's unix socket. CI runs against a service container with no socket,
// so the tests skip there.
func socketEnv(t *testing.T) (dsn, sockdir string) {
	t.Helper()
	dsn = os.Getenv("HATAMOTO_TEST_DB_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	sockdir = os.Getenv("HATAMOTO_TEST_PG_SOCKDIR")
	if dsn == "" || sockdir == "" {
		t.Skip("HATAMOTO_TEST_DB_URL or HATAMOTO_TEST_PG_SOCKDIR not set")
	}
	return dsn, sockdir
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// viaTCP reports whether the connection behind q reached the server over TCP.
func viaTCP(t *testing.T, q rowQuerier) bool {
	t.Helper()
	var isNull bool
	if err := q.QueryRow(context.Background(), "SELECT inet_server_addr() IS NULL").Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	return !isNull
}

func socketTestConfig(dsn, direct string, dirs []string) config.DatabaseConfig {
	return config.DatabaseConfig{
		URL: dsn, DirectURL: direct, SocketDirs: dirs, MaxConnections: 2,
		MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
	}
}

func dsnPort(t *testing.T, dsn string) uint16 {
	t.Helper()
	cc, err := pgconn.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return cc.Port
}

func TestUnixSocket_AllPoolsUseSocket(t *testing.T) {
	dsn, sockdir := socketEnv(t)
	ctx := context.Background()
	// A distinct string with the same user and database: the real Direct path.
	direct := dsn + "&application_name=direct"
	db, err := New(ctx, socketTestConfig(dsn, direct, []string{sockdir}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	if viaTCP(t, db.Pool()) {
		t.Error("app pool used TCP")
	}
	dp, closeDP, err := db.Direct().Pool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDP()
	if viaTCP(t, dp) {
		t.Error("Direct.Pool used TCP")
	}
	conn, release, err := db.Direct().Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if viaTCP(t, conn) {
		t.Error("Direct.Session used TCP")
	}
	// Plugin pools are built from Direct().ConnConfig().
	pc, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig = db.Direct().ConnConfig()
	pp, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	if viaTCP(t, pp) {
		t.Error("plugin-style pool used TCP")
	}

	notes := db.TransportNotes()
	if len(notes) != 2 || !strings.Contains(notes[0], "using unix socket") || !strings.Contains(notes[1], "using unix socket") {
		t.Fatalf("notes = %q", notes)
	}
}

func TestUnixSocket_TCPWhenNoSocketFile(t *testing.T) {
	dsn, _ := socketEnv(t)
	db, err := New(context.Background(), socketTestConfig(dsn, "", []string{shortDir(t)}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if !viaTCP(t, db.Pool()) {
		t.Error("expected TCP without a socket file")
	}
	if n := db.TransportNotes(); len(n) != 0 {
		t.Fatalf("notes = %q", n)
	}
}

func TestUnixSocket_TCPWhenPortHasNoSocket(t *testing.T) {
	dsn, sockdir := socketEnv(t)
	direct := pgbouncertest.ScratchDB(t, dsn)
	pooled := pgbouncertest.Start(t, direct, 1) // its port has no socket file in sockdir
	db, err := New(context.Background(), socketTestConfig(pooled, "", []string{sockdir}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if !viaTCP(t, db.Pool()) {
		t.Error("expected TCP for a port without a socket")
	}
}

func TestUnixSocket_TCPWhenSocketRefusesAuth(t *testing.T) {
	dsn, _ := socketEnv(t)
	port := dsnPort(t, dsn)
	for _, code := range []string{"28000", "28P01"} {
		t.Run(code, func(t *testing.T) {
			dir := shortDir(t)
			fakePG(t, dir, port, code)
			db, err := New(context.Background(), socketTestConfig(dsn, "", []string{dir}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			if !viaTCP(t, db.Pool()) {
				t.Error("expected TCP after the socket refused")
			}
			notes := db.TransportNotes()
			if len(notes) != 1 || !strings.Contains(notes[0], "refused (auth); using tcp 127.0.0.1:") {
				t.Fatalf("notes = %q", notes)
			}
		})
	}
}

func TestUnixSocket_OptOut(t *testing.T) {
	dsn, sockdir := socketEnv(t)
	cfg := socketTestConfig(dsn, "", []string{sockdir})
	cfg.UnixSocketOff = true
	db, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if !viaTCP(t, db.Pool()) {
		t.Error("expected TCP with the socket preference off")
	}
}
