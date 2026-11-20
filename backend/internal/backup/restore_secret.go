package backup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hatamoto.dev/core/internal/portability"
)

// effectiveSecret is the secret the restored server will run with:
// HATAMOTO_SECRET or JWT_SECRET in the environment (it wins over secrets.env,
// see app/secrets.go ensureSecrets), else the backup's secrets.env, else the
// secrets.env already in SecretsDir. "" when none has one.
func (x *restorer) effectiveSecret() (string, error) {
	getenv := x.o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, k := range []string{"HATAMOTO_SECRET", "JWT_SECRET"} {
		if v := getenv(k); v != "" {
			return v, nil
		}
	}
	if x.scan.hasSecrets {
		if v := secretFromEnvFile(x.scan.secrets); v != "" {
			return v, nil
		}
	}
	if x.o.SecretsDir == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(x.o.SecretsDir, secretsName)) // #nosec G304 -- operator supplied dir
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return secretFromEnvFile(b), nil
}

// secretFromEnvFile reads HATAMOTO_SECRET (else JWT_SECRET) from a secrets.env
// body, as app's readSecretsFile does.
func secretFromEnvFile(b []byte) string {
	var primary, legacy string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch k {
		case "HATAMOTO_SECRET":
			primary = v
		case "JWT_SECRET":
			legacy = v
		}
	}
	if primary != "" {
		return primary
	}
	return legacy
}

// checkSecret compares the manifest's check with the effective secret. It
// returns a refusal reason, "" when the restore may go on.
func (x *restorer) checkSecret(plan *RestorePlan) (refuse string, err error) {
	s, err := x.effectiveSecret()
	if err != nil {
		return "", fmt.Errorf("cannot read the existing secrets.env: %w", err)
	}
	switch portability.VerifySecret(x.mf.SecretCheck, s) {
	case portability.SecretMatch:
		plan.Checks = append(plan.Checks, "server secret ok")
	case portability.SecretNoCheck:
		plan.Warnings = append(plan.Warnings, "the backup has no server secret check (made by an older hatamoto); if the secret differs, commenter emails stay unreadable")
	case portability.SecretUnknown:
		plan.Warnings = append(plan.Warnings, "no server secret is set (HATAMOTO_SECRET or secrets.env); the backup's secret check was not verified")
	case portability.SecretMismatch:
		if !x.req.Force {
			return "the backup was made with a different server secret: commenter emails in it can't be decrypted and comment hashes won't match; restore with the original HATAMOTO_SECRET, or re-run with --force to restore and drop the unreadable commenter emails; a full backup restored under another secret also leaves the other sealed data unreadable (two-factor authenticator secrets and recovery codes, the SMTP password, the off-site secret key, the spam-protection secret), so reset 2FA with `hatamoto user reset-2fa --email E` and enter the others again in the admin", nil
		}
		x.dropEmails = true
		plan.Warnings = append(plan.Warnings, "--force: the backup was made with a different server secret; its commenter emails are dropped")
	}
	return "", nil
}
