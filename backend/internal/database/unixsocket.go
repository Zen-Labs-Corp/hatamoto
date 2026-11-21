package database

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// SocketOptions steers the unix-socket preference for loopback URLs. The zero
// value is enabled with DefaultSocketDirs.
type SocketOptions struct {
	Disabled bool
	Dirs     []string
}

// DefaultSocketDirs are the root-owned directories searched for the server's
// socket. /tmp is deliberately absent: any local user can create a socket there.
var DefaultSocketDirs = []string{"/var/run/postgresql", "/run/postgresql"}

// probeTimeout bounds the single connection attempt over the socket. A var so
// tests can shorten it.
var probeTimeout = 2 * time.Second

// socketOutcome records what preferUnixSocket did, for the boot log.
type socketOutcome struct {
	Socket string // socket file that was probed; "" when nothing was tried
	TCP    string // host:port of the TCP endpoint
	Reason string // why the probe was refused
	Used   bool
}

// note renders the boot log line, or "" when no socket was tried. prefix is
// "db" or "db direct". It never carries a URL or a driver error text.
func (o socketOutcome) note(prefix string) string {
	switch {
	case o.Socket == "":
		return ""
	case o.Used:
		return prefix + ": using unix socket " + o.Socket + " (loopback URL)"
	default:
		return prefix + ": unix socket " + o.Socket + " refused (" + o.Reason + "); using tcp " + o.TCP
	}
}

// preferUnixSocket switches cc from a loopback TCP endpoint to the server's
// unix socket for the same port when one exists and accepts the connection's
// credentials. It decides once: the socket is probed with a single attempt, and
// any failure leaves cc untouched. After a switch the original TCP endpoint
// stays in cc.Fallbacks so a later reconnect still reaches the server if the
// socket vanishes.
func preferUnixSocket(ctx context.Context, cc *pgconn.Config, o SocketOptions) socketOutcome {
	if o.Disabled || !socketEligible(cc) {
		return socketOutcome{}
	}
	dirs := o.Dirs
	if len(dirs) == 0 {
		dirs = DefaultSocketDirs
	}
	dir, path, ok := candidateSocket(dirs, cc.Port)
	if !ok {
		return socketOutcome{}
	}
	out := socketOutcome{Socket: path, TCP: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port)))}

	probe := cc.Copy()
	probe.Host = dir
	probe.TLSConfig = nil
	probe.Fallbacks = nil
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := pgconn.ConnectConfig(pctx, probe)
	if err != nil {
		out.Reason = probeReason(err)
		return out
	}
	_ = conn.Close(pctx)

	tcp := &pgconn.FallbackConfig{Host: cc.Host, Port: cc.Port, TLSConfig: cc.TLSConfig}
	cc.Fallbacks = append([]*pgconn.FallbackConfig{tcp}, cc.Fallbacks...)
	cc.Host = dir
	cc.TLSConfig = nil
	out.Used = true
	return out
}

// socketEligible reports whether cc is a single loopback TCP host that does not
// require TLS (a socket connection has no TLS to offer).
func socketEligible(cc *pgconn.Config) bool {
	for _, fb := range cc.Fallbacks {
		if fb.Host != cc.Host {
			return false
		}
	}
	h := cc.Host
	if h == "" || strings.HasPrefix(h, "/") || filepath.IsAbs(h) {
		return false
	}
	if h != "localhost" {
		if ip := net.ParseIP(h); ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	if cc.TLSConfig == nil {
		return true
	}
	// prefer and allow keep a plain-text entry for the host; require and
	// verify-* do not.
	for _, fb := range cc.Fallbacks {
		if fb.TLSConfig == nil {
			return true
		}
	}
	return false
}

// candidateSocket returns the first dir holding a socket file for port.
func candidateSocket(dirs []string, port uint16) (dir, path string, ok bool) {
	for _, d := range dirs {
		p := filepath.Join(d, ".s.PGSQL."+strconv.Itoa(int(port)))
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return d, p, true
		}
	}
	return "", "", false
}

// probeReason maps a probe failure to a fixed word, never err.Error(): the
// text could carry connection details.
func probeReason(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28000", "28P01":
			return "auth"
		case "3D000":
			return "database missing"
		}
		return "error"
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), pgconn.Timeout(err):
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	}
	return "error"
}
