package analytics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeTrafficRepo struct {
	rows []TrafficRow
	fail bool
}

func (f *fakeTrafficRepo) AddTraffic(_ context.Context, rows []TrafficRow) error {
	if f.fail {
		return errors.New("down")
	}
	f.rows = append(f.rows, rows...)
	return nil
}

// touchedRepo fails the test when anything reaches the database before Flush.
type touchedRepo struct{ t *testing.T }

func (r touchedRepo) Add(context.Context, time.Time, string, string, int64) error {
	r.t.Fatal("DB touched on the record path")
	return nil
}
func (r touchedRepo) AddTraffic(context.Context, []TrafficRow) error {
	r.t.Fatal("DB touched on the record path")
	return nil
}

func rowMap(rows []TrafficRow) map[string]Counts {
	m := map[string]Counts{}
	for _, r := range rows {
		m[fmt.Sprintf("%s|%s|%s", r.Dim, r.K1, r.K2)] = r.Counts
	}
	return m
}

func TestRecordTrafficIncrements(t *testing.T) {
	repo := &fakeTrafficRepo{}
	svc := newTestService(&fakeRepo{})
	svc.SetTrafficRepo(repo)
	entry := Event{Entry: true, UTMCampaign: "x", UTMMedium: "email", Path: "/landing", Device: "mobile", Country: "DE"}
	svc.RecordTraffic(entry, ChannelEmail, "newsletter")
	next := entry
	next.Entry = false
	svc.RecordTraffic(next, ChannelEmail, "newsletter")
	eng := entry
	eng.Entry, eng.Eng, eng.First = false, "multi", true
	svc.RecordTraffic(eng, ChannelEmail, "newsletter")
	eng.Eng, eng.First = "scroll", false
	svc.RecordTraffic(eng, ChannelEmail, "newsletter")
	svc.RecordTraffic(Event{Entry: true, Device: "desktop"}, ChannelDirect, SourceNone)

	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := rowMap(repo.rows)
	want := Counts{Visits: 1, Pageviews: 2, Engaged: 1, Multi: 1, Scrolled: 1}
	for _, k := range []string{"channel|email|", "source|newsletter|email", "campaign|x|email", "landing|/landing|", "device|mobile|", "country|DE|"} {
		if m[k] != want {
			t.Errorf("%s = %+v, want %+v", k, m[k], want)
		}
	}
	if got := m["country|(unknown)|"]; got.Visits != 1 {
		t.Errorf("unknown country = %+v", got)
	}
	if _, ok := m["campaign||"]; ok {
		t.Error("campaign row without a campaign")
	}
	if _, ok := m["landing||"]; ok {
		t.Error("landing row without a path")
	}
}

func TestRecordTrafficCapsKeysPerDim(t *testing.T) {
	repo := &fakeTrafficRepo{}
	svc := newTestService(&fakeRepo{})
	svc.SetTrafficRepo(repo)
	for i := 0; i < maxKeysPerDim+50; i++ {
		svc.RecordTraffic(Event{Entry: true, Path: fmt.Sprintf("/p%d", i), Device: "other"}, ChannelDirect, SourceNone)
	}
	_ = svc.Flush(context.Background())
	var landing int
	var total, other int64
	for _, r := range repo.rows {
		if r.Dim == DimLanding {
			landing++
			total += r.Visits
			if r.K1 == KeyOther {
				other = r.Visits
			}
		}
	}
	if landing != maxKeysPerDim+1 || other != 50 || total != maxKeysPerDim+50 {
		t.Fatalf("landing rows=%d other=%d total=%d", landing, other, total)
	}
}

func TestFlushTrafficMergesBackOnFailure(t *testing.T) {
	repo := &fakeTrafficRepo{fail: true}
	svc := newTestService(&fakeRepo{})
	svc.SetTrafficRepo(repo)
	svc.RecordTraffic(Event{Entry: true, Device: "other"}, ChannelDirect, SourceNone)
	if err := svc.Flush(context.Background()); err == nil {
		t.Fatal("want flush error")
	}
	svc.RecordTraffic(Event{Entry: true, Device: "other"}, ChannelDirect, SourceNone)
	repo.fail = false
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rowMap(repo.rows)["channel|direct|"]; got.Visits != 2 || got.Pageviews != 2 {
		t.Fatalf("channel direct = %+v", got)
	}
}

func TestRecordTrafficNeverTouchesDBAndAllocatesNothing(t *testing.T) {
	r := touchedRepo{t}
	svc := NewService(r, nil)
	svc.SetTrafficRepo(r)
	ev := Event{Entry: true, UTMCampaign: "x", UTMMedium: "email", Path: "/a", Device: "mobile", Country: "DE"}
	svc.RecordTraffic(ev, ChannelEmail, "newsletter") // first call inserts the keys
	if n := testing.AllocsPerRun(100, func() { svc.RecordTraffic(ev, ChannelEmail, "newsletter") }); n > 0 {
		t.Fatalf("RecordTraffic allocates %v times per call", n)
	}
}

func TestEngagementDefaultsOn(t *testing.T) {
	svc := newTestService(&fakeRepo{})
	if !svc.EngagementEnabled() {
		t.Fatal("engagement should default to on")
	}
	svc.SetEngagement(false)
	if svc.EngagementEnabled() {
		t.Fatal("engagement should be off")
	}
}

func TestRecordTrafficCapHoldsAcrossFlushes(t *testing.T) {
	repo := &fakeTrafficRepo{}
	svc := newTestService(&fakeRepo{})
	svc.SetTrafficRepo(repo)
	record := func(from, to int) {
		for i := from; i < to; i++ {
			svc.RecordTraffic(Event{Entry: true, Path: fmt.Sprintf("/p%d", i), Device: "other"}, ChannelDirect, SourceNone)
		}
	}
	record(0, maxKeysPerDim)
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The second flush window sees 100 new keys and one admitted key again.
	record(maxKeysPerDim, maxKeysPerDim+100)
	record(0, 1)
	if err := svc.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	var other, total int64
	for _, r := range repo.rows {
		if r.Dim != DimLanding {
			continue
		}
		keys[r.K1] = true
		total += r.Visits
		if r.K1 == KeyOther {
			other += r.Visits
		}
	}
	if len(keys) != maxKeysPerDim+1 || other != 100 || total != maxKeysPerDim+101 {
		t.Fatalf("distinct landing keys=%d other=%d total=%d", len(keys), other, total)
	}
}

func TestRecordTrafficKeyAfterManyOthersKeepsOwnRow(t *testing.T) {
	repo := &fakeTrafficRepo{}
	svc := newTestService(&fakeRepo{})
	svc.SetTrafficRepo(repo)
	for i := 0; i < 300; i++ {
		svc.RecordTraffic(Event{Entry: true, Path: fmt.Sprintf("/p%d", i), Device: "other"}, ChannelDirect, SourceNone)
	}
	svc.RecordTraffic(Event{Entry: true, Path: "/late", Device: "other"}, ChannelDirect, SourceNone)
	_ = svc.Flush(context.Background())
	m := rowMap(repo.rows)
	if m["landing|/late|"].Visits != 1 {
		t.Fatalf("late key folded: %+v", m["landing|(other)|"])
	}
}
