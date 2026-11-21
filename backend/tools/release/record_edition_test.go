package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/release"
)

// writeEditionFiles writes one fake file per platform for edition ed.
func writeEditionFiles(t *testing.T, dir, ed string, platforms []string) {
	t.Helper()
	for i, p := range platforms {
		body := bytes.Repeat([]byte{byte('m' + i)}, 200+i)
		if err := os.WriteFile(filepath.Join(dir, release.ArtifactName(ed, p)), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func recordArgs(facts, dir, out string) []string {
	return []string{"record", "--version", "v1.2.3", "--kind", "patch", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "https://example.test/n",
		"--dir", dir, "--out", out}
}

func TestRecordAddsEditionFiles(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	ed := release.Editions[0]
	writeEditionFiles(t, dir, ed, release.Platforms)
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, recordArgs(facts, dir, out)...)
	rec, err := readRecord(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Editions[ed]) != len(release.Platforms) {
		t.Fatalf("editions: %+v", rec.Editions)
	}
	for _, p := range release.Platforms {
		want, err := hashFile(filepath.Join(dir, release.ArtifactName(ed, p)))
		if err != nil || rec.Editions[ed][p] != want {
			t.Fatalf("%s: got %+v, want %+v (%v)", p, rec.Editions[ed][p], want, err)
		}
	}
	got := runOK(t, env{}, "record", "--check", out, "--dir", dir)
	for _, p := range release.Platforms {
		if !strings.Contains(got, ed+"/"+p+" ") {
			t.Fatalf("check does not print %s/%s:\n%s", ed, p, got)
		}
	}
}

func TestRecordWithoutEditionFilesHasNoMember(t *testing.T) {
	dir := t.TempDir()
	out := buildRecordFile(t, dir)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"editions"`)) {
		t.Fatalf("record has an editions member:\n%s", b)
	}
}

func TestRecordRefusesPartialEditionSet(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	writeEditionFiles(t, dir, release.Editions[0], release.Platforms[:3])
	err := runErr(t, env{}, recordArgs(facts, dir, filepath.Join(dir, "release.json"))...)
	if !strings.Contains(err.Error(), "all or none") {
		t.Fatalf("error: %v", err)
	}
}

func TestRecordCheckRejectsUnknownEditionAndMismatch(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	ed := release.Editions[0]
	writeEditionFiles(t, dir, ed, release.Platforms)
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, recordArgs(facts, dir, out)...)

	// An edition outside the whitelist is a problem, and is never joined into a path.
	b, _ := os.ReadFile(out)
	other := filepath.Join(dir, "other.json")
	os.WriteFile(other, bytes.ReplaceAll(b, []byte(`"`+ed+`"`), []byte(`"other"`)), 0o644)
	err := runErr(t, env{}, "record", "--check", other, "--dir", dir)
	if !strings.Contains(err.Error(), "problem") {
		t.Fatalf("error: %v", err)
	}
	var outb, errb bytes.Buffer
	run([]string{"record", "--check", other, "--dir", dir}, &outb, &errb, env{})
	if !strings.Contains(outb.String(), `unknown edition "other"`) {
		t.Fatalf("output:\n%s", outb.String())
	}

	// A tampered edition file no longer matches.
	name := filepath.Join(dir, release.ArtifactName(ed, release.Platforms[1]))
	f, _ := os.ReadFile(name)
	f[0] ^= 1
	os.WriteFile(name, f, 0o755)
	outb.Reset()
	if run([]string{"record", "--check", out, "--dir", dir}, &outb, &errb, env{}) == nil {
		t.Fatal("tampered file accepted")
	}
	if !strings.Contains(outb.String(), "does not match the record") {
		t.Fatalf("output:\n%s", outb.String())
	}
}

func TestRecordAmendKeepsEditions(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	ed := release.Editions[0]
	writeEditionFiles(t, dir, ed, release.Platforms)
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, recordArgs(facts, dir, out)...)
	before, _ := readRecord(out)
	runOK(t, env{}, "record", "--amend", out, "--security")
	after, err := readRecord(out)
	if err != nil || !after.Security || len(after.Editions[ed]) != len(release.Platforms) {
		t.Fatalf("amend lost the member: %+v, %v", after, err)
	}
	for _, p := range release.Platforms {
		if after.Editions[ed][p] != before.Editions[ed][p] {
			t.Fatalf("%s changed by amend", p)
		}
	}
}

func TestRecordRefusesFactsFromAnotherEdition(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	out := filepath.Join(dir, "release.json")
	write := func(ed string) {
		js := `{"version":"v1.2.3","commit":"abc123","sdk":"0.10.0","schema_head":202610201100,"edition":"` + ed + `"}`
		os.WriteFile(facts, []byte(js), 0o644)
	}
	write("x")
	runErr(t, env{}, recordArgs(facts, dir, out)...)
	write("community")
	runOK(t, env{}, recordArgs(facts, dir, out)...)
}

// Edition files that are symlinks are read like community files; only a
// missing file is absent.
func TestRecordFollowsEditionSymlinks(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	ed := release.Editions[0]
	src := t.TempDir()
	writeEditionFiles(t, src, ed, release.Platforms)
	for _, p := range release.Platforms {
		name := release.ArtifactName(ed, p)
		if err := os.Symlink(filepath.Join(src, name), filepath.Join(dir, name)); err != nil {
			t.Skip("symlinks unavailable:", err)
		}
	}
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, recordArgs(facts, dir, out)...)
	rec, err := readRecord(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range release.Platforms {
		want, err := hashFile(filepath.Join(src, release.ArtifactName(ed, p)))
		if err != nil || rec.Editions[ed][p] != want {
			t.Fatalf("%s: got %+v, want %+v (%v)", p, rec.Editions[ed][p], want, err)
		}
	}
}

func TestRecordRefusesUnusableEditionEntry(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T, path string){
		"directory":           func(t *testing.T, path string) { os.Mkdir(path, 0o755) },
		"dangling link":       func(t *testing.T, path string) { os.Symlink(filepath.Join(filepath.Dir(path), "nowhere"), path) },
		"link to a directory": func(t *testing.T, path string) { os.Symlink(filepath.Dir(path), path) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			facts := writeDist(t, dir, "v1.2.3")
			ed := release.Editions[0]
			writeEditionFiles(t, dir, ed, release.Platforms)
			victim := filepath.Join(dir, release.ArtifactName(ed, release.Platforms[0]))
			os.Remove(victim)
			mk(t, victim)
			err := runErr(t, env{}, recordArgs(facts, dir, filepath.Join(dir, "release.json"))...)
			if strings.Contains(err.Error(), "all or none") {
				t.Fatalf("entry treated as absent: %v", err)
			}
		})
	}
}
