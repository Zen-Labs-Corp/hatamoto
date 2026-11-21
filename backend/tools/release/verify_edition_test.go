package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/release"
)

// editionRelease is a signed record in dir; withEdition adds the edition files
// before the record is built.
func editionRelease(t *testing.T, withEdition bool) signedRelease {
	t.Helper()
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	if withEdition {
		writeEditionFiles(t, dir, release.Editions[0], release.Platforms)
	}
	s := signedRelease{dir: dir, record: filepath.Join(dir, "release.json")}
	runOK(t, env{}, recordArgs(facts, dir, s.record)...)
	rel := newKeyPair(t, dir, "rel")
	s.env = env{Release: rel.set, Now: func() time.Time { return testNow }}
	runOK(t, s.env, "sign-record", s.record, "--key", rel.file)
	s.sig = s.record + ".sig"
	return s
}

func dirArgs(s signedRelease, extra ...string) []string {
	return append([]string{"verify", "--chan", "edge", "--record", s.record, "--sig", s.sig, "--dir", s.dir}, extra...)
}

func TestVerifyDirWithEditions(t *testing.T) {
	s := editionRelease(t, true)
	out := runOK(t, s.env, dirArgs(s, "--version", "v1.2.3")...)
	if !strings.Contains(out, "record ok: v1.2.3 (4 artifacts, 4 edition artifacts)") {
		t.Fatalf("output: %q", out)
	}
	var want []string
	for _, p := range release.Platforms {
		want = append(want, release.ArtifactName("", p))
	}
	for _, p := range release.Platforms {
		want = append(want, release.ArtifactName(release.Editions[0], p))
	}
	if got := runOK(t, s.env, dirArgs(s, "--list-files")...); got != strings.Join(want, "\n")+"\n" {
		t.Fatalf("list-files:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestVerifyDirListFilesDefaultOnly(t *testing.T) {
	s := editionRelease(t, false)
	got := runOK(t, s.env, dirArgs(s, "--list-files")...)
	lines := strings.Fields(got)
	if len(lines) != len(release.Platforms) || lines[0] != "hatamoto-"+release.Platforms[0] {
		t.Fatalf("list-files: %q", got)
	}
	if out := runOK(t, s.env, dirArgs(s)...); !strings.Contains(out, "record ok: v1.2.3 (4 artifacts)") {
		t.Fatalf("output: %q", out)
	}
}

func TestVerifyDirRefusesEditionFilesNotInRecord(t *testing.T) {
	s := editionRelease(t, false)
	writeEditionFiles(t, s.dir, release.Editions[0], release.Platforms[:1])
	err := runErr(t, s.env, dirArgs(s)...)
	if !strings.Contains(err.Error(), "the record names no") {
		t.Fatalf("error: %v", err)
	}
}

func TestVerifyDirRefusesMissingOrTamperedEditionFile(t *testing.T) {
	name := release.ArtifactName(release.Editions[0], release.Platforms[2])

	s := editionRelease(t, true)
	os.Remove(filepath.Join(s.dir, name))
	if err := runErr(t, s.env, dirArgs(s)...); !strings.Contains(err.Error(), name) {
		t.Fatalf("missing: %v", err)
	}

	s = editionRelease(t, true)
	b, _ := os.ReadFile(filepath.Join(s.dir, name))
	b[0] ^= 1
	os.WriteFile(filepath.Join(s.dir, name), b, 0o755)
	if err := runErr(t, s.env, dirArgs(s)...); !strings.Contains(err.Error(), name) {
		t.Fatalf("tampered: %v", err)
	}
}

func TestVerifyDirRefusesUnknownEdition(t *testing.T) {
	s := editionRelease(t, true)
	b, _ := os.ReadFile(s.record)
	b = []byte(strings.ReplaceAll(string(b), `"`+release.Editions[0]+`"`, `"other"`))
	os.WriteFile(s.record, b, 0o644)
	// sign-record refuses such a record, so sign the bytes directly.
	key := newKeyPair(t, s.dir, "rogue")
	sg, err := newFileSigner(key.file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sg.Sign(b)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.sig, []byte(encodeRecordSig(sg.PublicKey(), raw)+"\n"), 0o644)
	s.env.Release = key.set
	if err := runErr(t, s.env, dirArgs(s)...); !strings.Contains(err.Error(), `unknown edition "other"`) {
		t.Fatalf("error: %v", err)
	}
}
