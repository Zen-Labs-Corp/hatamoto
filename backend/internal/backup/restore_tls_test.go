package backup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

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

// verifyFullQuery is the query of a managed PostgreSQL URL.
func verifyFullQuery(t *testing.T) (query, ca string) {
	t.Helper()
	ca = writeTestCA(t)
	return "sslmode=verify-full&sslrootcert=" + url.QueryEscape(ca), ca
}

func wantTLSEnv(t *testing.T, what string, env []string, ca string) {
	t.Helper()
	for _, w := range []string{"PGSSLMODE=verify-full", "PGSSLROOTCERT=" + ca} {
		if !slices.Contains(env, w) {
			t.Errorf("%s: env lacks %s: %v", what, w, env)
		}
	}
}

// The tools (pg_dump, pg_restore, psql) read the TLS settings from the
// environment, for DATABASE_URL and for --admin-url.
func TestPgEnvCarriesVerifyFull(t *testing.T) {
	q, ca := verifyFullQuery(t)
	for _, user := range []string{"postgres", "hm_admin"} {
		for _, name := range []string{"DATABASE_URL", "--admin-url"} {
			raw := "postgres://" + user + ":p@db.example.com/x?" + q
			env, err := pgEnv(name, raw, true)
			if err != nil {
				t.Fatalf("%s as %s: %v", name, user, err)
			}
			wantTLSEnv(t, name+" as "+user, env, ca)
			if !slices.Contains(env, "PGUSER="+user) || !slices.Contains(env, "PGHOST=db.example.com") {
				t.Errorf("%s as %s: env = %v", name, user, env)
			}
		}
	}
}

// The admin URL the restore connects with is the --admin-url pointed at the
// target database; its TLS settings survive the rewrite, and so does what the
// tools get from it.
func TestRestoreAdminURLKeepsVerifyFull(t *testing.T) {
	q, ca := verifyFullQuery(t)
	for _, user := range []string{"postgres", "hm_admin"} {
		t.Run(user, func(t *testing.T) {
			r := newRestoreRig(t)
			r.o.DatabaseURL = "postgres://hm_app:x@db.example.com:1/site?" + q
			admin := "postgres://" + user + ":pw@db.example.com/postgres?dbname=other&" + q
			// The app connection fails; only the admin URL matters.
			_, _ = Restore(t.Context(), r.o, RestoreRequest{Source: buildArchive(t, nil), AdminURL: admin, DryRun: true})
			u, err := url.Parse(r.adminURL)
			if err != nil {
				t.Fatalf("admin URL %q: %v", r.adminURL, err)
			}
			if u.Path != "/site" || u.Query().Has("dbname") || u.User.Username() != user {
				t.Errorf("admin URL = %q", r.adminURL)
			}
			if u.Query().Get("sslmode") != "verify-full" || u.Query().Get("sslrootcert") != ca {
				t.Errorf("admin URL lost its TLS settings: %q", r.adminURL)
			}
			env, err := pgEnv("--admin-url", r.adminURL, true)
			if err != nil {
				t.Fatal(err)
			}
			wantTLSEnv(t, "rewritten admin URL", env, ca)
			if !slices.Contains(env, "PGDATABASE=site") {
				t.Errorf("env = %v", env)
			}
		})
	}
}
