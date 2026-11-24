//go:build integration

package comment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPolicyInfo(t *testing.T) {
	f := newFixture(t)
	const sch = `UPDATE hatamoto_entity_schemas SET settings = $1::jsonb WHERE slug = 'cmm-note'`
	for _, c := range []struct {
		name, settings string
		want           bool
	}{
		{"absent", `{}`, false},
		{"on", `{"comments":{"policy":{"enabled":true}}}`, true},
		{"off", `{"comments":{"policy":{"enabled":false}}}`, false},
		{"string", `{"comments":{"policy":{"enabled":"true"}}}`, false},
		{"number", `{"comments":{"policy":{"enabled":1}}}`, false},
		{"policy not an object", `{"comments":{"policy":true}}`, false},
	} {
		f.exec(sch, c.settings)
		got, err := f.svc.PolicyInfo(f.ctx, f.entity)
		if err != nil || got.Enabled != c.want || got.Schema != "cmm-note" {
			t.Errorf("%s: %+v, %v", c.name, got, err)
		}
	}
	if _, err := f.svc.PolicyInfo(f.ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown entity = %v", err)
	}
}

func TestEnsureCommenterAndEmail(t *testing.T) {
	f := newFixture(t)
	a, err := f.svc.EnsureCommenter(f.ctx, "Reader@Example.org")
	if err != nil || a == uuid.Nil {
		t.Fatalf("ensure: %v, %v", a, err)
	}
	b, err := f.svc.EnsureCommenter(f.ctx, "reader@example.org")
	if err != nil || a != b {
		t.Fatalf("second ensure: %v, %v (want %v)", b, err, a)
	}
	f.enable()
	c := f.store(f.draft("reader@example.org", "hi"))
	if c.CommenterID == nil || *c.CommenterID != a {
		t.Errorf("Store commenter = %v, want %v", c.CommenterID, a)
	}
	cm, email, err := f.svc.CommenterEmail(f.ctx, a)
	if err != nil || cm.ID != a || email != "reader@example.org" {
		t.Errorf("CommenterEmail = %+v, %q, %v", cm, email, err)
	}
	if _, _, err := f.svc.CommenterEmail(f.ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown commenter = %v", err)
	}
}

func TestThreadCommenterID(t *testing.T) {
	f := newFixture(t)
	f.enable()
	now := time.Now().UTC()
	root := f.seedComment(nil, now.Add(-2*time.Hour), StatusApproved)
	reply := f.seedComment(&root, now.Add(-time.Hour), StatusApproved)
	cid, err := f.svc.EnsureCommenter(f.ctx, "reader@example.org")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE hatamoto_comments SET commenter_id = $1 WHERE id = $2`, cid, root.id)
	page, err := f.svc.Thread(f.ctx, f.entity, 1)
	if err != nil || len(page.Comments) != 2 {
		t.Fatalf("thread: %+v, %v", page, err)
	}
	got := map[uuid.UUID]*uuid.UUID{}
	for _, tc := range page.Comments {
		got[tc.ID] = tc.CommenterID
	}
	if got[root.id] == nil || *got[root.id] != cid {
		t.Errorf("commenter comment carries %v, want %v", got[root.id], cid)
	}
	if got[reply.id] != nil {
		t.Errorf("staff reply carries %v, want nil", got[reply.id])
	}
}

func TestLastAuthorName(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ('33333333-3333-3333-3333-333333333333', 'Other', 'cmm-other', '{"type":"object"}')`)
	other := uuid.New()
	f.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status) VALUES ($1, '33333333-3333-3333-3333-333333333333', 'o', 'o', 'published')`, other)
	second := f.newEntity("second")
	cid, err := f.svc.EnsureCommenter(f.ctx, "reader@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.LastAuthorName(f.ctx, cid, f.entity); err != nil || got != "" {
		t.Fatalf("no comment: %q, %v", got, err)
	}
	now := time.Now().UTC()
	put := func(entity uuid.UUID, name string, status Status, age time.Duration) {
		f.t.Helper()
		c := f.seedComment(nil, now.Add(-age), status)
		f.exec(`UPDATE hatamoto_comments SET commenter_id = $1, author_name = $2, entity_id = $3 WHERE id = $4`, cid, name, entity, c.id)
	}
	put(f.entity, "old_name", StatusApproved, 3*time.Hour)
	put(second, "new_name", StatusApproved, 2*time.Hour)
	put(other, "other_schema", StatusApproved, time.Hour)
	put(f.entity, "", StatusSpam, 30*time.Minute) // purged
	put(f.entity, "trashed", StatusTrash, 10*time.Minute)
	if got, err := f.svc.LastAuthorName(f.ctx, cid, f.entity); err != nil || got != "new_name" {
		t.Errorf("same schema = %q, %v, want new_name", got, err)
	}
	if got, _ := f.svc.LastAuthorName(f.ctx, cid, other); got != "other_schema" {
		t.Errorf("other schema = %q", got)
	}
}
