//go:build integration

package changeset

import (
	"testing"
)

func (f *fixture) parentOf(slug string) (parent *string) {
	f.t.Helper()
	if err := f.db.Pool().QueryRow(f.ctx,
		`SELECT parent_id::text FROM hatamoto_entities WHERE schema_id = $1 AND slug = $2`, f.schema.ID, slug).Scan(&parent); err != nil {
		f.t.Fatal(err)
	}
	return parent
}

func (f *fixture) entityID(slug string) string {
	f.t.Helper()
	var id string
	if err := f.db.Pool().QueryRow(f.ctx,
		`SELECT id::text FROM hatamoto_entities WHERE schema_id = $1 AND slug = $2`, f.schema.ID, slug).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) childItem(key, slug, parentKey string) Item {
	it := f.entityItem(key, slug, "c")
	it.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": "N " + slug, "slug": slug, "parent_key": parentKey,
		"attributes": map[string]any{"content": "c"}})
	return it
}

// A parent and its children staged in one changeset publish with the
// children under the parent: parent_key resolves against the items published
// earlier in the same transaction.
func TestParentKeyInOneChangeset(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("alpha")
	p, c, g := f.tag+"-p", f.tag+"-c", f.tag+"-g"
	f.stage(cs, f.entityItem(f.tag+"-pk", p, "p"))
	f.stage(cs, f.childItem(f.tag+"-ck", c, f.tag+"-pk"))
	f.stage(cs, f.childItem(f.tag+"-gk", g, f.tag+"-ck"))
	res := f.publish(cs)
	if res.Applied != 3 || res.Skipped != 0 {
		t.Fatalf("result %+v", res)
	}
	if got := f.parentOf(c); got == nil || *got != f.entityID(p) {
		t.Fatalf("child parent = %v, want %s", got, f.entityID(p))
	}
	if got := f.parentOf(g); got == nil || *got != f.entityID(c) {
		t.Fatalf("grandchild parent = %v, want %s", got, f.entityID(c))
	}

	// A later changeset resolves a parent a previous one published.
	cs2 := f.begin("alpha")
	d := f.tag + "-d"
	f.stage(cs2, f.childItem(f.tag+"-dk", d, f.tag+"-pk"))
	f.publish(cs2)
	if got := f.parentOf(d); got == nil || *got != f.entityID(p) {
		t.Fatalf("later child parent = %v, want %s", got, f.entityID(p))
	}
}

// A child whose parent is neither mapped nor published earlier is skipped,
// not created at the top level.
func TestParentKeyMissingSkipsChild(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("alpha")
	f.stage(cs, f.childItem(f.tag+"-lost", f.tag+"-lost", f.tag+"-nobody"))
	res := f.publish(cs)
	if res.Applied != 0 || res.Skipped != 1 || res.Conflicts[0].Reason != reasonParentMissing {
		t.Fatalf("result %+v", res)
	}
	if f.entities() != 0 {
		t.Fatal("a child with a missing parent was created")
	}
}

func TestResolveAndHasStaged(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("alpha")
	key := f.tag + "-rk"
	f.stage(cs, f.entityItem(key, f.tag+"-r", "x"))
	if ok, err := f.eng.HasStaged(f.ctx, cs, KindEntity, key); err != nil || !ok {
		t.Fatalf("HasStaged = %v %v", ok, err)
	}
	if ok, _ := f.eng.HasStaged(f.ctx, cs, KindTerm, key); ok {
		t.Fatal("HasStaged ignored the kind")
	}
	if id, ok, err := f.eng.Resolve(f.ctx, "alpha", KindEntity, key); err != nil || ok || id != "" {
		t.Fatalf("Resolve before publish = %q %v %v", id, ok, err)
	}
	f.publish(cs)
	id, ok, err := f.eng.Resolve(f.ctx, "alpha", KindEntity, key)
	if err != nil || !ok || id != f.entityID(f.tag+"-r") {
		t.Fatalf("Resolve = %q %v %v", id, ok, err)
	}
	if _, ok, _ := f.eng.Resolve(f.ctx, "beta", KindEntity, key); ok {
		t.Fatal("Resolve crossed plugins")
	}
}

// seo lands in the attributes the public site reads.
func TestEntitySEOPayload(t *testing.T) {
	f := newFixture(t)
	cs := f.begin("alpha")
	slug := f.tag + "-seo"
	it := f.entityItem(f.tag+"-seok", slug, "x")
	it.Payload = raw(map[string]any{"schema": f.schema.Slug, "name": "N", "slug": slug, "attributes": map[string]any{"content": "x"},
		"seo": map[string]any{"meta_title": "T", "meta_description": "D", "canonical": "https://example.com/x", "robots": "noindex"}})
	f.stage(cs, it)
	f.publish(cs)
	var title, desc, canon, robots string
	if err := f.db.Pool().QueryRow(f.ctx, `
		SELECT attributes->>'meta_title', attributes->>'meta_description', attributes->>'canonical_url', attributes->'seo'->>'robots'
		FROM hatamoto_entities WHERE schema_id = $1 AND slug = $2`, f.schema.ID, slug).Scan(&title, &desc, &canon, &robots); err != nil {
		t.Fatal(err)
	}
	if title != "T" || desc != "D" || canon != "https://example.com/x" || robots != "noindex" {
		t.Fatalf("seo attributes: %q %q %q %q", title, desc, canon, robots)
	}
}
