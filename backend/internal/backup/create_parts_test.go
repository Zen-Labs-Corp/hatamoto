package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readTar returns the entries of a backup tar in order, with their bodies.
func readTar(t *testing.T, path string) (names []string, body map[string][]byte) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body = map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names, body
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		body[h.Name] = b
	}
}

func readManifest(t *testing.T, body map[string][]byte) manifest {
	t.Helper()
	var mf manifest
	if err := json.Unmarshal(body[manifestName], &mf); err != nil {
		t.Fatal(err)
	}
	return mf
}

func TestCreateStoresSecretsConfigAndTrust(t *testing.T) {
	r := newRig(t, "webhooks")
	secrets := t.TempDir()
	const secretsBody = "first line\nsecond line\n"
	if err := os.WriteFile(filepath.Join(secrets, "secrets.env"), []byte(secretsBody), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "hatamoto.env")
	const cfgBody = "DATABASE_URL=postgres://x\n# kept verbatim\n"
	if err := os.WriteFile(cfg, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}
	r.e.opts.SecretsDir = secrets
	r.e.opts.ConfigPath = filepath.Join(t.TempDir(), "ignored.env") // the request wins
	want := TrustIDs{Release: []string{"a", "b"}, Manifest: []string{"m"}, Env: []string{"e"}}
	r.e.opts.Trust = func() (TrustIDs, error) { return want, nil }

	info, err := r.e.Create(t.Context(), Request{Kind: KindManual, ConfigPath: cfg})
	if err != nil {
		t.Fatal(err)
	}
	names, body := readTar(t, filepath.Join(r.data, "backups", info.ID+".tar"))
	wantNames := []string{"core.dump", "plugin_webhooks.dump", "secrets.env", "config.env", "content.tar.gz", "manifest.json"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("entries = %v, want %v", names, wantNames)
	}
	if string(body["secrets.env"]) != secretsBody || string(body["config.env"]) != cfgBody {
		t.Fatalf("secrets %q config %q", body["secrets.env"], body["config.env"])
	}
	mf := readManifest(t, body)
	if mf.Trust == nil || !reflect.DeepEqual(*mf.Trust, want) {
		t.Fatalf("trust = %+v", mf.Trust)
	}
	if mf.ConfigPath != cfg || mf.Version != manifestVersion {
		t.Fatalf("config_path %q version %d", mf.ConfigPath, mf.Version)
	}
	for _, p := range mf.Parts {
		sum := sha256.Sum256(body[p.Name])
		if p.SHA256 != hex.EncodeToString(sum[:]) || p.Size != int64(len(body[p.Name])) {
			t.Errorf("part %s does not match its entry", p.Name)
		}
	}
	if len(mf.Parts) != len(wantNames)-1 {
		t.Errorf("parts = %d", len(mf.Parts))
	}
	// The work dir (0600 parts) is gone and the source files are untouched.
	if left, _ := filepath.Glob(filepath.Join(r.data, "backups", ".tmp-*")); len(left) != 0 {
		t.Errorf("temp left: %v", left)
	}
	if fi, _ := os.Stat(filepath.Join(secrets, "secrets.env")); fi.Mode().Perm() != 0o640 {
		t.Errorf("source secrets mode changed to %v", fi.Mode().Perm())
	}
}

func TestCreateConfigFromOptions(t *testing.T) {
	r := newRig(t)
	cfg := filepath.Join(t.TempDir(), "hatamoto.env")
	if err := os.WriteFile(cfg, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.e.opts.ConfigPath = cfg
	info, err := r.e.Create(t.Context(), Request{Kind: KindPreUpdate, Target: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	_, body := readTar(t, filepath.Join(r.data, "backups", info.ID+".tar"))
	if string(body["config.env"]) != "A=1\n" {
		t.Fatalf("config = %q", body["config.env"])
	}
}

func TestCreateMissingConfigFileFails(t *testing.T) {
	r := newRig(t)
	_, err := r.e.Create(t.Context(), Request{Kind: KindManual, ConfigPath: filepath.Join(t.TempDir(), "nope.env")})
	if err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("err = %v", err)
	}
}

// Without a secrets file or a config path the part list is what it was before
// these parts existed; the default trust still lands in the manifest.
func TestCreateWithoutOwnerFilesKeepsOldParts(t *testing.T) {
	r := newRig(t, "webhooks")
	r.e.opts.SecretsDir = t.TempDir() // empty: secret only in the environment
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	names, body := readTar(t, filepath.Join(r.data, "backups", info.ID+".tar"))
	want := []string{"core.dump", "plugin_webhooks.dump", "content.tar.gz", "manifest.json"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	mf := readManifest(t, body)
	emb, _ := embeddedTrust()
	if mf.Trust == nil || !reflect.DeepEqual(*mf.Trust, emb) || len(emb.Release) == 0 || len(emb.Manifest) == 0 {
		t.Fatalf("trust = %+v, embedded %+v", mf.Trust, emb)
	}
	if mf.ConfigPath != "" {
		t.Errorf("config_path = %q", mf.ConfigPath)
	}
}

// A relative --config path is recorded absolute: a restore elsewhere shows the
// recorded path as a hint and must not depend on the creating process's cwd.
func TestCreateRecordsConfigPathAbsolute(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("rel.env", []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := r.e.Create(t.Context(), Request{Kind: KindManual, ConfigPath: "rel.env"})
	if err != nil {
		t.Fatal(err)
	}
	_, body := readTar(t, filepath.Join(r.data, "backups", info.ID+".tar"))
	// t.Chdir may resolve symlinks (macOS); compare against the working dir.
	wd, _ := os.Getwd()
	if got := readManifest(t, body).ConfigPath; got != filepath.Join(wd, "rel.env") {
		t.Fatalf("config_path = %q", got)
	}
}
