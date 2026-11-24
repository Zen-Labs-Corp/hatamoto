//go:build integration

package comment

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// commenter inserts a commenter row and returns its id.
func (f *fixture) commenter(tag string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	f.scalar(&id, `INSERT INTO hatamoto_commenters (email_hash, email_sealed) VALUES ('h-'||$1::text, '\x00') RETURNING id`, tag)
	return id
}

// seedBy is seedComment on entity ent, written by commenter who (may be nil).
func (f *fixture) seedBy(ent uuid.UUID, parent *seeded, who *uuid.UUID, created time.Time, status Status) seeded {
	f.t.Helper()
	parentPath, depth := "", 1
	var parentID *uuid.UUID
	if parent != nil {
		parentPath, depth, parentID = parent.path, Depth(parent.path)+1, &parent.id
	}
	var s seeded
	f.scalar(&s.id, `WITH s AS (SELECT nextval('hatamoto_comments_seq') AS v)
		INSERT INTO hatamoto_comments (seq, entity_id, parent_id, path, depth, status, body_raw, body_html, created_at, updated_at, commenter_id)
		SELECT v, $1, $2, CASE WHEN $3::text = '' THEN '' ELSE $3::text || '.' END || lpad(to_hex(v), 12, '0'), $4, $5, 'b', '<p>b</p>', $6, $6, $7 FROM s
		RETURNING id`, ent, parentID, parentPath, depth, string(status), created, who)
	f.scalar(&s.path, `SELECT path FROM hatamoto_comments WHERE id = $1`, s.id)
	return s
}

// ownedEntity creates a published entity on a new content type owned by origin.
func (f *fixture) ownedEntity(slug, origin string) uuid.UUID {
	f.t.Helper()
	sid, id := uuid.New(), uuid.New()
	f.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema, origin_plugin) VALUES ($1, $2, $2, '{"type":"object"}', $3)`, sid, slug, origin)
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, $2, $3, $3, 'published')`, id, sid, slug+"-e")
	return id
}

func wantIDs(t *testing.T, step string, got []ThreadComment, want ...seeded) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d comments, want %d", step, len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].id {
			t.Fatalf("%s: item %d = %s, want %s", step, i, got[i].ID, want[i].id)
		}
	}
}

func TestByAuthorPublicApprovedNewestFirst(t *testing.T) {
	f := newFixture(t)
	f.enable()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	a, b := f.commenter("a"), f.commenter("b")

	draftEnt := f.newEntity("draft-e")
	f.exec(`UPDATE hatamoto_entities SET status = 'draft' WHERE id = $1`, draftEnt)
	hiddenEnt := f.newEntity("hidden-e")
	f.exec(`INSERT INTO hatamoto_comment_entities (entity_id, mode) VALUES ($1, 'hidden')`, hiddenEnt)
	personEnt := uuid.New() // the built-in person type: public only with attributes.public
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status)
		SELECT $1, id, 'p', 'p', 'published' FROM hatamoto_entity_schemas WHERE slug = 'person'`, personEnt)

	first := f.seedBy(f.entity, nil, &a, at(1), StatusApproved)
	tie1 := f.seedBy(f.entity, nil, &a, at(5), StatusApproved)
	tie2 := f.seedBy(f.entity, nil, &a, at(5), StatusApproved) // same instant, higher seq: first
	last := f.seedBy(f.entity, nil, &a, at(9), StatusApproved)
	for _, st := range []Status{StatusPending, StatusSpam, StatusTrash, StatusDead} {
		f.seedBy(f.entity, nil, &a, at(20), st)
	}
	f.seedBy(draftEnt, nil, &a, at(21), StatusApproved)
	f.seedBy(hiddenEnt, nil, &a, at(22), StatusApproved)
	f.seedBy(personEnt, nil, &a, at(23), StatusApproved)
	f.seedBy(f.entity, nil, &b, at(24), StatusApproved)
	f.seedBy(f.entity, nil, nil, at(25), StatusApproved)

	tp, err := f.svc.ByAuthor(f.ctx, a, 1, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "A", tp.Comments, last, tie2, tie1, first)
	if tp.HasMore || tp.Page != 1 {
		t.Errorf("page = %+v", tp)
	}
	for _, c := range tp.Comments {
		if c.Status != StatusApproved || c.CommenterID == nil || *c.CommenterID != a {
			t.Errorf("unexpected comment %+v", c)
		}
	}
}

func TestByAuthorPaging(t *testing.T) {
	f := newFixture(t)
	f.enable()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := f.commenter("a")
	var all []seeded
	for i := 0; i < 51; i++ {
		all = append(all, f.seedBy(f.entity, nil, &a, base.Add(time.Duration(i)*time.Minute), StatusApproved))
	}
	p1, err := f.svc.ByAuthor(f.ctx, a, 1, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Comments) != 50 || !p1.HasMore || p1.Comments[0].ID != all[50].id {
		t.Fatalf("page 1: %d comments, has_more %v", len(p1.Comments), p1.HasMore)
	}
	p2, err := f.svc.ByAuthor(f.ctx, a, 2, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Comments) != 1 || p2.HasMore || p2.Comments[0].ID != all[0].id {
		t.Fatalf("page 2: %d comments, has_more %v", len(p2.Comments), p2.HasMore)
	}
	f.exec(`DELETE FROM hatamoto_comments WHERE id = $1`, all[50].id)
	p1, err = f.svc.ByAuthor(f.ctx, a, 1, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Comments) != 50 || p1.HasMore {
		t.Fatalf("exactly 50: %d comments, has_more %v", len(p1.Comments), p1.HasMore)
	}
}

func TestByAuthorSiteOff(t *testing.T) {
	f := newFixture(t)
	a := f.commenter("a")
	f.seedBy(f.entity, nil, &a, time.Now(), StatusApproved)
	tp, err := f.svc.ByAuthor(f.ctx, a, 1, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	if tp.Comments == nil || len(tp.Comments) != 0 {
		t.Fatalf("comments off: %+v", tp)
	}
}

func TestByAuthorOneQuery(t *testing.T) {
	f := newFixture(t)
	f.enable()
	a := f.commenter("a")
	f.seedBy(f.entity, nil, &a, time.Now(), StatusApproved)
	if _, err := f.svc.Settings(f.ctx); err != nil {
		t.Fatal(err)
	}
	n := f.counter.Count()
	if _, err := f.svc.ByAuthor(f.ctx, a, 1, DeadScope{}); err != nil {
		t.Fatal(err)
	}
	if got := f.counter.Count() - n; got != 1 {
		t.Fatalf("ByAuthor ran %d statements, want 1: %v", got, f.counter.Since(n))
	}
}

func TestByAuthorDeadScope(t *testing.T) {
	f := newFixture(t)
	f.enable()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	a := f.commenter("a")
	owned := f.ownedEntity("note-type", "example")
	other := f.ownedEntity("page-type", "other")
	okA := f.seedBy(owned, nil, &a, at(1), StatusApproved)
	deadOwned := f.seedBy(owned, nil, &a, at(2), StatusDead)
	okB := f.seedBy(other, nil, &a, at(3), StatusApproved)
	f.seedBy(other, nil, &a, at(4), StatusDead)

	for _, tc := range []struct {
		name  string
		scope DeadScope
		want  []seeded
	}{
		{"none", DeadScope{}, []seeded{okB, okA}},
		{"owner", DeadScope{"example", "note-type"}, []seeded{okB, deadOwned, okA}},
		{"wrong schema", DeadScope{"example", "page-type"}, []seeded{okB, okA}},
		{"wrong plugin", DeadScope{"other2", "note-type"}, []seeded{okB, okA}},
		{"no plugin", DeadScope{"", "note-type"}, []seeded{okB, okA}},
		{"no schema", DeadScope{"example", ""}, []seeded{okB, okA}},
	} {
		tp, err := f.svc.ByAuthor(f.ctx, a, 1, tc.scope)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, tc.name, tp.Comments, tc.want...)
		for _, c := range tp.Comments {
			if c.Status == StatusDead && (c.BodyHTML == "" || c.ID != deadOwned.id) {
				t.Errorf("%s: dead row %+v", tc.name, c)
			}
		}
	}

	// paging stays exact with a dead row in scope: 50 approved + 1 dead = 51 rows
	for i := 0; i < 48; i++ {
		f.seedBy(owned, nil, &a, at(10+i), StatusApproved)
	}
	p1, err := f.svc.ByAuthor(f.ctx, a, 1, DeadScope{"example", "note-type"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Comments) != 50 || !p1.HasMore {
		t.Fatalf("page 1: %d comments, has_more %v", len(p1.Comments), p1.HasMore)
	}
	p1, err = f.svc.ByAuthor(f.ctx, a, 1, DeadScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Comments) != 50 || p1.HasMore {
		t.Fatalf("without dead: %d comments, has_more %v", len(p1.Comments), p1.HasMore)
	}
}

func TestByAuthorUsesCommenterIndex(t *testing.T) {
	f := newFixture(t)
	f.enable()
	f.exec(`INSERT INTO hatamoto_commenters (email_hash, email_sealed) SELECT 'bulk-' || g, '\x00' FROM generate_series(1, 100) g`)
	f.exec(`WITH n AS (SELECT g, nextval('hatamoto_comments_seq') AS v FROM generate_series(1, 10000) g)
		INSERT INTO hatamoto_comments (seq, entity_id, path, depth, status, body_raw, body_html, created_at, updated_at, commenter_id)
		SELECT n.v, $1, lpad(to_hex(n.v), 12, '0'), 1, 'approved', 'b', '<p>b</p>', now() - n.g * interval '1 minute', now(), c.id
		FROM n JOIN hatamoto_commenters c ON c.email_hash = 'bulk-' || (n.g % 100 + 1)`, f.entity)
	f.exec(`ANALYZE hatamoto_comments`)
	var who uuid.UUID
	f.scalar(&who, `SELECT id FROM hatamoto_commenters WHERE email_hash = 'bulk-7'`)

	conn, err := f.db.Pool().Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, scope := range []DeadScope{{}, {"example", "note-type"}} {
		rows, err := conn.Query(f.ctx, `EXPLAIN `+byAuthorSQL, who, AuthorPageSize+1, 0, nil, scope.Plugin, scope.Schema)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		p := strings.Join(plan, "\n")
		if !strings.Contains(p, "hatamoto_comments_commenter_created_idx") || strings.Contains(p, "Seq Scan on hatamoto_comments") {
			t.Errorf("scope %+v does not use the commenter index:\n%s", scope, p)
		}
	}
}

func TestCommentersSince(t *testing.T) {
	f := newFixture(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var made []uuid.UUID
	for i, tag := range []string{"x", "y", "z"} {
		id := f.commenter(tag)
		f.exec(`UPDATE hatamoto_commenters SET created_at = $2 WHERE id = $1`, id, base.Add(time.Duration(i)*time.Hour))
		made = append(made, id)
	}
	all, err := f.svc.CommentersSince(f.ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != made[0] || all[1].ID != made[1] || all[2].ID != made[2] {
		t.Fatalf("all = %+v", all)
	}
	late, err := f.svc.CommentersSince(f.ctx, all[1].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(late) != 1 || late[0].ID != made[2] {
		t.Fatalf("since second = %+v", late)
	}
}
