package supervisor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"hatamoto.dev/core/internal/release"
)

// maxRecordFile bounds release.json and its signature when read back.
const maxRecordFile = 1 << 20

// VerifyStored re-checks a staged release before it is run: the stored
// record's signature against the build's trust, the record's version against
// the directory name, and the binary's sha256 against the record. The binary
// is opened without following symlinks and read once, with a size bound.
// Anything that can write the data volume could otherwise plant a binary here.
func VerifyStored(l Layout, version string, o release.Options) error {
	if !validName(version) {
		return fmt.Errorf("invalid release name %q", version)
	}
	dir := filepath.Join(l.Releases, version)
	recBytes, err := readNoFollow(filepath.Join(dir, "release.json"))
	if err != nil {
		return fmt.Errorf("stored record: %w", err)
	}
	sigBytes, err := readNoFollow(filepath.Join(dir, "release.json.sig"))
	if err != nil {
		return fmt.Errorf("stored signature: %w", err)
	}
	rec, err := release.VerifyRecord(recBytes, strings.TrimSpace(string(sigBytes)), o)
	if err != nil {
		return fmt.Errorf("stored record check failed: %w", err)
	}
	if rec.Version != version {
		return fmt.Errorf("stored record is for %s, not %s", rec.Version, version)
	}
	f, err := os.OpenFile(l.Binary(version), os.O_RDONLY|noFollow, 0)
	if err != nil {
		return fmt.Errorf("binary: %w", err)
	}
	defer f.Close()
	if err := release.VerifyArtifact(rec, runtime.GOOS+"-"+runtime.GOARCH, f, o); err != nil {
		return fmt.Errorf("stored binary check failed: %w", err)
	}
	return nil
}

func readNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxRecordFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRecordFile {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// verifyRelease is VerifyStored with the config's trust, or the test seam.
func (s *sup) verifyRelease(version string) error {
	if s.cfg.verify != nil {
		return s.cfg.verify(version)
	}
	return VerifyStored(s.layout, version, s.cfg.Release)
}
