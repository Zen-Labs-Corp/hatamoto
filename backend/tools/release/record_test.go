package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/release"
)

func TestRecordBuildsFromFacts(t *testing.T) {
	dir := t.TempDir()
	path := buildRecordFile(t, dir)
	b, _ := os.ReadFile(path)
	var rec release.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.2.3" || rec.SDK != "0.10.0" || rec.SchemaHead != 202610201100 || len(rec.Artifacts) != 4 {
		t.Fatalf("unexpected record %+v", rec)
	}
	for _, p := range release.Platforms {
		want, err := hashFile(filepath.Join(dir, "hatamoto-"+p))
		if err != nil || rec.Artifacts[p] != want {
			t.Errorf("%s: record %+v, file %+v (%v)", p, rec.Artifacts[p], want, err)
		}
	}
	again, _ := release.MarshalRecord(rec)
	if !bytes.Equal(b, again) {
		t.Error("record file is not in canonical form")
	}
	out := runOK(t, env{}, "record", "--check", path, "--dir", dir)
	if !strings.Contains(out, "looks complete") {
		t.Errorf("check output: %s", out)
	}

	// A missing platform file, a wrong facts version and an unknown commit all refuse.
	os.Remove(filepath.Join(dir, "hatamoto-darwin-arm64"))
	runErr(t, env{}, "record", "--version", "v1.2.3", "--kind", "patch", "--facts", filepath.Join(dir, "facts.json"),
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "u", "--dir", dir, "--out", filepath.Join(dir, "x.json"))
	dir2 := t.TempDir()
	facts := writeDist(t, dir2, "v1.2.4")
	runErr(t, env{}, "record", "--version", "v1.2.3", "--kind", "patch", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "u", "--dir", dir2)
	os.WriteFile(facts, []byte(`{"version":"v1.2.3","commit":"unknown","sdk":"0.10.0","schema_head":1}`), 0o644)
	runErr(t, env{}, "record", "--version", "v1.2.3", "--kind", "patch", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "u", "--dir", dir2)
}

func TestRecordCheck(t *testing.T) {
	dir := t.TempDir()
	good := release.Record{
		Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 5,
		RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/n",
		Artifacts: map[string]release.Artifact{},
	}
	for _, p := range release.Platforms {
		good.Artifacts[p] = release.Artifact{SHA256: strings.Repeat("a", 64), Size: 10}
	}
	clone := func(edit func(*release.Record)) release.Record {
		r := good
		r.Artifacts = map[string]release.Artifact{}
		for k, v := range good.Artifacts {
			r.Artifacts[k] = v
		}
		edit(&r)
		return r
	}
	cases := []struct {
		name    string
		rec     release.Record
		flags   []string
		wantErr string
	}{
		{"good", good, nil, ""},
		{"placeholder floor", clone(func(r *release.Record) { r.RollbackFloor = r.Version }), nil, "placeholder"},
		{"placeholder allowed", clone(func(r *release.Record) { r.RollbackFloor, r.MinFrom = r.Version, r.Version }), []string{"--allow-same"}, ""},
		{"floor newer", clone(func(r *release.Record) { r.RollbackFloor = "v1.3.0" }), nil, "newer than"},
		{"min_from newer", clone(func(r *release.Record) { r.MinFrom = "v2.0.0" }), nil, "newer than"},
		{"bad semver", clone(func(r *release.Record) { r.Version = "one" }), nil, "not a valid version"},
		{"missing platform", clone(func(r *release.Record) { delete(r.Artifacts, "linux-arm64") }), nil, "no artifact for linux-arm64"},
		{"bad sha", clone(func(r *release.Record) { r.Artifacts["linux-arm64"] = release.Artifact{SHA256: "xyz", Size: 1} }), nil, "sha256"},
		{"zero size", clone(func(r *release.Record) {
			r.Artifacts["linux-arm64"] = release.Artifact{SHA256: strings.Repeat("a", 64)}
		}), nil, "size"},
		{"major as patch", clone(func(r *release.Record) { r.Version = "v2.0.0" }), []string{"--allow-same"}, "kind patch does not match version v2.0.0 (want major)"},
		{"minor as patch", clone(func(r *release.Record) { r.Version = "v1.3.0" }), nil, "want minor"},
		{"patch as minor", clone(func(r *release.Record) { r.Kind = "minor" }), nil, "want patch"},
		{"prerelease follows its core", clone(func(r *release.Record) { r.Version, r.Kind = "v1.3.0-rc.1", "minor" }), nil, ""},
		{"build metadata", clone(func(r *release.Record) { r.Version = "v1.2.3+x" }), nil, "not a valid version"},
		{"no v", clone(func(r *release.Record) { r.MinFrom = "1.0.0" }), nil, "min_from"},
		{"bad kind", clone(func(r *release.Record) { r.Kind = "huge" }), nil, "kind"},
		{"compat list ok", clone(func(r *release.Record) { r.SDKCompatibleWith = []string{"0.8.0", "0.9.0"} }), nil, ""},
		{"bad compat entry", clone(func(r *release.Record) { r.SDKCompatibleWith = []string{"0.8.0", "x"} }), nil, "sdk_compatible_with"},
		{"no schema head", clone(func(r *release.Record) { r.SchemaHead = 0 }), nil, "schema_head"},
	}
	for _, tc := range cases {
		b, _ := release.MarshalRecord(tc.rec)
		path := filepath.Join(dir, "r.json")
		os.WriteFile(path, b, 0o644)
		var out, errb bytes.Buffer
		err := run(append([]string{"record", "--check", path}, tc.flags...), &out, &errb, env{})
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: %v\n%s", tc.name, err, out.String())
		case tc.wantErr != "" && (err == nil || !strings.Contains(out.String(), tc.wantErr)):
			t.Errorf("%s: want %q in output, err %v\n%s", tc.name, tc.wantErr, err, out.String())
		}
	}

	// Files that differ from the record are caught with --dir.
	path := buildRecordFile(t, dir)
	os.WriteFile(filepath.Join(dir, "hatamoto-linux-amd64"), []byte("changed"), 0o755)
	runErr(t, env{}, "record", "--check", path, "--dir", dir)
}

func TestKindOf(t *testing.T) {
	for v, want := range map[string]string{
		"v2.0.0": "major", "v1.0.0-rc.1": "major", "v1.2.0": "minor", "v0.5.0": "minor",
		"v1.2.3": "patch", "v1.2.3-rc.2": "patch", "v0.0.1": "patch",
	} {
		if got := kindOf(v); got != want {
			t.Errorf("kindOf(%s) = %q, want %q", v, got, want)
		}
	}
}

func TestRecordAmend(t *testing.T) {
	dir := t.TempDir()
	// A CI-shaped record: floor and min_from are placeholders equal to the version.
	facts := writeDist(t, dir, "v1.3.0")
	path := filepath.Join(dir, "release.json")
	runOK(t, env{}, "record", "--version", "v1.3.0", "--kind", "minor", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "https://example.test/n", "--dir", dir, "--out", path)
	rec, err := readRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	rec.RollbackFloor, rec.MinFrom = rec.Version, rec.Version
	b, _ := release.MarshalRecord(rec)
	os.WriteFile(path, b, 0o644)
	runErr(t, env{}, "record", "--check", path)

	// A failing amend leaves the file untouched.
	before, _ := os.ReadFile(path)
	runErr(t, env{}, "record", "--amend", path, "--security", "--rollback-floor", "v1.2.0")
	runErr(t, env{}, "record", "--amend", path, "--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--kind", "major")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("a refused amend changed the file")
	}

	runOK(t, env{}, "record", "--amend", path, "--security", "--rollback-floor", "v1.2.0", "--min-from", "v1.0.0")
	got, _ := readRecord(path)
	if !got.Security || got.RollbackFloor != "v1.2.0" || got.MinFrom != "v1.0.0" || got.Kind != "minor" || len(got.Artifacts) != 4 {
		t.Fatalf("amended record: %+v", got)
	}
	runOK(t, env{}, "record", "--check", path, "--dir", dir)
	// An absent flag leaves its field alone; --security=false clears it.
	runOK(t, env{}, "record", "--amend", path, "--security=false")
	if got, _ = readRecord(path); got.Security || got.MinFrom != "v1.0.0" {
		t.Fatalf("second amend: %+v", got)
	}
}

func TestRecordCheckDirIgnoresUnknownPlatformPaths(t *testing.T) {
	dir := t.TempDir()
	path := buildRecordFile(t, dir)
	rec, err := readRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	// A platform name that would climb out of dir if it were joined to it.
	const evil = "x/../../escape"
	rec.Artifacts[evil] = rec.Artifacts["linux-amd64"]
	b, _ := release.MarshalRecord(rec)
	os.WriteFile(path, b, 0o644)
	var out bytes.Buffer
	err = run([]string{"record", "--check", path, "--dir", dir}, &out, &bytes.Buffer{}, env{})
	if err == nil || !strings.Contains(out.String(), "unknown platform") {
		t.Fatalf("want an unknown platform problem, got %v\n%s", err, out.String())
	}
	// The only problem is the name: no file was looked up for it.
	if strings.Contains(out.String(), "no such file") || strings.Contains(out.String(), "does not match") {
		t.Fatalf("a file was read for the unknown platform:\n%s", out.String())
	}
}

func TestRecordCopiesSDKCompatibleWith(t *testing.T) {
	dir := t.TempDir()
	facts := writeDist(t, dir, "v1.2.3")
	js := `{"version":"v1.2.3","commit":"abc123","sdk":"0.10.0","schema_head":5,"sdk_compatible_with":["0.8.0","0.9.0"]}`
	os.WriteFile(facts, []byte(js), 0o644)
	out := filepath.Join(dir, "release.json")
	runOK(t, env{}, "record", "--version", "v1.2.3", "--kind", "patch", "--facts", facts,
		"--rollback-floor", "v1.2.0", "--min-from", "v1.0.0", "--notes-url", "https://example.test/n",
		"--dir", dir, "--out", out)
	b, _ := os.ReadFile(out)
	var rec release.Record
	if err := json.Unmarshal(b, &rec); err != nil || len(rec.SDKCompatibleWith) != 2 {
		t.Fatalf("got %+v, %v", rec, err)
	}
}
