package database

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/config"
)

// writeTestCA writes a throwaway CA certificate as PEM and returns its path.
func writeTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A verify-full URL, as a managed PostgreSQL gives it out, parses into a TLS
// config that checks the host name against the given CA. The user is the
// superuser-style or the role admin style one; it does not matter to TLS.
func TestVerifyFullURLParsesToTLSConfig(t *testing.T) {
	ca := writeTestCA(t)
	for _, user := range []string{"postgres", "hm_admin"} {
		t.Run(user, func(t *testing.T) {
			raw := "postgres://" + user + ":p@db.example.com/x?sslmode=verify-full&sslrootcert=" + ca
			cfg, err := pgxpool.ParseConfig(raw)
			if err != nil {
				t.Fatal(err)
			}
			tc := cfg.ConnConfig.TLSConfig
			if tc == nil {
				t.Fatal("no TLS config")
			}
			if tc.ServerName != "db.example.com" || tc.InsecureSkipVerify {
				t.Errorf("ServerName = %q, skip verify = %v", tc.ServerName, tc.InsecureSkipVerify)
			}
			pem, err := os.ReadFile(ca)
			if err != nil {
				t.Fatal(err)
			}
			want := x509.NewCertPool()
			if !want.AppendCertsFromPEM(pem) {
				t.Fatal("test CA is not valid PEM")
			}
			if tc.RootCAs == nil || !tc.RootCAs.Equal(want) {
				t.Errorf("RootCAs does not hold the given CA")
			}
			if len(cfg.ConnConfig.Fallbacks) != 0 {
				t.Errorf("verify-full must not fall back to plain text: %d fallbacks", len(cfg.ConnConfig.Fallbacks))
			}
			if cfg.MinConns != 0 {
				t.Errorf("MinConns = %d", cfg.MinConns)
			}
		})
	}
}

// New reads the same URL: a refused connection is a ping failure, not a
// parse failure, so the TLS settings were accepted and the pool built.
func TestNewAcceptsVerifyFullURL(t *testing.T) {
	ca := writeTestCA(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(ctx, config.DatabaseConfig{
		URL:            "postgres://hm_admin:p@db.example.com/x?sslmode=verify-full&sslrootcert=" + ca,
		MaxConnections: 4,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to ping database") {
		t.Fatalf("err = %v, want a ping failure", err)
	}
}
