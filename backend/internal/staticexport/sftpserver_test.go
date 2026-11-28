package staticexport

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	testSFTPUser     = "site"
	testSFTPPassword = "secret-pw-4711"
)

// sftpTestServer is an in-process SSH server with the sftp subsystem over a
// temp dir. It accepts testSFTPUser with testSFTPPassword or ClientKeyPEM.
type sftpTestServer struct {
	Host         string
	Port         int
	Root         string
	Fingerprint  string
	ClientKeyPEM string
	conns        atomic.Int64
}

func (s *sftpTestServer) Conns() int { return int(s.conns.Load()) }

func newTestSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return signer, pem.EncodeToMemory(blk)
}

func newSFTPServer(t *testing.T) *sftpTestServer {
	t.Helper()
	hostSigner, _ := newTestSigner(t)
	clientSigner, clientPEM := newTestSigner(t)
	srv := &sftpTestServer{Root: t.TempDir(), ClientKeyPEM: string(clientPEM),
		Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey())}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == testSFTPUser && string(pw) == testSFTPPassword {
				return nil, nil
			}
			return nil, os.ErrPermission
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == testSFTPUser && string(k.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, os.ErrPermission
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	addr := ln.Addr().(*net.TCPAddr)
	srv.Host, srv.Port = addr.IP.String(), addr.Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.conns.Add(1)
			go srv.serve(conn, cfg)
		}
	}()
	return srv
}

func (s *sftpTestServer) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range creqs {
				ok := r.Type == "subsystem" && len(r.Payload) >= 4 && string(r.Payload[4:]) == "sftp"
				_ = r.Reply(ok, nil)
				if ok {
					srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.Root))
					if err == nil {
						_ = srv.Serve()
						_ = srv.Close()
					}
					return
				}
			}
		}()
	}
}

// walkTmp returns every leftover temporary upload under root.
func leftoverTmp(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == sftpTmpSuffix {
			out = append(out, p)
		}
		return nil
	})
	return out
}
