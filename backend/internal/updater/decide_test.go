package updater

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/release"
)

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// world is a fake channel: records by version, a manifest, and a call log.
type world struct {
	recs    map[string]*release.Record
	entries []release.Entry
	yanks   []release.Yank
	calls   []string
	failRec map[string]bool
}

func newWorld() *world { return &world{recs: map[string]*release.Record{}, failRec: map[string]bool{}} }

func open() []release.RampStep { return []release.RampStep{{At: "2026-01-01T00:00:00Z", Pct: 100}} }

// add lists version on the channel with the given ramp; minFrom/kind default sensibly.
func (w *world) add(version string, ramp []release.RampStep, edit ...func(*release.Record, *release.Entry)) {
	rec := &release.Record{Version: version, Kind: kindFor(version), MinFrom: "v1.0.0", RollbackFloor: "v1.0.0"}
	e := release.Entry{Version: version, Ramp: ramp}
	for _, f := range edit {
		f(rec, &e)
	}
	w.recs[version] = rec
	w.entries = append(w.entries, e)
}

func kindFor(v string) string {
	switch {
	case strings.HasSuffix(v, ".0.0"):
		return "major"
	case strings.HasSuffix(v, ".0"):
		return "minor"
	}
	return "patch"
}

func (w *world) input(running string) Input {
	return Input{
		Running:  running,
		Manifest: &release.ChannelManifest{Releases: w.entries, Yanked: w.yanks},
		Settings: Settings{Mode: ModeAuto, Channel: "stable"},
		Now:      t0,
		Bucket:   func(string) int { return 0 },
		Record: func(v string) (*release.Record, error) {
			w.calls = append(w.calls, v)
			if w.failRec[v] {
				return nil, errors.New("boom")
			}
			r, ok := w.recs[v]
			if !ok {
				return nil, errors.New("not found")
			}
			return r, nil
		},
	}
}

func mustDecide(t *testing.T, in Input) Decision {
	t.Helper()
	d, err := Decide(in)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecideRampEdgeUsesRealBucket(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", []release.RampStep{{At: "2026-11-01T00:00:00Z", Pct: 5}})
	for _, tc := range []struct {
		bucket     int
		wantAction string
		wantKind   string
	}{{4, ActionApply, KindApplying}, {5, ActionNone, KindScheduled}} {
		in := w.input("v1.0.0")
		in.Bucket = func(v string) int { return Bucket(idInBucket(t, v, tc.bucket), v) }
		d := mustDecide(t, in)
		if d.Action != tc.wantAction || d.Kind != tc.wantKind {
			t.Errorf("bucket %d: %+v", tc.bucket, d)
		}
		if tc.bucket == 5 && !strings.Contains(d.Reason, "bucket 5, ramp 5%") {
			t.Errorf("reason %q", d.Reason)
		}
	}
}

// TestDecideChannelSchedule pins the default schedule: canary at day 0, edge at
// +2d, stable at +7d (5%), +9d (25%), +11d (100%).
func TestDecideChannelSchedule(t *testing.T) {
	day := 24 * time.Hour
	at := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339) }
	step := func(d time.Duration, pct int) release.RampStep { return release.RampStep{At: at(d), Pct: pct} }
	canary := []release.RampStep{step(0, 100)}
	edge := []release.RampStep{step(2*day, 100)}
	stable := []release.RampStep{step(7*day, 5), step(9*day, 25), step(11*day, 100)}
	for _, tc := range []struct {
		name    string
		channel string
		ramp    []release.RampStep
		now     time.Duration
		bucket  int
		want    bool
	}{
		{"canary day 0", "canary", canary, 0, 99, true},
		{"edge just before 2d", "edge", edge, 2*day - 2*time.Hour, 99, false},
		{"edge 2d", "edge", edge, 2 * day, 99, true},
		{"stable just before 7d", "stable", stable, 7*day - 2*time.Hour, 0, false},
		{"stable 7d bucket 4", "stable", stable, 7 * day, 4, true},
		{"stable 7d bucket 5", "stable", stable, 7 * day, 5, false},
		{"stable 9d bucket 24", "stable", stable, 9 * day, 24, true},
		{"stable 9d bucket 25", "stable", stable, 9 * day, 25, false},
		{"stable just before 11d bucket 99", "stable", stable, 11*day - 2*time.Hour, 99, false},
		{"stable 11d bucket 99", "stable", stable, 11 * day, 99, true},
	} {
		w := newWorld()
		w.add("v1.0.1", tc.ramp)
		in := w.input("v1.0.0")
		in.Settings.Channel = tc.channel
		in.Now = t0.Add(tc.now)
		in.Bucket = func(string) int { return tc.bucket }
		d := mustDecide(t, in)
		if got := d.Action == ActionApply; got != tc.want {
			t.Errorf("%s: %+v, offered=%v want %v", tc.name, d, got, tc.want)
		}
	}
}

func TestDecideOutOfBucketFetchesNoRecord(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", []release.RampStep{{At: "2026-11-01T00:00:00Z", Pct: 5}})
	in := w.input("v1.0.0")
	in.Bucket = func(string) int { return 50 }
	mustDecide(t, in)
	if len(w.calls) != 0 {
		t.Fatalf("records fetched: %v", w.calls)
	}
}

func TestDecideHaltedIsNeverATarget(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open(), func(_ *release.Record, e *release.Entry) { e.Halted = true })
	d := mustDecide(t, w.input("v1.0.0"))
	if d.Action != ActionNone || d.Kind != KindUpToDate || len(w.calls) != 0 {
		t.Fatalf("%+v calls %v", d, w.calls)
	}
}

func TestDecideYankedAndBadAreSkipped(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	w.add("v1.0.2", open())
	w.add("v1.0.3", open())
	w.yanks = []release.Yank{{Version: "v1.0.3", Reason: "x"}}
	in := w.input("v1.0.0")
	in.Bad = func(v string) bool { return v == "v1.0.2" }
	d := mustDecide(t, in)
	if d.Target != "v1.0.1" || d.Action != ActionApply {
		t.Fatalf("%+v", d)
	}
}

func TestDecideYankOfRunningWithToIgnoresRampWindowDefer(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	w.add("v1.0.2", []release.RampStep{{At: "2099-01-01T00:00:00Z", Pct: 100}}) // ramp closed
	w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "broken", To: "v1.0.2"}}
	in := w.input("v1.0.1")
	h1, h2 := 3, 4
	in.Settings.WindowStart, in.Settings.WindowEnd, in.Settings.DeferDays = &h1, &h2, 7
	in.Bucket = func(string) int { return 99 }
	d := mustDecide(t, in)
	if d.Action != ActionApply || d.Target != "v1.0.2" || d.Rec == nil || !strings.Contains(d.Reason, "withdrawn") {
		t.Fatalf("%+v", d)
	}
}

func TestDecideYankToCases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prep       func(w *world, in *Input)
		wantAction string
	}{
		{"to is bad, no previous", func(w *world, in *Input) { in.Bad = func(v string) bool { return v == "v1.0.2" } }, ActionBlocked},
		{"to is bad, previous ok", func(w *world, in *Input) {
			in.Bad = func(v string) bool { return v == "v1.0.2" }
			in.Previous = "v1.0.0"
			in.RunningRecord = w.recs["v1.0.1"]
		}, ActionRollback},
		{"to is halted", func(w *world, in *Input) { w.entries[1].Halted = true; in.Manifest.Releases = w.entries }, ActionBlocked},
		{"to is yanked too", func(w *world, in *Input) {
			in.Manifest.Yanked = append(in.Manifest.Yanked, release.Yank{Version: "v1.0.2"})
		}, ActionBlocked},
		{"to needs a newer base", func(w *world, in *Input) { w.recs["v1.0.2"].MinFrom = "v1.0.2" }, ActionBlocked},
		{"to is a major", func(w *world, in *Input) {
			w.entries[1].Version = "v2.0.0"
			w.recs["v2.0.0"] = &release.Record{Version: "v2.0.0", Kind: "major"}
			in.Manifest.Yanked = []release.Yank{{Version: "v1.0.1", To: "v2.0.0"}}
		}, ActionAvailable},
		{"to unreadable", func(w *world, in *Input) { w.failRec["v1.0.2"] = true }, ActionBlocked},
	} {
		w := newWorld()
		w.add("v1.0.1", open())
		w.add("v1.0.2", open())
		w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "r", To: "v1.0.2"}}
		in := w.input("v1.0.1")
		tc.prep(w, &in)
		d := mustDecide(t, in)
		if d.Action != tc.wantAction {
			t.Errorf("%s: %+v", tc.name, d)
		}
		if d.Action == ActionApply {
			t.Errorf("%s: must not apply", tc.name)
		}
	}
}

func TestDecideYankBackFloor(t *testing.T) {
	for _, tc := range []struct {
		name, previous, floor string
		record                bool
		want                  string
	}{
		{"previous at floor", "v1.0.0", "v1.0.0", true, ActionRollback},
		{"previous above floor", "v1.0.5", "v1.0.0", true, ActionRollback},
		{"previous below floor", "v0.9.0", "v1.0.0", true, ActionBlocked},
		{"no previous", "", "v1.0.0", true, ActionBlocked},
		{"no record", "v1.0.0", "", false, ActionBlocked},
	} {
		w := newWorld()
		w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}} // no To
		in := w.input("v1.0.1")
		in.Previous = tc.previous
		if tc.record {
			in.RunningRecord = &release.Record{Version: "v1.0.1", RollbackFloor: tc.floor}
		}
		d := mustDecide(t, in)
		if d.Action != tc.want {
			t.Errorf("%s: %+v", tc.name, d)
		}
		if tc.want == ActionRollback && (d.Target != tc.previous || d.Kind != KindRolledBackYank) {
			t.Errorf("%s: %+v", tc.name, d)
		}
	}
	// A yanked previous is not a refuge.
	w := newWorld()
	w.yanks = []release.Yank{{Version: "v1.0.1"}, {Version: "v1.0.0"}}
	in := w.input("v1.0.1")
	in.Previous, in.RunningRecord = "v1.0.0", &release.Record{RollbackFloor: "v1.0.0"}
	if d := mustDecide(t, in); d.Action != ActionBlocked {
		t.Errorf("yanked previous: %+v", d)
	}
}

func TestDecidePin(t *testing.T) {
	w := newWorld()
	w.add("v1.4.1", open())
	w.add("v1.5.0", open())
	in := w.input("v1.4.0")
	in.Settings.Pin = "v1.4"
	d := mustDecide(t, in)
	if d.Action != ActionApply || d.Target != "v1.4.1" {
		t.Fatalf("pinned: %+v", d)
	}

	// Only out-of-pin releases: nothing to do.
	w = newWorld()
	w.add("v1.5.0", open())
	in = w.input("v1.4.0")
	in.Settings.Pin = "v1.4"
	if d := mustDecide(t, in); d.Action != ActionNone || !strings.Contains(d.Reason, "pinned") {
		t.Fatalf("outside pin: %+v", d)
	}

	// A security release escapes the pin inside the major.
	w = newWorld()
	w.add("v1.4.1", open())
	w.add("v1.5.1", open(), func(r *release.Record, _ *release.Entry) { r.Security = true })
	in = w.input("v1.4.0")
	in.Settings.Pin = "v1.4"
	if d := mustDecide(t, in); d.Action != ActionApply || d.Target != "v1.5.1" {
		t.Fatalf("security escape: %+v", d)
	}

	// ...but never across a major.
	w = newWorld()
	w.add("v2.0.0", open(), func(r *release.Record, _ *release.Entry) { r.Security = true })
	in = w.input("v1.4.0")
	in.Settings.Pin = "v1.4"
	if d := mustDecide(t, in); d.Action != ActionAvailable {
		t.Fatalf("major: %+v", d)
	}
}

func TestDecideDeferVersusSecurity(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	in := w.input("v1.0.0")
	in.Settings.DeferDays = 3
	d := mustDecide(t, in)
	if d.Action != ActionWait || d.Kind != KindWaitingDefer || !d.FirstSeen["v1.0.1"].Equal(t0) {
		t.Fatalf("first sight: %+v", d)
	}
	in.FirstSeen = d.FirstSeen
	in.Now = t0.AddDate(0, 0, 2)
	if d := mustDecide(t, in); d.Action != ActionWait || !d.FirstSeen["v1.0.1"].Equal(t0) {
		t.Fatalf("two days: %+v", d)
	}
	in.Now = t0.AddDate(0, 0, 3)
	if d := mustDecide(t, in); d.Action != ActionApply {
		t.Fatalf("after defer: %+v", d)
	}

	w = newWorld()
	w.add("v1.0.1", open(), func(r *release.Record, _ *release.Entry) { r.Security = true })
	in = w.input("v1.0.0")
	in.Settings.DeferDays = 14
	if d := mustDecide(t, in); d.Action != ActionApply {
		t.Fatalf("security: %+v", d)
	}
}

func TestDecideFirstSeenIsPruned(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	in := w.input("v1.0.0")
	in.FirstSeen = map[string]time.Time{"v0.9.0": t0, "v1.0.1": t0.Add(-time.Hour)}
	d := mustDecide(t, in)
	if _, ok := d.FirstSeen["v0.9.0"]; ok || !d.FirstSeen["v1.0.1"].Equal(t0.Add(-time.Hour)) {
		t.Fatalf("%v", d.FirstSeen)
	}
}

func TestDecideMajorIsOnlyAvailable(t *testing.T) {
	w := newWorld()
	w.add("v2.0.0", open())
	w.add("v3.0.0", open())
	d := mustDecide(t, w.input("v1.4.0"))
	if d.Action != ActionAvailable || d.Kind != KindAvailableMajor || d.AvailableMajor != "v3.0.0" || d.Target != "v3.0.0" {
		t.Fatalf("%+v", d)
	}
	if len(w.calls) != 0 {
		t.Fatalf("a major must not fetch records: %v", w.calls)
	}
	// A minor alongside is still applied, and the major is reported.
	w.add("v1.5.0", open())
	d = mustDecide(t, w.input("v1.4.0"))
	if d.Action != ActionApply || d.Target != "v1.5.0" || d.AvailableMajor != "v3.0.0" {
		t.Fatalf("%+v", d)
	}
}

func TestDecideRecordKindMajorInsideSameMajorIsAvailable(t *testing.T) {
	w := newWorld()
	w.add("v1.5.0", open(), func(r *release.Record, _ *release.Entry) { r.Kind = "major" })
	d := mustDecide(t, w.input("v1.4.0"))
	if d.Action != ActionAvailable || d.AvailableMajor != "v1.5.0" {
		t.Fatalf("%+v", d)
	}
}

func TestDecideMinFromSteppingOneHopPerCheck(t *testing.T) {
	w := newWorld()
	w.add("v1.2.0", open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = "v1.1.0" })
	w.add("v1.1.0", open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = "v1.0.0" })
	d := mustDecide(t, w.input("v1.0.0"))
	if d.Action != ActionApply || d.Target != "v1.1.0" {
		t.Fatalf("hop: %+v", d)
	}
	if strings.Join(w.calls, ",") != "v1.2.0,v1.1.0" {
		t.Fatalf("newest first: %v", w.calls)
	}

	// Nothing reachable: blocked, with the reason.
	w = newWorld()
	w.add("v1.2.0", open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = "v1.1.0" })
	d = mustDecide(t, w.input("v1.0.0"))
	if d.Action != ActionBlocked || !strings.Contains(d.Reason, "v1.2.0 needs v1.1.0") {
		t.Fatalf("%+v", d)
	}
}

func TestDecideRecordFetchCapAndErrors(t *testing.T) {
	w := newWorld()
	for i := 1; i <= 8; i++ {
		v := "v1.0." + string(rune('0'+i))
		w.add(v, open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = "v1.0.9" })
	}
	d := mustDecide(t, w.input("v1.0.0"))
	if len(w.calls) != MaxRecordFetches || d.Action != ActionBlocked {
		t.Fatalf("calls %v, %+v", w.calls, d)
	}

	// Every read failing is an error; one failing is skipped.
	w = newWorld()
	w.add("v1.0.1", open())
	w.add("v1.0.2", open())
	w.failRec["v1.0.2"] = true
	if d := mustDecide(t, w.input("v1.0.0")); d.Target != "v1.0.1" || d.Action != ActionApply {
		t.Fatalf("one failure: %+v", d)
	}
	w.failRec["v1.0.1"] = true
	if _, err := Decide(w.input("v1.0.0")); err == nil {
		t.Fatal("all reads failing must error")
	}
}

func TestDecideRecordVersionMismatchIsSkipped(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	w.add("v1.0.2", open())
	w.recs["v1.0.2"].Version = "v1.0.9" // a record for the wrong release
	d := mustDecide(t, w.input("v1.0.0"))
	if d.Target != "v1.0.1" {
		t.Fatalf("%+v", d)
	}
}

func TestDecideWindow(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	start, end := 22, 6 // wraps midnight; now is 12:00 UTC
	in := w.input("v1.0.0")
	in.Settings.WindowStart, in.Settings.WindowEnd = &start, &end
	if d := mustDecide(t, in); d.Action != ActionWait || d.Kind != KindWaitingWindow {
		t.Fatalf("outside: %+v", d)
	}
	in.Now = time.Date(2026, 11, 5, 23, 30, 0, 0, time.UTC)
	if d := mustDecide(t, in); d.Action != ActionApply {
		t.Fatalf("inside: %+v", d)
	}
	// Security ignores the window.
	w2 := newWorld()
	w2.add("v1.0.1", open(), func(r *release.Record, _ *release.Entry) { r.Security = true })
	in = w2.input("v1.0.0")
	in.Settings.WindowStart, in.Settings.WindowEnd = &start, &end
	if d := mustDecide(t, in); d.Action != ActionApply {
		t.Fatalf("security: %+v", d)
	}
}

func TestInWindow(t *testing.T) {
	h := func(n int) *int { return &n }
	at := func(hour int) time.Time { return time.Date(2026, 11, 5, hour, 30, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name       string
		start, end *int
		hour       int
		want       bool
	}{
		{"no window", nil, nil, 3, true},
		{"one bound", h(2), nil, 3, true},
		{"equal bounds", h(5), h(5), 3, true},
		{"inside", h(2), h(4), 3, true},
		{"start inclusive", h(3), h(4), 3, true},
		{"end exclusive", h(2), h(3), 3, false},
		{"outside", h(2), h(4), 12, false},
		{"wrap late", h(22), h(6), 23, true},
		{"wrap early", h(22), h(6), 5, true},
		{"wrap outside", h(22), h(6), 12, false},
	} {
		if got := InWindow(at(tc.hour), nil, tc.start, tc.end); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
	// The window is read in the site timezone.
	loc := time.FixedZone("x", 2*3600)
	if !InWindow(time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC), loc, h(2), h(4)) { // 03:00 local
		t.Error("local hour not used")
	}
}

func TestDecideDevAndUpToDate(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	for _, v := range []string{"dev", "", "v1.0", "v1.0.0+dirty"} {
		d := mustDecide(t, w.input(v))
		if d.Kind != KindDev || d.Action != ActionNone || len(w.calls) != 0 {
			t.Errorf("%q: %+v", v, d)
		}
	}
	d := mustDecide(t, w.input("v1.0.1"))
	if d.Action != ActionNone || d.Kind != KindUpToDate {
		t.Fatalf("up to date: %+v", d)
	}
	d = mustDecide(t, w.input("v1.0.5"))
	if d.Action != ActionNone || d.Kind != KindUpToDate {
		t.Fatalf("newer than channel: %+v", d)
	}
}

func TestDecideIgnoresInvalidManifestVersions(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	w.entries = append(w.entries, release.Entry{Version: "latest", Ramp: open()}, release.Entry{Version: "v1.0.1", Ramp: open()})
	d := mustDecide(t, w.input("v1.0.0"))
	if d.Target != "v1.0.1" || len(w.calls) != 1 {
		t.Fatalf("%+v calls %v", d, w.calls)
	}
	if _, err := Decide(Input{Running: "v1.0.0"}); err == nil {
		t.Fatal("nil manifest must error")
	}
}

func TestDecideYankBackToTheBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name, bootstrap, floor string
		want                   string
	}{
		{"at floor", "v1.0.0", "v1.0.0", ActionRollback},
		{"above floor", "v1.0.3", "v1.0.0", ActionRollback},
		{"below floor", "v0.9.0", "v1.0.0", ActionBlocked},
		{"no floor, non-semver bootstrap", "dev", "", ActionRollback},
		{"no floor, unknown bootstrap", "", "", ActionRollback},
		{"floor set, non-semver bootstrap", "dev", "v1.0.0", ActionBlocked},
		{"floor set, unknown bootstrap", "", "v1.0.0", ActionBlocked},
	} {
		w := newWorld()
		w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}}
		in := w.input("v1.0.1")
		in.Previous, in.PreviousIsBootstrap = tc.bootstrap, true
		in.RunningRecord = &release.Record{Version: "v1.0.1", RollbackFloor: tc.floor}
		if d := mustDecide(t, in); d.Action != tc.want || (tc.want == ActionBlocked && d.Reason == "") {
			t.Errorf("%s: %+v", tc.name, d)
		}
	}
	// A bootstrap on the bad list or withdrawn is not a refuge, and that is a
	// status, not an error.
	w := newWorld()
	w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}}
	in := w.input("v1.0.1")
	in.Previous, in.PreviousIsBootstrap = "v1.0.0", true
	in.RunningRecord = &release.Record{Version: "v1.0.1", RollbackFloor: "v1.0.0"}
	in.Bad = func(v string) bool { return v == "bootstrap" }
	if d := mustDecide(t, in); d.Action != ActionBlocked || !strings.Contains(d.Reason, "bad list") {
		t.Errorf("bad bootstrap: %+v", d)
	}
}

func TestDecideYankBackToABadListedPreviousIsBlocked(t *testing.T) {
	w := newWorld()
	w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}}
	in := w.input("v1.0.1")
	in.Previous, in.RunningRecord = "v1.0.0", &release.Record{RollbackFloor: "v1.0.0"}
	in.Bad = func(v string) bool { return v == "v1.0.0" }
	if d := mustDecide(t, in); d.Action != ActionBlocked || d.Kind != KindBlocked || !strings.Contains(d.Reason, "bad list") {
		t.Errorf("%+v", d)
	}
}

func TestDecideYankedRunningWithoutToPrefersALaterFix(t *testing.T) {
	w := newWorld()
	w.add("v1.0.2", open())
	w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}} // no To
	in := w.input("v1.0.1")
	in.Previous, in.PreviousIsBootstrap = "v1.0.0", true
	in.RunningRecord = &release.Record{RollbackFloor: "v1.0.0"}
	d := mustDecide(t, in)
	if d.Action != ActionApply || d.Target != "v1.0.2" {
		t.Fatalf("a later fix should win: %+v", d)
	}
	// With no usable newer release it rolls back, even with one that is out of
	// its ramp.
	w = newWorld()
	w.add("v1.0.2", []release.RampStep{{At: "2026-01-01T00:00:00Z", Pct: 0}})
	w.yanks = []release.Yank{{Version: "v1.0.1", Reason: "bad"}}
	in = w.input("v1.0.1")
	in.Previous, in.PreviousIsBootstrap = "v1.0.0", true
	in.RunningRecord = &release.Record{RollbackFloor: "v1.0.0"}
	if d := mustDecide(t, in); d.Action != ActionRollback {
		t.Fatalf("%+v", d)
	}
}

func TestDecideSteppingStoneBeyondTheReadCap(t *testing.T) {
	// A box on v1.0.0 and seven releases: v1.0.3..v1.0.7 need v1.0.2, v1.0.2
	// needs v1.0.1, v1.0.1 needs nothing newer. Newest-first reads alone would
	// spend the whole budget above the stepping stone.
	w := newWorld()
	for i := 1; i <= 7; i++ {
		v := "v1.0." + string(rune('0'+i))
		from := "v1.0.2"
		if i == 2 {
			from = "v1.0.1"
		}
		if i == 1 {
			from = "v1.0.0"
		}
		w.add(v, open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = from })
	}
	d := mustDecide(t, w.input("v1.0.0"))
	if d.Action != ActionApply || d.Target != "v1.0.1" || len(w.calls) > MaxRecordFetches {
		t.Fatalf("%+v calls %v", d, w.calls)
	}
}

func TestDecidePinIsNotStarvedByNewerLines(t *testing.T) {
	w := newWorld()
	w.add("v1.4.1", open())
	for _, v := range []string{"v1.5.0", "v1.5.1", "v1.5.2", "v1.5.3", "v1.5.4", "v1.5.5"} {
		w.add(v, open())
	}
	in := w.input("v1.4.0")
	in.Settings.Pin = "v1.4"
	d := mustDecide(t, in)
	if d.Action != ActionApply || d.Target != "v1.4.1" {
		t.Fatalf("%+v calls %v", d, w.calls)
	}
}

func TestDecideNotifyReadsNoRecords(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	w.add("v1.0.2", open())
	in := w.input("v1.0.0")
	in.Settings.Mode = ModeNotify
	d := mustDecide(t, in)
	if d.Kind != KindNotify || d.Target != "v1.0.2" || len(w.calls) != 0 {
		t.Fatalf("%+v calls %v", d, w.calls)
	}
	// A yank-to is reported too, without reading the replacement.
	w = newWorld()
	w.add("v1.0.2", open())
	w.yanks = []release.Yank{{Version: "v1.0.1", To: "v1.0.2", Reason: "bad"}}
	in = w.input("v1.0.1")
	in.Settings.Mode = ModeNotify
	d = mustDecide(t, in)
	if d.Kind != KindNotify || d.Target != "v1.0.2" || len(w.calls) != 0 {
		t.Fatalf("yank-to: %+v calls %v", d, w.calls)
	}
}

func TestDecideRolledBackNewestIsNotUpToDate(t *testing.T) {
	w := newWorld()
	w.add("v1.0.1", open())
	in := w.input("v1.0.0")
	in.Bad = func(v string) bool { return v == "v1.0.1" }
	in.BadReason = func(string) string { return "health gate failed" }
	d := mustDecide(t, in)
	if d.Kind != KindRolledBack || d.Target != "v1.0.1" || d.Reason != "health gate failed" || d.Action != ActionNone {
		t.Fatalf("%+v", d)
	}
	// A newer good release still wins.
	w.add("v1.0.2", open())
	if d := mustDecide(t, w.input2(in)); d.Action != ActionApply || d.Target != "v1.0.2" {
		t.Fatalf("%+v", d)
	}
}

func (w *world) input2(base Input) Input {
	base.Manifest = &release.ChannelManifest{Releases: w.entries, Yanked: w.yanks}
	return base
}

// The first release (v0.25.0) is recorded with min_from v0.0.0-0, the lowest
// possible prerelease, so every dev build v0.0.0-dev.N can step to it. Plain
// v0.0.0 would sort ABOVE v0.0.0-dev.N and block them.
func TestDecideFirstReleaseMinFromAdmitsDevBuilds(t *testing.T) {
	for _, tc := range []struct {
		name, running, minFrom string
		action                 string
		target                 string
	}{
		{"dev build takes min_from v0.0.0-0", "v0.0.0-dev.5", "v0.0.0-0", ActionApply, "v0.25.0"},
		{"v0.0.0 would block the dev build", "v0.0.0-dev.5", "v0.0.0", ActionBlocked, ""},
		{"a newer dev box never downgrades to v0.25.0", "v0.25.1-dev.3", "v0.0.0-0", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.add("v0.25.0", open(), func(r *release.Record, _ *release.Entry) { r.MinFrom = tc.minFrom; r.RollbackFloor = "v0.0.0-0" })
			d := mustDecide(t, w.input(tc.running))
			if tc.action == "" {
				if d.Action == ActionApply || d.Target == "v0.25.0" {
					t.Fatalf("downgrade offered: %+v", d)
				}
				return
			}
			if d.Action != tc.action || d.Target != tc.target {
				t.Fatalf("%+v", d)
			}
		})
	}
}
