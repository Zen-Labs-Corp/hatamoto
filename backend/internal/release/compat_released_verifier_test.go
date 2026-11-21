package release

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"testing"
)

// releasedArtifact is a copy of Artifact as of v1.4.1; never update it.
type releasedArtifact struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// releasedDecode spells out the v1.4.1 decode options (jsonx.StoredLoose then:
// v2 defaults, case-insensitive names, duplicate names allowed), so a change
// to jsonx or to today's types shows up here instead of moving both sides.
var releasedDecode = jsonv2.JoinOptions(jsonv2.DefaultOptionsV2(), jsonv2.MatchCaseInsensitiveNames(true), jsontext.AllowDuplicateNames(true))

// releasedRecord is a copy of the Record struct in the last release without
// editions; never update it.
type releasedRecord struct {
	Version            string                      `json:"version"`
	Kind               string                      `json:"kind"`
	Security           bool                        `json:"security"`
	SDK                string                      `json:"sdk"`
	SDKCompatibleWith  []string                    `json:"sdk_compatible_with,omitempty"`
	RemovedPermissions []string                    `json:"removed_permissions,omitempty"`
	SchemaHead         int64                       `json:"schema_head"`
	RollbackFloor      string                      `json:"rollback_floor"`
	MinFrom            string                      `json:"min_from"`
	NotesURL           string                      `json:"notes_url"`
	Artifacts          map[string]releasedArtifact `json:"artifacts"`
}

// editionRecord is the fixture's record plus one edition build, marshalled.
func editionRecord(t testing.TB, f *fixture) (Record, []byte) {
	t.Helper()
	rec := Record{
		Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 7,
		RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/notes",
		Artifacts: map[string]Artifact{f.platform: {SHA256: sha(f.artifact), Size: int64(len(f.artifact))}},
		Editions: map[string]map[string]Artifact{
			Editions[0]: {f.platform: {SHA256: sha([]byte("other build")), Size: 11}},
		},
	}
	b, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	return rec, b
}

// A released verifier decodes a signed record that carries editions and picks
// the community artifact, so existing installs keep updating.
func TestReleasedVerifierAcceptsEditions(t *testing.T) {
	f := newFixture(t)
	rec, b := editionRecord(t, f)
	sig := f.signRecord(t, f.relKey, b)
	rel, _ := f.opts.sets()
	if err := VerifyRecordSig(rel, b, sig); err != nil {
		t.Fatal(err)
	}
	var old releasedRecord
	if err := jsonv2.Unmarshal(b, &old, releasedDecode); err != nil {
		t.Fatalf("released decode refused editions: %v", err)
	}
	if want := (releasedArtifact(rec.Artifacts[f.platform])); old.Artifacts[f.platform] != want {
		t.Fatalf("artifact = %+v, want %+v", old.Artifacts[f.platform], want)
	}
	if old.Version != rec.Version || old.RollbackFloor != rec.RollbackFloor || old.MinFrom != rec.MinFrom || old.SchemaHead != rec.SchemaHead {
		t.Fatalf("released decode differs: %+v", old)
	}
}
