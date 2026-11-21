package release

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func baseRecord() Record {
	return Record{
		Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 7,
		RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/notes",
		Artifacts: map[string]Artifact{
			"linux-amd64": {SHA256: strings.Repeat("a", 64), Size: 1},
			"linux-arm64": {SHA256: strings.Repeat("b", 64), Size: 2},
		},
	}
}

func TestMarshalRecordOmitsEmptyEditions(t *testing.T) {
	plain, err := MarshalRecord(baseRecord())
	if err != nil {
		t.Fatal(err)
	}
	for name, eds := range map[string]map[string]map[string]Artifact{"nil": nil, "empty": {}} {
		rec := baseRecord()
		rec.Editions = eds
		got, err := MarshalRecord(rec)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("%s editions changed the bytes:\n%s", name, got)
		}
	}
	if bytes.Contains(plain, []byte("editions")) {
		t.Errorf("editions member present:\n%s", plain)
	}
}

func TestMarshalRecordEditionsGolden(t *testing.T) {
	rec := baseRecord()
	rec.Editions = map[string]map[string]Artifact{Editions[0]: {
		"linux-arm64": {SHA256: strings.Repeat("d", 64), Size: 4},
		"linux-amd64": {SHA256: strings.Repeat("c", 64), Size: 3},
	}}
	got, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": "v1.2.3",
  "kind": "patch",
  "security": false,
  "sdk": "0.10.0",
  "schema_head": 7,
  "rollback_floor": "v1.2.0",
  "min_from": "v1.0.0",
  "notes_url": "https://example.test/notes",
  "artifacts": {
    "linux-amd64": {
      "sha256": "` + strings.Repeat("a", 64) + `",
      "size": 1
    },
    "linux-arm64": {
      "sha256": "` + strings.Repeat("b", 64) + `",
      "size": 2
    }
  },
  "editions": {
    "` + Editions[0] + `": {
      "linux-amd64": {
        "sha256": "` + strings.Repeat("c", 64) + `",
        "size": 3
      },
      "linux-arm64": {
        "sha256": "` + strings.Repeat("d", 64) + `",
        "size": 4
      }
    }
  }
}
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestArtifactFor(t *testing.T) {
	ed := Editions[0]
	community := Artifact{SHA256: "c", Size: 1}
	edition := Artifact{SHA256: "e", Size: 2}
	other := Artifact{SHA256: "o", Size: 3}
	rec := &Record{
		Artifacts: map[string]Artifact{"p": community},
		Editions:  map[string]map[string]Artifact{ed: {"p": edition}, "other": {"p": other}},
	}
	for _, tc := range []struct {
		edition, platform string
		want              Artifact
		wantErr           bool
	}{
		{"", "p", community, false},
		{"community", "p", community, false},
		{ed, "p", edition, false},
		{ed, "q", Artifact{}, true},
		{"other", "p", Artifact{}, true},
	} {
		got, err := rec.ArtifactFor(tc.edition, tc.platform)
		if (err != nil) != tc.wantErr {
			t.Errorf("ArtifactFor(%q,%q) err = %v", tc.edition, tc.platform, err)
		}
		if err != nil && !errors.Is(err, ErrRecordMismatch) {
			t.Errorf("ArtifactFor(%q,%q) err %v does not wrap ErrRecordMismatch", tc.edition, tc.platform, err)
		}
		if got != tc.want {
			t.Errorf("ArtifactFor(%q,%q) = %+v, want %+v", tc.edition, tc.platform, got, tc.want)
		}
	}
	// A record without the edition never yields the community build.
	if a, err := (&Record{Artifacts: rec.Artifacts}).ArtifactFor(ed, "p"); err == nil || a != (Artifact{}) {
		t.Errorf("no fallback: got %+v, %v", a, err)
	}
	if _, err := rec.ArtifactFor("", "q"); err == nil || !strings.HasSuffix(err.Error(), ": no artifact for q") {
		t.Errorf("community error text changed: %v", err)
	}
}

func TestArtifactName(t *testing.T) {
	if got := ArtifactName("", "linux-amd64"); got != "hatamoto-linux-amd64" {
		t.Error(got)
	}
	if got := ArtifactName("community", "linux-amd64"); got != "hatamoto-linux-amd64" {
		t.Error(got)
	}
	if got, want := ArtifactName(Editions[0], "linux-amd64"), "hatamoto-"+Editions[0]+"-linux-amd64"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func editionFixtureRecord(t *testing.T, f *fixture, editions map[string]map[string]Artifact) (*Record, []byte) {
	t.Helper()
	rec := Record{
		Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 7,
		RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/notes",
		Artifacts: map[string]Artifact{f.platform: {SHA256: sha(f.artifact), Size: int64(len(f.artifact))}},
		Editions:  editions,
	}
	b, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyRecord(b, f.signRecord(t, f.relKey, b), f.opts)
	if err != nil {
		t.Fatal(err)
	}
	return got, b
}

func TestVerifyRecordWithEditionsKeepsDefaultArtifact(t *testing.T) {
	f := newFixture(t)
	edBytes := []byte("the edition build")
	rec, _ := editionFixtureRecord(t, f, map[string]map[string]Artifact{
		Editions[0]: {f.platform: {SHA256: sha(edBytes), Size: int64(len(edBytes))}},
	})
	if err := VerifyArtifact(rec, f.platform, bytes.NewReader(f.artifact), f.opts); err != nil {
		t.Errorf("default build: %v", err)
	}
	if err := VerifyArtifactFor(rec, Editions[0], f.platform, bytes.NewReader(edBytes), f.opts); err != nil {
		t.Errorf("edition build: %v", err)
	}
	err := VerifyArtifactFor(rec, Editions[0], f.platform, bytes.NewReader(f.artifact), f.opts)
	if !errors.Is(err, ErrArtifactHash) && !errors.Is(err, ErrArtifactSize) {
		t.Errorf("default bytes against the edition: %v", err)
	}
}

func TestVerifyRecordIgnoresUnknownEditionNames(t *testing.T) {
	f := newFixture(t)
	rec, _ := editionFixtureRecord(t, f, map[string]map[string]Artifact{
		"other": {f.platform: {SHA256: sha([]byte("x")), Size: 1}},
	})
	if err := VerifyArtifact(rec, f.platform, bytes.NewReader(f.artifact), f.opts); err != nil {
		t.Error(err)
	}
}
