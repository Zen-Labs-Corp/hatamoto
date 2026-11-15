// Package pgbouncertest starts a private PgBouncer in transaction-pooling
// mode in front of a test Postgres, so tests can prove code is safe behind a
// pooler. Tests skip when no pgbouncer binary is available.
package pgbouncertest

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// binary returns the pgbouncer executable, or skips the test when there is none.
func binary(t testing.TB) string {
	t.Helper()
	if bin := os.Getenv("PGBOUNCER_BIN"); bin != "" {
		return bin
	}
	p, err := exec.LookPath("pgbouncer")
	if err != nil {
		t.Skip("pgbouncer not found (set PGBOUNCER_BIN)")
	}
	return p
}

// Start launches PgBouncer for the server in directDSN and returns a DSN that
// reaches the same database through it. The pooler uses pool_mode=transaction
// and max_prepared_statements=100, with poolSize server connections per
// database and user. A wildcard [databases] entry lets tests create their own
// databases (through directDSN) and reach them by name. The process is killed
// when the test ends. Start skips the test when no binary is found.
func Start(t testing.TB, directDSN string, poolSize int) (pooledDSN string) {
	t.Helper()
	bin := binary(t)
	u, err := url.Parse(directDSN)
	if err != nil || u.User == nil {
		t.Fatalf("pgbouncertest: need a postgres URL with credentials: %v", err)
	}
	pw, _ := u.User.Password()
	user := u.User.Username()

	dir := t.TempDir()
	userlist := filepath.Join(dir, "userlist.txt")
	writeFile(t, userlist, fmt.Sprintf("%q %q\n", user, pw))

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		port := freePort(t)
		ini := filepath.Join(dir, fmt.Sprintf("pgbouncer-%d.ini", attempt))
		logPath := filepath.Join(dir, fmt.Sprintf("pgbouncer-%d.log", attempt))
		writeFile(t, ini, fmt.Sprintf(`[databases]
* = host=%s port=%s

[pgbouncer]
listen_addr = 127.0.0.1
listen_port = %d
unix_socket_dir =
auth_type = scram-sha-256
auth_file = %s
pool_mode = transaction
max_prepared_statements = 100
default_pool_size = %d
max_client_conn = 200
ignore_startup_parameters = extra_float_digits,options
logfile = %s
pidfile = %s
admin_users = %s
`, u.Hostname(), u.Port(), port, userlist, poolSize, logPath,
			filepath.Join(dir, fmt.Sprintf("pgbouncer-%d.pid", attempt)), user))

		cmd := exec.Command(bin, ini)
		if err := cmd.Start(); err != nil {
			t.Fatalf("pgbouncertest: start %s: %v", bin, err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		stop := func() {
			_ = cmd.Process.Kill()
			<-exited
		}

		pooled := *u
		pooled.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if lastErr = waitReady(pooled.String(), exited); lastErr == nil {
			t.Cleanup(stop)
			return pooled.String()
		}
		stop()
		lastErr = fmt.Errorf("%w\npgbouncer log:\n%s", lastErr, tail(logPath))
	}
	t.Fatalf("pgbouncertest: pgbouncer did not become ready: %v", lastErr)
	return ""
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitReady polls select 1 through the pooler for up to 5s.
func waitReady(dsn string, exited <-chan struct{}) error {
	deadline := time.Now().Add(5 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return fmt.Errorf("pgbouncer exited early")
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var c *pgx.Conn
		if c, err = pgx.Connect(ctx, dsn); err == nil {
			_, err = c.Exec(ctx, "select 1")
			_ = c.Close(ctx)
		}
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

func tail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}
