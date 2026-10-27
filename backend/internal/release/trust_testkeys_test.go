//go:build hatamoto_testkeys

package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"hatamoto.dev/core/internal/packages"
)

const helperEnv = "HATAMOTO_TESTKEYS_HELPER"

// The helper runs in a subprocess because the trust set is read in init.
// It exits 0 when the record verifies, 3 when it is rejected.
func TestTestkeysHelper(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("subprocess only")
	}
	rec, err := os.ReadFile(os.Getenv(helperEnv + "_REC"))
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := os.ReadFile(os.Getenv(helperEnv + "_SIG"))
	if _, err := VerifyRecord(rec, string(sig), Options{}); err != nil {
		println("rejected:", err.Error())
		os.Exit(3)
	}
}

func TestTrustDirReplacesEmbeddedKeys(t *testing.T) {
	good, _ := genKey(t)
	other, _ := genKey(t)
	pub, _ := good.PublicKey()
	dir := t.TempDir()
	pubFile, _ := json.Marshal(packages.Key{ID: packages.KeyID(pub), Public: good.Public})
	if err := os.WriteFile(filepath.Join(dir, "release-test.pub"), pubFile, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	record, err := MarshalRecord(Record{Version: "v1.0.1", RollbackFloor: "v1.0.0", MinFrom: "v1.0.0", Artifacts: map[string]Artifact{"linux-amd64": {SHA256: sha([]byte("x")), Size: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		key  packages.Key
		want int
	}{"signed by the trusted throwaway key": {good, 0}, "signed by an unknown key": {other, 3}} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			recPath, sigPath := filepath.Join(work, "r.json"), filepath.Join(work, "r.sig")
			_ = os.WriteFile(recPath, record, 0o644)
			_ = os.WriteFile(sigPath, []byte(f.signRecord(t, tc.key, record)), 0o644)
			cmd := exec.Command(os.Args[0], "-test.run=^TestTestkeysHelper$")
			cmd.Env = append(os.Environ(), helperEnv+"=1", helperEnv+"_REC="+recPath, helperEnv+"_SIG="+sigPath, "HATAMOTO_TEST_TRUST_DIR="+dir)
			outb, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, outb)
			}
		})
	}
}
