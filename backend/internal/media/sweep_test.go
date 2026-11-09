package media

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var sweepNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

const sweepDir = legacyMediaTenantDir + "/"

// sweepFixture stores one object per case under the "site/" prefix.
func sweepFixture(t *testing.T) (*S3Store, func() map[string]bool) {
	t.Helper()
	st, fake := newTestS3Store(t, "site/")
	old := sweepNow.Add(-48 * time.Hour)
	for key, at := range map[string]time.Time{
		"site/" + sweepDir + "ref.png":          old,
		"site/" + sweepDir + "ref_300.png":      old,
		"site/" + sweepDir + "orphan-b.png":     old,
		"site/" + sweepDir + "orphan-a.png":     old,
		"site/" + sweepDir + "fresh.png":        sweepNow.Add(-10 * time.Minute),
		"site/" + sweepDir + "zero.png":         {},
		"site/" + sweepDir + "staged/run/x.png": old,
		"site/offsite/backup.tar":               old,
		"elsewhere/other.png":                   old,
		"site/" + sweepDir + "orphan-c.png":     old,
	} {
		fake.PutAt(key, []byte("0123456789"), nil, at)
	}
	return st, func() map[string]bool {
		left := map[string]bool{}
		for k := range fake.Objects() {
			left[k] = true
		}
		return left
	}
}

func sweepKeys(keys ...string) func(context.Context) (map[string]struct{}, error) {
	return func(context.Context) (map[string]struct{}, error) {
		m := map[string]struct{}{}
		for _, k := range keys {
			m[sweepDir+k] = struct{}{}
		}
		return m, nil
	}
}

func orphanKeys(res SweepResult) string {
	var ks []string
	for _, o := range res.Orphans {
		ks = append(ks, o.Key)
	}
	return strings.Join(ks, ",")
}

func TestSweepDryRunReportsOnlyOldUnreferencedMediaObjects(t *testing.T) {
	st, left := sweepFixture(t)
	before := len(left())
	res, err := Sweep(context.Background(), st, sweepKeys("ref.png", "ref_300.png"),
		SweepOptions{OlderThan: 24 * time.Hour, Now: sweepNow})
	if err != nil {
		t.Fatal(err)
	}
	want := sweepDir + "orphan-a.png," + sweepDir + "orphan-b.png," + sweepDir + "orphan-c.png"
	if got := orphanKeys(res); got != want {
		t.Fatalf("orphans = %s, want %s", got, want)
	}
	if res.Bytes != 30 || res.Deleted != 0 {
		t.Fatalf("bytes = %d, deleted = %d", res.Bytes, res.Deleted)
	}
	if len(left()) != before {
		t.Fatal("a dry run deleted objects")
	}
}

func TestSweepApplyDeletesOnlyOrphans(t *testing.T) {
	st, left := sweepFixture(t)
	res, err := Sweep(context.Background(), st, sweepKeys("ref.png", "ref_300.png"),
		SweepOptions{OlderThan: 24 * time.Hour, Apply: true, Now: sweepNow})
	if err != nil || res.Deleted != 3 || len(res.Failed) != 0 {
		t.Fatalf("Sweep = %+v, %v", res, err)
	}
	remaining := left()
	for _, gone := range []string{"orphan-a.png", "orphan-b.png", "orphan-c.png"} {
		if remaining["site/"+sweepDir+gone] {
			t.Errorf("%s was not deleted", gone)
		}
	}
	for _, kept := range []string{"site/" + sweepDir + "ref.png", "site/" + sweepDir + "ref_300.png",
		"site/" + sweepDir + "fresh.png", "site/" + sweepDir + "zero.png", "site/" + sweepDir + "staged/run/x.png",
		"site/offsite/backup.tar", "elsewhere/other.png"} {
		if !remaining[kept] {
			t.Errorf("%s was deleted", kept)
		}
	}
}

func TestSweepRejectsAgeBelowTheMinimum(t *testing.T) {
	st, _ := sweepFixture(t)
	for _, age := range []time.Duration{0, -time.Hour, time.Hour, 23 * time.Hour} {
		if _, err := Sweep(context.Background(), st, sweepKeys(), SweepOptions{OlderThan: age, Apply: true, Now: sweepNow}); err == nil {
			t.Errorf("age %s accepted", age)
		}
	}
}

// orderStore records the order of the listing and the row load, and keeps
// going when one delete fails.
type orderStore struct {
	SweepStore
	events  *[]string
	failKey string
}

func (o orderStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	*o.events = append(*o.events, "list "+prefix)
	return o.SweepStore.List(ctx, prefix)
}

func (o orderStore) Delete(ctx context.Context, key string) error {
	if key == o.failKey {
		return errors.New("boom")
	}
	return o.SweepStore.Delete(ctx, key)
}

func TestSweepListsMediaDirectoryBeforeLoadingRowsAndSurvivesFailures(t *testing.T) {
	st, left := sweepFixture(t)
	var events []string
	load := func(ctx context.Context) (map[string]struct{}, error) {
		events = append(events, "rows")
		return sweepKeys("ref.png", "ref_300.png")(ctx)
	}
	res, err := Sweep(context.Background(), orderStore{st, &events, sweepDir + "orphan-a.png"}, load,
		SweepOptions{OlderThan: 24 * time.Hour, Apply: true, Now: sweepNow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, "|") != "list "+sweepDir+"|rows" {
		t.Fatalf("events = %v", events)
	}
	if res.Deleted != 2 || len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Error(), "orphan-a.png") {
		t.Fatalf("Sweep = %+v", res)
	}
	if !left()["site/"+sweepDir+"orphan-a.png"] {
		t.Fatal("the failing key vanished")
	}
}
