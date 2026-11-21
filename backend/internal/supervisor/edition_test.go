package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/release"
)

// withEdition returns a record modifier carrying a community artifact for
// community and, when edition is not empty, one for the edition.
func withEdition(community, edition string) func(*release.Record) {
	return func(r *release.Record) {
		r.Artifacts = map[string]release.Artifact{testPlatform: {SHA256: shaOf(community), Size: int64(len(community))}}
		if edition != "" {
			r.Editions = map[string]map[string]release.Artifact{
				release.Editions[0]: {testPlatform: {SHA256: shaOf(edition), Size: int64(len(edition))}},
			}
		}
	}
}

// editionOpts is the apply input for a build of the given edition: the record
// is written for the "community" and "edition" bodies, the file holds body.
func (b *applyBox) editionOpts(version, body string, withEd bool) ApplyOpts {
	ed := ""
	if withEd {
		ed = "edition"
	}
	o := b.artifact(version, body, withEdition("community", ed))
	o.Edition = release.Editions[0]
	return o
}

func TestApplyEditionStagesEditionArtifact(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.editionOpts("v1.1.0", "edition", true)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(b.l.Binary("v1.1.0"))
	if err != nil || string(got) != "edition" {
		t.Fatalf("staged binary = %q, %v", got, err)
	}
}

func TestApplyEditionRefusesRecordWithoutIt(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	_, err := Apply(b.cfg, b.editionOpts("v1.1.0", "community", false))
	if !errors.Is(err, release.ErrRecordMismatch) {
		t.Fatalf("err = %v, want ErrRecordMismatch", err)
	}
	if _, err := os.Stat(filepath.Join(b.l.Releases, "v1.1.0")); err == nil {
		t.Error("something was staged")
	}
}

func TestApplyEditionRefusesDefaultBinary(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.editionOpts("v1.1.0", "community", true)); err == nil {
		t.Fatal("accepted the community binary for an edition")
	}
	if _, err := os.Stat(filepath.Join(b.l.Releases, "v1.1.0")); err == nil {
		t.Error("something was staged")
	}
}

func TestVerifyStoredForEdition(t *testing.T) {
	b := newApplyBox(t, "v1.0.0")
	if _, err := Apply(b.cfg, b.editionOpts("v1.1.0", "edition", true)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS+"-"+runtime.GOARCH != testPlatform {
		t.Skip("stored-binary check runs for the host platform only")
	}
	if err := verifyStored(b.l, "v1.1.0", release.Editions[0], b.opts); err != nil {
		t.Fatalf("edition build: %v", err)
	}
	if err := verifyStored(b.l, "v1.1.0", "", b.opts); err == nil {
		t.Error("community check accepted the edition binary")
	}
}

func TestConfigEditionDefaultsToRegistered(t *testing.T) {
	if got := (Config{}).edition(); got != editionstate.Name() {
		t.Errorf("edition() = %q, want %q", got, editionstate.Name())
	}
	if got := (Config{Edition: "x"}).edition(); got != "x" {
		t.Errorf("explicit edition = %q", got)
	}
}
