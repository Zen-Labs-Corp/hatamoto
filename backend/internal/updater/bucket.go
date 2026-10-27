package updater

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"hatamoto.dev/core/internal/release"
)

// InstallIDFile is the name of the install id file under the data directory.
const InstallIDFile = "install_id"

// installIDSize is the length of the install id in bytes.
const installIDSize = 32

// ErrInstallID means the install id file exists but is not 32 bytes. It is
// never replaced silently: a new id would move the box to another bucket.
var ErrInstallID = errors.New("install_id is unreadable")

// InstallID returns the box's random id, creating the file (0600) on first
// use. The id picks the rollout bucket and never leaves the machine.
func InstallID(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, InstallIDFile)
	if id, err := readInstallID(path); !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dataDir, err)
	}
	id := make([]byte, installIDSize)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("generate install id: %w", err)
	}
	// Write a private temp file, then link it into place: the link fails when
	// another process won the race, and no reader ever sees a partial file.
	tmp, err := os.CreateTemp(dataDir, ".install_id-*")
	if err != nil {
		return nil, fmt.Errorf("create install id: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(id); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("write install id: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("write install id: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return nil, fmt.Errorf("write install id: %w", err)
	}
	switch err := os.Link(tmp.Name(), path); {
	case err == nil:
		return id, nil
	case errors.Is(err, os.ErrExist):
		return readInstallID(path)
	default:
		return nil, fmt.Errorf("create install id: %w", err)
	}
}

func readInstallID(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrInstallID, err)
	}
	if len(b) != installIDSize {
		return nil, fmt.Errorf("%w: %s holds %d bytes, want %d", ErrInstallID, path, len(b), installIDSize)
	}
	return b, nil
}

// Bucket places an install in 0..99 for one version: the first two bytes of
// sha256(id || version) as a big-endian number, modulo 100. An install is in a
// rollout of pct percent when Bucket < pct.
func Bucket(id []byte, version string) int {
	h := sha256.Sum256(append(append([]byte{}, id...), version...))
	return int(binary.BigEndian.Uint16(h[:2])) % 100
}

// RampPct is the percentage of the last ramp step that has started by now (the
// step with the latest start time not after now). A step whose start time does
// not parse is ignored; no started step means 0. Step order does not matter.
func RampPct(steps []release.RampStep, now time.Time) int {
	pct := 0
	var latest time.Time
	for _, s := range steps {
		at, err := time.Parse(time.RFC3339, s.At)
		if err != nil {
			slog.Warn("ignoring ramp step with an unparsable start", "from", s.At)
			continue
		}
		if at.After(now) || (!latest.IsZero() && at.Before(latest)) {
			continue
		}
		latest, pct = at, min(max(s.Pct, 0), 100)
	}
	return pct
}
