package app

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Names of the server secret. HATAMOTO_SECRET is preferred; JWT_SECRET is the
// name older deployments set and is read everywhere HATAMOTO_SECRET is.
const (
	secretEnv       = "HATAMOTO_SECRET"
	legacySecretEnv = "JWT_SECRET"
)

// ensureSecrets makes sure the server secret is set in the process environment
// (as HATAMOTO_SECRET unless the environment already provides one under either
// name). A value in the environment always wins and is never written to disk;
// otherwise it comes from <dir>/secrets.env (HATAMOTO_SECRET, or the JWT_SECRET
// an older release stored there), and is generated and saved there (mode 0600)
// when missing. It keys the flash cookie, theme-preview links and stored
// credentials, so it must survive restarts.
func ensureSecrets(dir string, logger *slog.Logger) error {
	if os.Getenv(secretEnv) != "" || os.Getenv(legacySecretEnv) != "" {
		return nil
	}
	path := filepath.Join(dir, "secrets.env")
	stored, err := readSecretsFile(path)
	if err != nil {
		return err
	}
	v := stored[secretEnv]
	if v == "" {
		v = stored[legacySecretEnv]
	}
	if v == "" {
		if v, err = randomSecret(); err != nil {
			return err
		}
		logger.Info("generated secret on first boot", "key", secretEnv, "file", path)
		stored[secretEnv] = v
		if err := writeSecretsFile(path, stored); err != nil {
			return fmt.Errorf("cannot persist generated secret (%w): set HATAMOTO_SECRET in the environment or make HATAMOTO_SECRETS_DIR writable", err)
		}
	}
	return os.Setenv(secretEnv, v)
}

func randomSecret() (string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func readSecretsFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path) // #nosec G304 -- operator-configured secrets dir
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && !strings.HasPrefix(k, "#") {
			out[k] = v
		}
	}
	return out, sc.Err()
}

// writeSecretsFile replaces path atomically with a 0600 file.
func writeSecretsFile(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, k := range []string{secretEnv, legacySecretEnv} {
		if m[k] != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, m[k])
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secrets-*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
