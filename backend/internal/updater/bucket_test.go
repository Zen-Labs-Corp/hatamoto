package updater

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/release"
)

func TestInstallIDCreatedOnceAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	id, err := InstallID(dir)
	if err != nil || len(id) != 32 {
		t.Fatalf("id %x, %v", id, err)
	}
	fi, err := os.Stat(filepath.Join(dir, InstallIDFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v, %v", fi, err)
	}
	again, err := InstallID(dir)
	if err != nil || !bytes.Equal(id, again) {
		t.Fatalf("second read differs: %x vs %x (%v)", id, again, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestInstallIDConcurrentFirstCreateYieldsOne(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	ids := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); ids[i], errs[i] = InstallID(dir) }()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || !bytes.Equal(ids[i], ids[0]) {
			t.Fatalf("caller %d: %x, %v (first %x)", i, ids[i], errs[i], ids[0])
		}
	}
}

func TestInstallIDWrongLengthIsAnErrorAndKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, InstallIDFile)
	for _, body := range []string{"", "short", string(make([]byte, 33))} {
		os.WriteFile(path, []byte(body), 0o600)
		if _, err := InstallID(dir); !errors.Is(err, ErrInstallID) {
			t.Fatalf("len %d: err = %v", len(body), err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Fatalf("file was rewritten (len %d)", len(body))
		}
	}
}

func TestBucketGolden(t *testing.T) {
	id := make([]byte, 32)
	for i := range id {
		id[i] = byte(i)
	}
	// Computed independently: int(sha256(id+version)[:2], big endian) % 100.
	for v, want := range map[string]int{"v1.0.1": 75, "v1.2.3": 35} {
		if got := Bucket(id, v); got != want {
			t.Errorf("Bucket(%s) = %d, want %d", v, got, want)
		}
	}
	if first, second := Bucket(id, "v1.0.1"), Bucket(id, "v1.0.1"); first != second {
		t.Error("not deterministic")
	}
}

// idInBucket finds an id whose bucket for version is want.
func idInBucket(t *testing.T, version string, want int) []byte {
	t.Helper()
	id := make([]byte, 32)
	for i := 0; i < 100000; i++ {
		id[0], id[1], id[2] = byte(i), byte(i>>8), byte(i>>16)
		if Bucket(id, version) == want {
			return id
		}
	}
	t.Fatalf("no id found for bucket %d", want)
	return nil
}

func TestBucketEdgeAtFivePercent(t *testing.T) {
	in, out := idInBucket(t, "v1.0.1", 4), idInBucket(t, "v1.0.1", 5)
	const pct = 5
	if b := Bucket(in, "v1.0.1"); !(b < pct) {
		t.Errorf("bucket %d must be in at %d%%", b, pct)
	}
	if b := Bucket(out, "v1.0.1"); b < pct {
		t.Errorf("bucket %d must be out at %d%%", b, pct)
	}
}

func TestRampPct(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	step := func(at string, pct int) release.RampStep { return release.RampStep{At: at, Pct: pct} }
	for _, tc := range []struct {
		name  string
		steps []release.RampStep
		want  int
	}{
		{"none", nil, 0},
		{"not yet", []release.RampStep{step("2026-11-06T00:00:00Z", 50)}, 0},
		{"started", []release.RampStep{step("2026-11-05T12:00:00Z", 5)}, 5},
		{"last started wins", []release.RampStep{step("2026-11-01T00:00:00Z", 5), step("2026-11-04T00:00:00Z", 25), step("2026-11-09T00:00:00Z", 100)}, 25},
		{"unsorted", []release.RampStep{step("2026-11-09T00:00:00Z", 100), step("2026-11-04T00:00:00Z", 25), step("2026-11-01T00:00:00Z", 5)}, 25},
		{"unparsable ignored", []release.RampStep{step("soon", 100), step("2026-11-01T00:00:00Z", 5)}, 5},
		{"offset zone", []release.RampStep{step("2026-11-05T13:00:00+02:00", 40)}, 40},
		{"clamped", []release.RampStep{step("2026-11-01T00:00:00Z", 250)}, 100},
		{"a later step may lower it", []release.RampStep{step("2026-11-01T00:00:00Z", 50), step("2026-11-02T00:00:00Z", 10)}, 10},
	} {
		if got := RampPct(tc.steps, now); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestRampPctStableSchedule(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	steps := []release.RampStep{{At: at(7 * day), Pct: 5}, {At: at(9 * day), Pct: 25}, {At: at(11 * day), Pct: 100}}
	for _, tc := range []struct {
		name string
		now  time.Duration
		want int
	}{
		{"6.9d", 6*day + 22*time.Hour, 0}, {"7d", 7 * day, 5}, {"8.9d", 8*day + 22*time.Hour, 5},
		{"9d", 9 * day, 25}, {"10.9d", 10*day + 22*time.Hour, 25}, {"11d", 11 * day, 100},
	} {
		if got := RampPct(steps, base.Add(tc.now)); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}
