//go:build integration

package changeset

import (
	"errors"
	"testing"
)

func TestReviewLimitsMatchEngine(t *testing.T) {
	if MaxStagedItems != maxStagedItems || MaxStagedBytes != maxStagedBytes {
		t.Errorf("exported limits %d/%d differ from the engine's %d/%d", MaxStagedItems, MaxStagedBytes, maxStagedItems, maxStagedBytes)
	}
}

func TestReviewCountsAndItems(t *testing.T) {
	f := newFixture(t)
	// A page that already exists, so an item updating it is an update.
	live := f.begin("")
	f.stage(live, f.entityItem("live-"+f.tag, "rv-live-"+f.tag, "old"))
	f.publish(live)

	cs := f.begin("")
	f.stage(cs, f.entityItem("new-"+f.tag, "rv-new-"+f.tag, "fresh"))
	f.stage(cs, f.entityItem("live-"+f.tag, "rv-live-"+f.tag, "changed"))
	f.stage(cs, f.redirectItem("-old", "/elsewhere"))
	f.stage(cs, f.redirectItem("-gone", "/rv-new-"+f.tag))
	f.stage(cs, f.entityItem("link-"+f.tag, "rv-link-"+f.tag, `<a href="https://evil.test/p">x</a>`))
	f.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, Payload: raw(map[string]any{"kind": "tag", "name": "T", "slug": f.tag + "-term"})})

	if _, err := f.eng.ReviewCounts(f.ctx, cs); !errors.Is(err, ErrNotClassified) {
		t.Fatalf("ReviewCounts before Classify err = %v, want ErrNotClassified", err)
	}
	if _, err := f.eng.ReviewItems(f.ctx, cs, nil, 10, 0); !errors.Is(err, ErrNotClassified) {
		t.Fatalf("ReviewItems before Classify err = %v, want ErrNotClassified", err)
	}
	if err := f.eng.Classify(f.ctx, cs); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.SetDecisions(f.ctx, cs, []int{3}, DecisionApproved); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.SetDecisions(f.ctx, cs, []int{5}, DecisionDropped); err != nil {
		t.Fatal(err)
	}

	c, err := f.eng.ReviewCounts(f.ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if c.Low != 3 || c.High != 3 || c.Critical != 0 || c.HighDecided != 2 || c.Dropped != 1 {
		t.Errorf("counts = %+v, want low 3 high 3 decided 2 dropped 1", c)
	}
	want := []KindCount{
		{Kind: KindEntity, New: 2, Updated: 1},
		{Kind: KindTerm, New: 1},
		{Kind: KindRedirect, New: 2},
	}
	if len(c.ByKind) != len(want) {
		t.Fatalf("ByKind = %+v", c.ByKind)
	}
	for i := range want {
		if c.ByKind[i] != want[i] {
			t.Errorf("ByKind[%d] = %+v, want %+v", i, c.ByKind[i], want[i])
		}
	}
	if c.PayloadBytes <= 0 {
		t.Errorf("PayloadBytes = %d", c.PayloadBytes)
	}

	high, err := f.eng.ReviewItems(f.ctx, cs, []string{"high"}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(high) != 3 {
		t.Fatalf("high items = %+v", high)
	}
	if h := high[0]; h.Seq != 3 || h.Reason != string(ReasonRedirect) || h.Decision != DecisionApproved || h.Old != "/"+f.tag+"-old" || h.New != "/elsewhere" {
		t.Errorf("redirect row = %+v", h)
	}
	if h := high[1]; h.Seq != 4 || h.Ref != 1 || h.New != "/rv-new-"+f.tag {
		t.Errorf("grouped redirect row = %+v", h)
	}
	if h := high[2]; h.Seq != 5 || h.Reason != string(ReasonLinkHost) || h.New != "evil.test/p" || h.Decision != DecisionDropped {
		t.Errorf("link row = %+v", h)
	}
	page, err := f.eng.ReviewItems(f.ctx, cs, nil, 2, 1)
	if err != nil || len(page) != 2 || page[0].Seq != 2 || page[1].Seq != 3 {
		t.Errorf("page = %+v, err %v", page, err)
	}
}

func TestStaleSeqs(t *testing.T) {
	f := newFixture(t)
	live := f.begin("")
	f.stage(live, f.entityItem("st-"+f.tag, "rv-st-"+f.tag, "old"))
	f.publish(live)
	var id string
	if err := f.db.Pool().QueryRow(f.ctx, `SELECT target_id FROM hatamoto_changeset_items WHERE changeset_id = $1`, live).Scan(&id); err != nil {
		t.Fatal(err)
	}

	cs := f.begin("")
	it := f.entityItem("st-"+f.tag, "rv-st-"+f.tag, "new")
	it.BaseToken = f.entityToken(id)
	f.stage(cs, it)
	if got, err := f.eng.StaleSeqs(f.ctx, cs); err != nil || len(got) != 0 {
		t.Fatalf("StaleSeqs before edit = %+v, %v", got, err)
	}
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET version = version + 1, updated_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	got, err := f.eng.StaleSeqs(f.ctx, cs)
	if err != nil || len(got) != 1 || got[0].Seq != 1 || got[0].Kind != KindEntity {
		t.Fatalf("StaleSeqs after edit = %+v, %v", got, err)
	}
	if _, err := f.eng.StaleSeqs(f.ctx, live); !errors.Is(err, ErrNotStaged) {
		t.Errorf("StaleSeqs of a published changeset err = %v", err)
	}
}

func TestHistoryListAndRevertPreview(t *testing.T) {
	f := newFixture(t)
	plugin := "rv" + f.tag
	first := f.begin(plugin)
	f.stage(first, f.entityItem("h1-"+f.tag, "rv-h1-"+f.tag, "a"))
	f.stage(first, f.entityItem("h2-"+f.tag, "rv-h2-"+f.tag, "b"))
	f.stage(first, f.entityItem("h3-"+f.tag, "rv-h3-"+f.tag, "c"))
	if err := f.eng.SetDecisions(f.ctx, first, []int{3}, DecisionDropped); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.PublishReviewed(f.ctx, first); err != nil {
		t.Fatal(err)
	}
	second := f.begin(plugin)
	f.stage(second, f.entityItem("h4-"+f.tag, "rv-h4-"+f.tag, "d"))
	t.Cleanup(func() { f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_runs WHERE plugin = $1`, plugin) })

	rows, err := f.eng.List(f.ctx, plugin, 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("List = %+v, %v", rows, err)
	}
	if rows[0].ID != second || rows[0].State != StateStaged || rows[0].PublishedAt != nil {
		t.Errorf("newest row = %+v", rows[0])
	}
	if r := rows[1]; r.ID != first || r.State != StatePublished || r.PublishedAt == nil || r.Applied != 2 || r.Dropped != 1 || r.Skipped != 0 || r.PayloadBytes <= 0 {
		t.Errorf("published row = %+v", r)
	}
	if rows, err := f.eng.List(f.ctx, plugin+"-none", 10, 0); err != nil || len(rows) != 0 {
		t.Errorf("List of another plugin = %+v, %v", rows, err)
	}

	moved, total, err := f.eng.RevertPreview(f.ctx, first)
	if err != nil || moved != 0 || total != 2 {
		t.Fatalf("RevertPreview = %d of %d, %v, want 0 of 2", moved, total, err)
	}
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_entities SET version = version + 1, updated_at = now() WHERE schema_id = $1 AND slug = $2`, f.schema.ID, "rv-h1-"+f.tag); err != nil {
		t.Fatal(err)
	}
	moved, total, err = f.eng.RevertPreview(f.ctx, first)
	if err != nil || moved != 1 || total != 2 {
		t.Fatalf("RevertPreview after edit = %d of %d, %v, want 1 of 2", moved, total, err)
	}
	if f.state(first) != StatePublished || f.entities() != 2 {
		t.Errorf("RevertPreview wrote: state %q entities %d", f.state(first), f.entities())
	}
	if _, _, err := f.eng.RevertPreview(f.ctx, second); !errors.Is(err, ErrNotPublished) {
		t.Errorf("RevertPreview of a staged changeset err = %v", err)
	}
}
