package backup

import (
	"errors"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/portability"
)

func createdManifest(t *testing.T, r *rig) manifest {
	t.Helper()
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	_, body := readTar(t, filepath.Join(r.data, "backups", info.ID+".tar"))
	return readManifest(t, body)
}

func TestCreateRecordsSecretCheck(t *testing.T) {
	r := newRig(t)
	r.e.opts.Secret = func() (string, error) { return "s3cret", nil }
	mf := createdManifest(t, r)
	if mf.SecretCheck == "" || mf.SecretCheck != portability.SecretCheck("s3cret") {
		t.Fatalf("secret_check = %q", mf.SecretCheck)
	}
	if mf.Version != manifestVersion {
		t.Fatalf("version = %d", mf.Version)
	}
}

func TestCreateWithoutSecretRecordsNoCheck(t *testing.T) {
	r := newRig(t)
	if mf := createdManifest(t, r); mf.SecretCheck != "" {
		t.Fatalf("secret_check = %q", mf.SecretCheck)
	}
}

func TestCreateSurvivesSecretError(t *testing.T) {
	r := newRig(t)
	r.e.opts.Secret = func() (string, error) { return "", errors.New("unreadable") }
	if mf := createdManifest(t, r); mf.SecretCheck != "" || mf.Version != manifestVersion {
		t.Fatalf("manifest = %+v", mf)
	}
}
