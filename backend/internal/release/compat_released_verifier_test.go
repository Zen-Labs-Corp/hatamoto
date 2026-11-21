package release

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// releasedRecord is a copy of the Record struct in the last release without
// editions; never update it.
type releasedRecord struct {
	Version            string              `json:"version"`
	Kind               string              `json:"kind"`
	Security           bool                `json:"security"`
	SDK                string              `json:"sdk"`
	SDKCompatibleWith  []string            `json:"sdk_compatible_with,omitempty"`
	RemovedPermissions []string            `json:"removed_permissions,omitempty"`
	SchemaHead         int64               `json:"schema_head"`
	RollbackFloor      string              `json:"rollback_floor"`
	MinFrom            string              `json:"min_from"`
	NotesURL           string              `json:"notes_url"`
	Artifacts          map[string]Artifact `json:"artifacts"`
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
	if err := jsonx.Unmarshal(b, &old, jsonx.StoredLoose); err != nil {
		t.Fatalf("released decode refused editions: %v", err)
	}
	if old.Artifacts[f.platform] != rec.Artifacts[f.platform] {
		t.Fatalf("artifact = %+v, want %+v", old.Artifacts[f.platform], rec.Artifacts[f.platform])
	}
	if old.Version != rec.Version || old.RollbackFloor != rec.RollbackFloor || old.MinFrom != rec.MinFrom || old.SchemaHead != rec.SchemaHead {
		t.Fatalf("released decode differs: %+v", old)
	}
}
