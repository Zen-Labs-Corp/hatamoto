package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/supervisor"
)

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// withEditionArtifact adds a build of the edition for the test platform.
func withEditionArtifact(edition string, body []byte) func(*release.Record) {
	return func(r *release.Record) {
		r.Editions = map[string]map[string]release.Artifact{
			edition: {testPlatform: {SHA256: shaHex(body), Size: int64(len(body))}},
		}
	}
}

func TestRunEditionDownloadsEditionArtifact(t *testing.T) {
	ed := release.Editions[0]
	b := newRunBox(t, "v1.0.0")
	b.u.env.Edition = ed
	b.supervisedNow = true
	body := []byte("edition build")
	b.offer("v1.0.1", withEditionArtifact(ed, body))
	b.files["/releases/v1.0.1/hatamoto-"+ed+"-"+testPlatform] = body
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil || st.Decision != KindApplying {
		t.Fatalf("%+v %v", st, err)
	}
	if len(b.applied) != 1 || string(b.appliedSeen[0]) != string(body) || b.applied[0].Edition != ed {
		t.Fatalf("applied=%d seen=%q", len(b.applied), b.appliedSeen)
	}
	if b.hits("/hatamoto-"+ed+"-"+testPlatform) != 1 {
		t.Error("edition artifact not requested once")
	}
	if b.hits("/hatamoto-"+testPlatform) != 0 {
		t.Error("the community artifact was requested")
	}
}

func TestRunEditionRefusesRecordWithoutEdition(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.u.env.Edition = release.Editions[0]
	b.supervisedNow = true
	b.offer("v1.0.1", nil)
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	st, err := b.u.Run(t.Context(), RunOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Reason, "has no") || len(b.applied)+len(b.staged) != 0 || b.hits("hatamoto-") != 0 {
		t.Fatalf("%+v applied=%d staged=%d", st, len(b.applied), len(b.staged))
	}
}

func TestRunEditionNotWhitelistedRefuses(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	b.u.env.Edition = "other"
	b.supervisedNow = true
	b.offer("v1.0.1", withEditionArtifact("other", []byte("x")))
	b.publish([]release.Entry{open100("v1.0.1")}, nil)
	if _, err := b.u.Run(t.Context(), RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(b.applied)+len(b.staged) != 0 || b.hits("hatamoto-") != 0 {
		t.Fatalf("applied=%d staged=%d downloads=%d", len(b.applied), len(b.staged), b.hits("hatamoto-"))
	}
}

func TestAlreadyStagedComparesEditionArtifact(t *testing.T) {
	ed := release.Editions[0]
	b := newRunBox(t, "v1.0.0")
	b.offer("v1.0.1", withEditionArtifact(ed, []byte("one")))
	// Stage the release by hand: the record as published, signed, plus a binary.
	dir := filepath.Join(b.data, "releases", "v1.0.1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"release.json":     b.files["/releases/v1.0.1/release.json"],
		"release.json.sig": b.files["/releases/v1.0.1/release.json.sig"],
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(supervisor.NewLayout(b.data).Binary("v1.0.1"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	b.u.env.Edition = ed
	rec := func(body string) Decision {
		r := release.Record{Version: "v1.0.1", Kind: "patch", SDK: "0.10.0", RollbackFloor: "v1.0.0", MinFrom: "v1.0.0",
			Artifacts: map[string]release.Artifact{testPlatform: {SHA256: shaHex([]byte("binary v1.0.1")), Size: 13}}}
		withEditionArtifact(ed, []byte(body))(&r)
		return Decision{Target: "v1.0.1", Rec: &r}
	}
	if !b.u.alreadyStaged(rec("one")) {
		t.Fatal("same edition artifact: not recognised as staged")
	}
	if b.u.alreadyStaged(rec("two")) {
		t.Error("a different edition artifact counted as staged (community hash is equal)")
	}
}

func TestNewDefaultsEditionToRegistered(t *testing.T) {
	b := newRunBox(t, "v1.0.0")
	u, err := New(Env{Data: b.data, Version: "v1.0.0", Supervisor: supervisor.Config{Data: b.data}})
	if err != nil {
		t.Fatal(err)
	}
	if u.env.Edition != editionstate.Name() {
		t.Errorf("edition = %q, want %q", u.env.Edition, editionstate.Name())
	}
}
