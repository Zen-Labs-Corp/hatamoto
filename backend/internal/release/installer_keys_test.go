package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const installDir = "../../../deploy/install"

func assembleInstaller(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available (the assembler needs it)")
	}
	out := t.TempDir()
	cmd := exec.Command("bash", filepath.Join(installDir, "build-install.sh"), "--out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-install.sh: %v\n%s", err, b)
	}
	return out
}

// keyBlock returns the "<id> <base64>" lines of one generated shell variable.
func keyBlock(t *testing.T, script, name string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + name + `='([^']*)'$`).FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("install.sh has no %s block", name)
	}
	keys := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(m[1]), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("%s: bad line %q", name, line)
		}
		keys[f[0]] = f[1]
	}
	return keys
}

// TestInstallerKeys ties the key block in the assembled install.sh to the
// embedded key files, and the unit payload to the tracked unit.
func TestInstallerKeys(t *testing.T) {
	out := assembleInstaller(t)
	b, err := os.ReadFile(filepath.Join(out, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)

	want := map[string]map[string]string{"TRUSTED_RELEASE_KEYS": {}, "TRUSTED_MANIFEST_KEYS": {}}
	files, err := filepath.Glob("keys/*.pub")
	if err != nil || len(files) == 0 {
		t.Fatalf("no key files: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var k struct {
			KeyID  string `json:"key_id"`
			Public string `json:"public"`
		}
		if err := json.Unmarshal(raw, &k); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		pub, err := base64.StdEncoding.DecodeString(k.Public)
		if err != nil || len(pub) != 32 {
			t.Fatalf("%s: bad public key", f)
		}
		sum := sha256.Sum256(pub)
		if id := hex.EncodeToString(sum[:])[:16]; id != k.KeyID {
			t.Errorf("%s: key_id %s, key hashes to %s", f, k.KeyID, id)
		}
		name := filepath.Base(f)
		switch {
		case strings.HasPrefix(name, "release-"):
			want["TRUSTED_RELEASE_KEYS"][k.KeyID] = k.Public
		case name == "manifest.pub" || strings.HasPrefix(name, "manifest-"):
			want["TRUSTED_MANIFEST_KEYS"][k.KeyID] = k.Public
		default:
			t.Fatalf("unexpected key file %s", name)
		}
	}
	for name, w := range want {
		got := keyBlock(t, script, name)
		var gi, wi []string
		for id := range got {
			gi = append(gi, id)
		}
		for id := range w {
			wi = append(wi, id)
		}
		sort.Strings(gi)
		sort.Strings(wi)
		if strings.Join(gi, ",") != strings.Join(wi, ",") {
			t.Errorf("%s: ids %v, want %v", name, gi, wi)
			continue
		}
		for id, pub := range w {
			if got[id] != pub {
				t.Errorf("%s: key %s differs from the key file", name, id)
			}
		}
	}
	// The key sets are disjoint: a manifest key must not verify records.
	rel, man := keyBlock(t, script, "TRUSTED_RELEASE_KEYS"), keyBlock(t, script, "TRUSTED_MANIFEST_KEYS")
	for id := range rel {
		if _, ok := man[id]; ok {
			t.Errorf("key %s is in both sets", id)
		}
	}
}

func TestInstallerDeterministicAndUnitVerbatim(t *testing.T) {
	a, b := assembleInstaller(t), assembleInstaller(t)
	for _, f := range []string{"install.sh", "firstboot.sh"} {
		x, _ := os.ReadFile(filepath.Join(a, f))
		y, _ := os.ReadFile(filepath.Join(b, f))
		if len(x) == 0 || !bytes.Equal(x, y) {
			t.Errorf("%s: two assemblies differ or are empty", f)
		}
	}
	script, _ := os.ReadFile(filepath.Join(a, "install.sh"))
	unit, err := os.ReadFile("../../../deploy/systemd/hatamoto-api.service")
	if err != nil {
		t.Fatal(err)
	}
	payload := "payload_unit() {\ncat <<'HM_PAYLOAD_UNIT'\n" + string(unit) + "HM_PAYLOAD_UNIT\n}\n"
	if !strings.Contains(string(script), payload) {
		t.Error("the unit payload in install.sh is not the tracked unit, byte for byte")
	}
	installUnit, err := os.ReadFile("../../../deploy/install-unit.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), string(installUnit)+"HM_PAYLOAD_INSTALL_UNIT\n}\n") {
		t.Error("the install-unit payload in install.sh is not the tracked script, byte for byte")
	}
}
