//go:build integration

package comment

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

func (f *fixture) resync() {
	f.t.Helper()
	if err := f.db.WithTx(f.ctx, func(tx pgx.Tx) error { return Resync(f.ctx, tx) }); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) storeApproved(body string) int64 {
	f.t.Helper()
	d := f.draft("", body)
	d.Status = StatusApproved
	c := f.store(d)
	var seq int64
	f.scalar(&seq, `SELECT seq FROM hatamoto_comments WHERE id = $1`, c.ID)
	return seq
}

func TestResyncAfterBulkLoad(t *testing.T) {
	f := newFixture(t)
	p1 := Segment(500)
	f.exec(`INSERT INTO hatamoto_comments (seq, entity_id, path, depth, status, body_raw, body_html)
		VALUES (500, $1, $2, 1, 'approved', 'a', '<p>a</p>'), (501, $1, $3, 2, 'pending', 'b', '<p>b</p>')`,
		f.entity, p1, p1+"."+Segment(501))
	f.exec(`INSERT INTO hatamoto_comment_entities (entity_id, comment_count, pending_count) VALUES ($1, 7, 9)`, f.entity)

	f.resync()
	f.wantCounts(1, 1, "after resync")

	if seq := f.storeApproved("c"); seq <= 501 {
		t.Errorf("next seq = %d, want > 501", seq)
	}
}

func TestResyncEmpty(t *testing.T) {
	f := newFixture(t)
	f.resync()
	var n int
	f.scalar(&n, `SELECT count(*) FROM hatamoto_comment_entities`)
	if n != 0 {
		t.Errorf("counts rows = %d, want 0", n)
	}
	var path string
	f.scalar(&path, `SELECT path FROM hatamoto_comments WHERE seq = $1`, f.storeApproved("a"))
	if path != Segment(1) {
		t.Errorf("path = %q, want %q", path, Segment(1))
	}
}

func TestResyncKeepsMode(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO hatamoto_comments (seq, entity_id, path, depth, status, body_raw, body_html)
		VALUES (5, $1, $2, 1, 'approved', 'a', '<p>a</p>')`, f.entity, Segment(5))
	f.exec(`INSERT INTO hatamoto_comment_entities (entity_id, mode) VALUES ($1, 'closed')`, f.entity)
	f.resync()
	var mode string
	f.scalar(&mode, `SELECT mode FROM hatamoto_comment_entities WHERE entity_id = $1`, f.entity)
	if mode != "closed" {
		t.Errorf("mode = %q, want closed", mode)
	}
	f.wantCounts(1, 0, "after resync")
}
