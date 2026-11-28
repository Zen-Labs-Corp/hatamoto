package staticexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"hatamoto.dev/core/internal/runenv"
	"hatamoto.dev/core/internal/safefetch"
)

// EnvSFTPAllowPrivate=1 lets the SFTP target reach a host on a private
// network or loopback. It is separate from EnvS3AllowPrivate: allowing a LAN
// storage service never opens SFTP to the LAN.
const EnvSFTPAllowPrivate = "HATAMOTO_STATIC_SFTP_ALLOW_PRIVATE"

const (
	sftpUploadConcurrency = 4
	sftpDialTimeout       = 15 * time.Second
	sftpHandshakeTimeout  = 30 * time.Second
	sftpProbeTimeout      = 10 * time.Second
	sftpTmpSuffix         = ".hatamoto-tmp"
	sftpMaxHTAccess       = 4 << 20
)

// Errors a caller can match; every message is safe to show.
var (
	ErrSFTPHostKeyUntrusted = errors.New("the SFTP host key is not trusted: use Trust host key on Settings > Hosting")
	ErrSFTPHostKeyChanged   = errors.New("the SFTP host key changed")
	ErrSFTPKeyPassphrase    = errors.New("the private key is protected by a passphrase: use a key without one")
	ErrPrivateSFTPHost      = errors.New("SFTP host refused: private, loopback or link-local address")
)

// errProbed stops the handshake once the probe has seen the host key.
var errProbed = errors.New("host key recorded")

// AllowPrivateSFTP reports whether the operator opted in to a private host.
func AllowPrivateSFTP(getenv func(string) string) bool {
	return getenv != nil && strings.TrimSpace(getenv(EnvSFTPAllowPrivate)) == "1"
}

// CheckSFTPHost refuses a host that is an IP literal safefetch blocks, or
// localhost. It does no DNS: the dial-time guard resolves and checks names.
func CheckSFTPHost(host string) error {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return fmt.Errorf("%w (set %s=1 to allow)", ErrPrivateSFTPHost, EnvSFTPAllowPrivate)
	}
	if ip, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil && safefetch.BlockedAddr(ip) {
		return fmt.Errorf("%w (set %s=1 to allow)", ErrPrivateSFTPHost, EnvSFTPAllowPrivate)
	}
	return nil
}

// SFTPConfig is everything the SFTP target needs. Secret never appears in an
// error, a key or a description.
type SFTPConfig struct {
	Host     string
	Port     int
	User     string
	Auth     string // SFTPAuthPassword or SFTPAuthKey
	Secret   string // the password, or an OpenSSH private key without passphrase
	Dir      string // relative to the login home
	HostKey  string // trusted "SHA256:..." fingerprint; "" refuses every connection
	HTAccess bool

	AllowPrivate bool
	Lookup       safefetch.Lookup // nil: the system resolver
}

// SFTPKey is the run record key of an SFTP target. It never holds a secret.
func SFTPKey(user, host string, port int, dir string) string {
	return "sftp:" + user + "@" + net.JoinHostPort(host, strconv.Itoa(port)) + "/" + dir
}

// SFTPTarget publishes to a folder on an SFTP server. It dials once per phase
// (Previous, Publish): a Build can take minutes and no connection idles across
// it. It deletes only files the previous manifest lists and never lists the
// remote folder.
type SFTPTarget struct {
	cfg      SFTPConfig
	dir      string
	key      string
	describe string
	signer   ssh.Signer
	puts     atomic.Int64
	dels     atomic.Int64

	noPosixRename bool // tests: force the remove-then-rename fallback
}

// NewSFTPTarget checks cfg and returns the target. It does not connect.
func NewSFTPTarget(cfg SFTPConfig) (*SFTPTarget, error) {
	dir, err := ValidateRemoteDir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		return nil, errors.New("SFTP host is required")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("SFTP port must be between 1 and 65535")
	}
	t := &SFTPTarget{cfg: cfg, dir: dir}
	switch cfg.Auth {
	case SFTPAuthPassword:
	case SFTPAuthKey:
		if t.signer, err = ParseSFTPKey(cfg.Secret); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("SFTP sign-in must be password or key")
	}
	t.key = SFTPKey(cfg.User, cfg.Host, cfg.Port, dir)
	t.describe = "sftp://" + cfg.User + "@" + net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)) + "/" + dir
	return t, nil
}

// ParseSFTPKey reads an OpenSSH or PEM private key. A key protected by a
// passphrase is refused with ErrSFTPKeyPassphrase.
func ParseSFTPKey(secret string) (ssh.Signer, error) {
	s, err := ssh.ParsePrivateKey([]byte(secret))
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, ErrSFTPKeyPassphrase
	}
	if err != nil {
		return nil, errors.New("the private key cannot be read: paste an OpenSSH private key")
	}
	return s, nil
}

func (t *SFTPTarget) Key() string      { return t.key }
func (t *SFTPTarget) Describe() string { return t.describe }

// WritesHTAccess tells Export to stage the .htaccess block.
func (t *SFTPTarget) WritesHTAccess() bool { return t.cfg.HTAccess }

// Modified reports whether the last Publish uploaded or deleted at least one
// file. A publish is not atomic, so after an error the folder may be part way
// to the new export.
func (t *SFTPTarget) Modified() bool { return t.puts.Load() > 0 || t.dels.Load() > 0 }

// Deleted reports whether the last Publish deleted at least one file.
func (t *SFTPTarget) Deleted() bool { return t.dels.Load() > 0 }

// Stage returns a fresh temporary directory.
func (t *SFTPTarget) Stage(context.Context) (string, error) {
	return os.MkdirTemp("", "hatamoto-static-*")
}

// Discard removes the staging directory.
func (t *SFTPTarget) Discard(staged string) { _ = os.RemoveAll(staged) }

func dialSFTP(ctx context.Context, host string, port int, allowPrivate bool, lookup safefetch.Lookup) (net.Conn, error) {
	d := &safefetch.Dialer{AllowPrivate: allowPrivate, Lookup: lookup, Net: net.Dialer{Timeout: sftpDialTimeout}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if errors.Is(err, safefetch.ErrBlocked) {
			return nil, fmt.Errorf("%w (set %s=1 to allow): %w", ErrPrivateSFTPHost, EnvSFTPAllowPrivate, err)
		}
		return nil, fmt.Errorf("cannot reach the SFTP host: %w", err)
	}
	return conn, nil
}

// ProbeSFTPHostKey connects and returns the SHA256 fingerprint of the key the
// server presents. The key exchange happens before sign-in, so no credential
// is sent and nothing is stored.
func ProbeSFTPHostKey(ctx context.Context, host string, port int, allowPrivate bool, lookup safefetch.Lookup) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sftpProbeTimeout)
	defer cancel()
	conn, err := dialSFTP(ctx, host, port, allowPrivate, lookup)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	var fp string
	_, _, _, err = ssh.NewClientConn(conn, net.JoinHostPort(host, strconv.Itoa(port)), &ssh.ClientConfig{
		User: "hatamoto-probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp = ssh.FingerprintSHA256(key)
			return errProbed
		},
		Timeout: sftpProbeTimeout,
	})
	if fp == "" {
		if err == nil {
			err = errors.New("no host key presented")
		}
		return "", fmt.Errorf("cannot read the SFTP host key: %w", err)
	}
	return fp, nil
}

// connect dials, checks the host key against the pinned fingerprint, signs in
// and opens the SFTP subsystem. The returned func closes everything.
func (t *SFTPTarget) connect(ctx context.Context) (*sftp.Client, func(), error) {
	if t.cfg.HostKey == "" {
		return nil, nil, ErrSFTPHostKeyUntrusted
	}
	conn, err := dialSFTP(ctx, t.cfg.Host, t.cfg.Port, t.cfg.AllowPrivate, t.cfg.Lookup)
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	closeAll := func() { stop(); conn.Close() }

	var auth []ssh.AuthMethod
	if t.signer != nil {
		auth = []ssh.AuthMethod{ssh.PublicKeys(t.signer)}
	} else {
		pw := t.cfg.Secret
		auth = []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				answers := make([]string, len(qs))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}
	}
	_ = conn.SetDeadline(time.Now().Add(sftpHandshakeTimeout))
	addr := net.JoinHostPort(t.cfg.Host, strconv.Itoa(t.cfg.Port))
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: t.cfg.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if fp := ssh.FingerprintSHA256(key); fp != t.cfg.HostKey {
				return fmt.Errorf("the SFTP host key of %s changed (it is now %s): check with your host, then trust it again on Settings > Hosting: %w",
					t.cfg.Host, fp, ErrSFTPHostKeyChanged)
			}
			return nil
		},
		Timeout: sftpHandshakeTimeout,
	})
	if err != nil {
		closeAll()
		switch {
		case errors.Is(err, ErrSFTPHostKeyChanged):
			return nil, nil, err
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, nil, errors.New("the SFTP host refused the user or the password or key")
		}
		return nil, nil, fmt.Errorf("cannot sign in to the SFTP host: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	client, err := sftp.NewClient(ssh.NewClient(cc, chans, reqs))
	if err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("the SFTP host did not start the file service: %w", err)
	}
	return client, func() { _ = client.Close(); closeAll() }, nil
}

// Previous reads the manifest in the remote folder. A missing file means a
// first export.
func (t *SFTPTarget) Previous(ctx context.Context) (Manifest, bool, error) {
	c, closeConn, err := t.connect(ctx)
	if err != nil {
		return Manifest{}, false, err
	}
	defer closeConn()
	f, err := c.Open(path.Join(t.dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("read previous manifest: %w", err)
	}
	defer f.Close()
	m, err := ReadManifest(f)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Publish uploads the changed files (each through a temporary name and a
// rename), then deletes the removed ones, then merges the .htaccess block, then
// writes the manifest. An upload error returns before any delete.
func (t *SFTPTarget) Publish(ctx context.Context, staged string, d Diff, next Manifest) error {
	if runenv.Suppress(ctx, nil, runenv.StaticExportSFTP, "target", t.describe) {
		return runenv.ErrSuppressed
	}
	t.puts.Store(0)
	t.dels.Store(0)
	c, closeConn, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer closeConn()
	if err := c.MkdirAll(t.dir); err != nil {
		return fmt.Errorf("create remote folder: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, sftpUploadConcurrency)
	for _, p := range d.Changed {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := t.upload(c, staged, p); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, p := range d.Removed {
		if err := c.Remove(path.Join(t.dir, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", p, err)
		}
		t.dels.Add(1)
	}

	if t.cfg.HTAccess {
		if err := t.mergeHTAccess(c, staged); err != nil {
			return err
		}
	}

	body, err := next.Encode()
	if err != nil {
		return err
	}
	if err := t.writeRemote(c, path.Join(t.dir, ManifestFile), bytes.NewReader(body)); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func (t *SFTPTarget) upload(c *sftp.Client, staged, p string) error {
	f, err := os.Open(filepath.Join(staged, filepath.FromSlash(p)))
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return err
	} else if st.IsDir() {
		return errors.New("staged path is a directory: " + p)
	}
	dst := path.Join(t.dir, p)
	if err := c.MkdirAll(path.Dir(dst)); err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	if err := t.writeRemote(c, dst, f); err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	t.puts.Add(1)
	return nil
}

// writeRemote writes r to <dir>/.<base>.hatamoto-tmp and renames it over dst,
// so a reader never sees half a file. The temporary file is removed on failure.
func (t *SFTPTarget) writeRemote(c *sftp.Client, dst string, r io.Reader) (err error) {
	tmp := path.Join(path.Dir(dst), "."+path.Base(dst)+sftpTmpSuffix)
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = c.Remove(tmp)
		}
	}()
	if _, err = io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	_ = c.Chmod(tmp, 0o644) // a server without chmod keeps its default
	return t.rename(c, tmp, dst)
}

// rename moves tmp over dst: atomically with posix-rename@openssh.com, else
// through a remove, which leaves a short window with no file.
func (t *SFTPTarget) rename(c *sftp.Client, tmp, dst string) error {
	if _, ok := c.HasExtension("posix-rename@openssh.com"); ok && !t.noPosixRename {
		return c.PosixRename(tmp, dst)
	}
	if err := c.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return c.Rename(tmp, dst)
}

// mergeHTAccess keeps the Hatamoto block of the remote .htaccess current and
// leaves every other line as it was.
func (t *SFTPTarget) mergeHTAccess(c *sftp.Client, staged string) error {
	block, err := os.ReadFile(filepath.Join(staged, HTAccessFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dst := path.Join(t.dir, HTAccessFile)
	var remote []byte
	existed := false
	f, err := c.Open(dst)
	switch {
	case err == nil:
		existed = true
		remote, err = io.ReadAll(io.LimitReader(f, sftpMaxHTAccess+1))
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("read remote %s: %w", HTAccessFile, err)
		}
		if len(remote) > sftpMaxHTAccess {
			return fmt.Errorf("remote %s is too large to merge", HTAccessFile)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("read remote %s: %w", HTAccessFile, err)
	}
	merged, err := MergeHTAccess(remote, block)
	if err != nil {
		return err
	}
	switch {
	case bytes.Equal(merged, remote):
		return nil
	case len(merged) == 0 && existed:
		if err := c.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", HTAccessFile, err)
		}
		return nil
	}
	if err := t.writeRemote(c, dst, bytes.NewReader(merged)); err != nil {
		return fmt.Errorf("write %s: %w", HTAccessFile, err)
	}
	return nil
}
