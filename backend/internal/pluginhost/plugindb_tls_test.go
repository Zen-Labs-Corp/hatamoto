package pluginhost

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

// A plugin pool connects to the same server with the same TLS settings as the
// core pool; only the user and password change.
func TestPluginConnConfigKeepsVerifyFullTLS(t *testing.T) {
	ca := writeTestCA(t)
	for _, user := range []string{"postgres", "hm_admin"} {
		t.Run(user, func(t *testing.T) {
			cfg, err := pgxpool.ParseConfig("postgres://" + user + ":core@db.example.com/x?sslmode=verify-full&sslrootcert=" + ca)
			if err != nil {
				t.Fatal(err)
			}
			cc := pluginConnConfig(cfg.ConnConfig, "capa", "hm_p_1234abcd_capa", "plugin-pw")
			if cc.User != "hm_p_1234abcd_capa" || cc.Password != "plugin-pw" {
				t.Errorf("user/password = %q/%q", cc.User, cc.Password)
			}
			tc := cc.TLSConfig
			if tc == nil || tc.ServerName != "db.example.com" || tc.InsecureSkipVerify || tc.RootCAs == nil {
				t.Fatalf("TLS config lost: %+v", tc)
			}
			if cc.Host != "db.example.com" || cc.Database != "x" || len(cc.Fallbacks) != 0 {
				t.Errorf("host %q, database %q, fallbacks %d", cc.Host, cc.Database, len(cc.Fallbacks))
			}
			if cfg.ConnConfig.User != user || cfg.ConnConfig.Password != "core" {
				t.Errorf("the core config was modified: %q/%q", cfg.ConnConfig.User, cfg.ConnConfig.Password)
			}
		})
	}
}
