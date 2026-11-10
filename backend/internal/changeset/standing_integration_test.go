//go:build integration

package changeset

import (
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

const standingPlugin = "pricer"

// standingFixture is a fixture with a schema that has typed fields, and a live
// entity the plugin created.
type standingFixture struct {
	*fixture
	slug   string // schema slug
	target string // id of the plugin's entity
	policy StandingPolicy
}

func newStandingFixture(t *testing.T) *standingFixture {
	t.Helper()
	f := newFixture(t)
	// The plugin name is shared: forget automatic publishes of earlier tests
	// so the hourly count starts at zero.
	if _, err := f.db.Pool().Exec(f.ctx, `UPDATE hatamoto_changesets SET auto_published = false WHERE plugin = $1`, standingPlugin); err != nil {
		t.Fatal(err)
	}
	slug := "pr-" + f.tag
	sc, err := entity.NewPostgresSchemaRepository(f.db).Create(f.ctx, &entity.CreateSchemaInput{Name: "pr " + f.tag, Slug: slug,
		Schema: jsontext.Value(`{"type":"object","properties":{
			"price":{"type":"number"},"stock":{"type":"integer"},"active":{"type":"boolean"},
			"on":{"type":"string","format":"date"},"tier":{"type":"string","enum":["a","b"]},
			"title":{"type":"string"},"nested":{"type":"object"},"content":{"type":"string"},
			"photo":{"type":"string","format":"uri","x-media":true},"featured_image":{"type":"string","format":"uri"},
			"gallery":{"type":"array","items":{"type":"string","x-hm-field":"image"}},
			"images":{"type":"array","items":{"type":"string","format":"uri"}},
			"banner":{"type":"object","properties":{"image":{"type":"string","format":"uri"}}},
			"hero_image":{"type":"string","x-hm-field":"url"},
			"og_image":{"type":"string"},"image":{"type":"string"},
			"caption":{"type":"string"},
			"cover_image":{"type":"string","format":"uri","x-hm-alias":["caption"]}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_, _ = f.db.Pool().Exec(f.ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, sc.ID)
	})
	s := &standingFixture{fixture: f, slug: slug,
		policy: StandingPolicy{Plugin: standingPlugin, Fields: []string{"price", "stock", "active", "on", "tier", "title", "nested"}, Schemas: []string{slug}}}
	cs := f.begin(standingPlugin)
	f.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + f.tag, Payload: s.payload("s-"+f.tag, s.attrs())})
	f.publish(cs)
	id, ok, err := f.eng.Resolve(f.ctx, standingPlugin, KindEntity, "k-"+f.tag)
	if err != nil || !ok {
		t.Fatalf("resolve: %v %v", ok, err)
	}
	s.target = id
	return s
}

func (s *standingFixture) attrs() map[string]any {
	return map[string]any{"price": 10.5, "stock": 3, "active": true, "on": "2030-01-01", "tier": "a", "title": "Hello", "nested": map[string]any{"x": 1}}
}

func (s *standingFixture) payload(slug string, attrs map[string]any) jsontext.Value {
	return raw(map[string]any{"schema": s.slug, "name": "N", "slug": slug, "attributes": attrs})
}

// update stages one update of the plugin's entity into a new changeset and
// returns it.
func (s *standingFixture) update(mutate func(a map[string]any)) int64 {
	a := s.attrs()
	mutate(a)
	cs := s.begin(standingPlugin)
	s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: s.payload("s-"+s.tag, a)})
	return cs
}

func (s *standingFixture) verdict(cs int64) PolicyVerdict {
	s.t.Helper()
	v, err := s.eng.CheckPolicy(s.ctx, cs, s.policy)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s *standingFixture) expires(cs int64) time.Time {
	var at time.Time
	if err := s.db.Pool().QueryRow(s.ctx, `SELECT expires_at FROM hatamoto_changesets WHERE id = $1`, cs).Scan(&at); err != nil {
		s.t.Fatal(err)
	}
	return at
}

func TestStandingApproval(t *testing.T) {
	s := newStandingFixture(t)
	cases := []struct {
		name  string
		build func() int64
		ok    bool
		why   string
	}{
		{"inside policy", func() int64 {
			return s.update(func(a map[string]any) {
				a["price"] = 11.0
				a["stock"] = 2
				a["active"] = false
				a["on"] = "2030-02-02"
				a["tier"] = "b"
			})
		}, true, ""},
		{"text field", func() int64 { return s.update(func(a map[string]any) { a["title"] = "Changed" }) }, false, `"title"`},
		{"link in text field", func() int64 {
			return s.update(func(a map[string]any) { a["title"] = "see https://evil.example/x" })
		}, false, `"title"`},
		{"field outside policy", func() int64 {
			s.policy.Fields = []string{"stock"}
			return s.update(func(a map[string]any) { a["price"] = 1.0 })
		}, false, `"price"`},
		{"enum value not allowed", func() int64 { return s.update(func(a map[string]any) { a["tier"] = "z" }) }, false, `"tier"`},
		{"number sent as text", func() int64 { return s.update(func(a map[string]any) { a["price"] = "9" }) }, false, `"price"`},
		{"nested value", func() int64 { return s.update(func(a map[string]any) { a["nested"] = map[string]any{"x": 2} }) }, false, `"nested"`},
		{"removed key", func() int64 { return s.update(func(a map[string]any) { delete(a, "price") }) }, false, `"price"`},
		{"slug", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: s.payload("moved-"+s.tag, s.attrs())})
			return cs
		}, false, "closer look"},
		{"omitted slug", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: raw(map[string]any{"schema": s.slug, "name": "N", "attributes": s.attrs()})})
			return cs
		}, false, "closer look"},
		{"seo", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: raw(map[string]any{"schema": s.slug, "name": "N", "slug": "s-" + s.tag, "attributes": s.attrs(), "seo": map[string]any{"meta_title": "x"}})})
			return cs
		}, false, "search"},
		{"foreign origin", func() int64 {
			// Staged while the plugin owned the entity; the owner took it over since.
			cs := s.update(func(a map[string]any) { a["price"] = 1.0 })
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET origin = 'owner' WHERE id = $1`, s.target); err != nil {
				t.Fatal(err)
			}
			return cs
		}, false, "did not create"},
		{"schema not named", func() int64 {
			s.policy.Schemas = []string{"other"}
			return s.update(func(a map[string]any) { a["price"] = 1.0 })
		}, false, "not covered"},
		{"term kind", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindTerm, Op: OpUpsert, SourceKey: s.tag + "t", Payload: raw(map[string]any{"name": s.tag + "t", "slug": s.tag + "t", "taxonomy": "tag"})})
			return cs
		}, false, "only page"},
		{"create", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "new-" + s.tag, Payload: s.payload("new-"+s.tag, s.attrs())})
			return cs
		}, false, "creating"},
		{"delete", func() int64 {
			cs := s.begin(standingPlugin)
			s.stage(cs, Item{Kind: KindEntity, Op: OpDelete, TargetID: s.target})
			return cs
		}, false, "closer look"},
		{"proposal present", func() int64 {
			cs := s.update(func(a map[string]any) { a["price"] = 12.0 })
			if _, err := s.db.Pool().Exec(s.ctx, `INSERT INTO hatamoto_run_proposals (run_id, kind, payload) SELECT run_id, 'settings', '{}' FROM hatamoto_changesets WHERE id = $1`, cs); err != nil {
				t.Fatal(err)
			}
			return cs
		}, false, "suggests"},
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			defer func(p StandingPolicy) { s.policy = p }(s.policy)
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET attributes = $2, origin = $3 WHERE id = $1`,
				s.target, raw(s.attrs()), Origin(standingPlugin)); err != nil {
				t.Fatal(err)
			}
			cs := c.build()
			v := s.verdict(cs)
			if v.OK != c.ok {
				t.Fatalf("verdict = %+v, want ok=%v", v, c.ok)
			}
			if !c.ok && !strings.Contains(strings.Join(v.Reasons, "|"), c.why) {
				t.Errorf("reasons %q lack %q", v.Reasons, c.why)
			}
			if st := s.state(cs); st != StateStaged {
				t.Errorf("check changed the state to %s", st)
			}
			if c.ok {
				if v, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 7); err != nil || !v.OK {
					t.Fatalf("publish: %+v %v", v, err)
				}
				if n, _ := s.eng.AutoPublishedLastHour(s.ctx, standingPlugin); n != 1 {
					t.Errorf("automatic last hour = %d, want 1", n)
				}
			}
		})
	}
}

func TestStandingApprovalHourlyCap(t *testing.T) {
	s := newStandingFixture(t)
	s.policy.MaxPerHour = 2
	first := s.update(func(a map[string]any) { a["price"] = 1.0 })
	if v := s.verdict(first); !v.OK {
		t.Fatalf("first: %+v", v)
	}
	if v, _, err := s.eng.PublishUnderPolicy(s.ctx, first, s.policy, 1); err != nil || !v.OK {
		t.Fatalf("publish: %+v %v", v, err)
	}
	// One more item fits (1+1 <= 2), two do not (1+2 > 2).
	two := s.begin(standingPlugin)
	for i := 0; i < 2; i++ {
		s.stage(two, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, TargetID: s.target, Payload: s.payload("s-"+s.tag, func() map[string]any { a := s.attrs(); a["price"] = float64(i + 5); return a }())})
	}
	if v := s.verdict(two); v.OK || !strings.Contains(strings.Join(v.Reasons, ""), "automatic changes in an hour") {
		t.Fatalf("over the cap: %+v", v)
	}
	// An hour later the window is empty again.
	s.now = s.now.Add(61 * time.Minute)
	s.policy.MaxPerHour = 0
	if n, _ := s.eng.AutoPublishedLastHour(s.ctx, standingPlugin); n != 0 {
		t.Errorf("after an hour = %d, want 0", n)
	}
	if c := (StandingPolicy{MaxPerHour: 500}).cap(); c != MaxStandingPerHour {
		t.Errorf("cap ceiling = %d", c)
	}
}

func TestOutsidePolicyRunStaysStagedAndExpires(t *testing.T) {
	s := newStandingFixture(t)
	cs := s.update(func(a map[string]any) { a["title"] = "Changed" })
	before := s.expires(cs)
	if v := s.verdict(cs); v.OK {
		t.Fatalf("verdict = %+v", v)
	}
	if st := s.state(cs); st != StateStaged || !s.expires(cs).Equal(before) {
		t.Fatalf("refused run: state %s, expiry moved %v -> %v", st, before, s.expires(cs))
	}
	s.now = s.now.Add(TTL + time.Hour)
	if _, err := s.eng.Expire(s.ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.state(cs); st != StateExpired {
		t.Errorf("after sweep state = %s, want expired", st)
	}
}

// twoItems stages a changeset of two updates of the plugin's entity.
func (s *standingFixture) twoItems(price float64) int64 {
	cs := s.begin(standingPlugin)
	for i := 0; i < 2; i++ {
		a := s.attrs()
		a["price"] = price + float64(i)
		s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, TargetID: s.target, Payload: s.payload("s-"+s.tag, a)})
	}
	return cs
}

// The publish sets the automatic marker in its own transaction: a published
// changeset is never left looking manual.
func TestPublishUnderPolicyMarksAutomaticAtomically(t *testing.T) {
	s := newStandingFixture(t)
	cs := s.update(func(a map[string]any) { a["price"] = 99.0 })
	v, res, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 42)
	if err != nil || !v.OK || res.Applied != 1 {
		t.Fatalf("publish: %+v %+v %v", v, res, err)
	}
	var auto bool
	var approval *int64
	if err := s.db.Pool().QueryRow(s.ctx, `SELECT auto_published, approval_id FROM hatamoto_changesets WHERE id = $1 AND state = 'published'`, cs).Scan(&auto, &approval); err != nil {
		t.Fatal(err)
	}
	if !auto || approval == nil || *approval != 42 {
		t.Fatalf("marker = %v %v", auto, approval)
	}
	// A refusal publishes nothing and marks nothing.
	bad := s.update(func(a map[string]any) { a["title"] = "Changed" })
	if v, _, err := s.eng.PublishUnderPolicy(s.ctx, bad, s.policy, 42); err != nil || v.OK {
		t.Fatalf("outside the rule: %+v %v", v, err)
	}
	if st := s.state(bad); st != StateStaged {
		t.Fatalf("refused changeset is %s", st)
	}
}

// Two runs of one plugin that finish together cannot both pass a cap only one
// fits: the count and the publish happen under a per-plugin lock.
func TestStandingCapHoldsAcrossConcurrentGates(t *testing.T) {
	s := newStandingFixture(t)
	s.policy.MaxPerHour = 2
	a, b := s.twoItems(1), s.twoItems(5)
	var wg sync.WaitGroup
	oks := make([]bool, 2)
	for i, cs := range []int64{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 1)
			if err != nil {
				t.Error(err)
			}
			oks[i] = v.OK
		}()
	}
	wg.Wait()
	if oks[0] == oks[1] {
		t.Fatalf("both or neither published: %v", oks)
	}
	if n, _ := s.eng.AutoPublishedLastHour(s.ctx, standingPlugin); n != 2 {
		t.Fatalf("automatic last hour = %d, want 2", n)
	}
}

// The gate waits for the plugin's lock before it counts: while another
// transaction holds it, the publish does not start.
func TestPublishUnderPolicyWaitsForThePluginLock(t *testing.T) {
	s := newStandingFixture(t)
	cs := s.update(func(a map[string]any) { a["price"] = 7.0 })
	tx, err := s.db.Pool().Begin(s.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(s.ctx)
	if _, err := tx.Exec(s.ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "standing:"+standingPlugin); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 1)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("published while the plugin lock was held (err %v)", err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := tx.Commit(s.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if st := s.state(cs); st != StatePublished {
		t.Fatalf("state %s", st)
	}
}

// withCreate lets the policy create entries of the fixture schema.
func (s *standingFixture) withCreate(max int, extra ...string) {
	s.policy.Create = &CreatePolicy{Schemas: append([]string{s.slug}, extra...), MaxPerHour: max}
}

// newItem is a clean create of the fixture schema: no target, a fresh source
// key and slug. extra overrides top-level payload keys; "attributes" replaces
// the attributes.
func (s *standingFixture) newItem(key string, extra map[string]any) Item {
	attrs := map[string]any{"title": "T", "price": 1.0}
	p := map[string]any{"schema": s.slug, "name": "New " + key, "slug": key + "-" + s.tag, "attributes": attrs}
	for k, v := range extra {
		p[k] = v
	}
	return Item{Kind: KindEntity, Op: OpUpsert, SourceKey: key + "-" + s.tag, Payload: raw(p)}
}

func (s *standingFixture) stageAll(items ...Item) int64 {
	cs := s.begin(standingPlugin)
	for _, it := range items {
		s.stage(cs, it)
	}
	return cs
}

// siteHost makes a host the site's own (published entries link to it) and
// returns it, so an address on it is not an external link to the gate.
func (s *standingFixture) siteHost() string {
	s.t.Helper()
	host := "site-" + s.tag + ".test"
	a := s.attrs()
	a["cta"] = "https://" + host + "/"
	if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET attributes = $2, status = 'published' WHERE id = $1`, s.target, raw(a)); err != nil {
		s.t.Fatal(err)
	}
	return host
}

// mediaRow adds a library file with the given public URL and returns its id.
func (s *standingFixture) mediaRow(publicURL string) string {
	s.t.Helper()
	var id string
	if err := s.db.Pool().QueryRow(s.ctx, `
		INSERT INTO hatamoto_media (filename, original_filename, mime_type, file_size, storage_path, public_url)
		VALUES ('f.jpg', 'f.jpg', 'image/jpeg', 1, $1, $2) RETURNING id::text`, "k/"+s.tag+publicURL[len(publicURL)-6:], publicURL).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _, _ = s.db.Pool().Exec(s.ctx, `DELETE FROM hatamoto_media WHERE id = $1`, id) })
	return id
}

// otherSchema makes a second schema of the same shape and returns its slug.
func (s *standingFixture) otherSchema() string {
	s.t.Helper()
	slug := "ot-" + s.tag
	sc, err := entity.NewPostgresSchemaRepository(s.db).Create(s.ctx, &entity.CreateSchemaInput{Name: "ot " + s.tag, Slug: slug,
		Schema: jsontext.Value(`{"type":"object","properties":{"title":{"type":"string"},"price":{"type":"number"}}}`)})
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		_, _ = s.db.Pool().Exec(s.ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, sc.ID)
		_, _ = s.db.Pool().Exec(s.ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, sc.ID)
	})
	return slug
}

// liveEntity publishes one entry as plugin ("" is the owner) and returns its id.
func (s *standingFixture) liveEntity(plugin, schema, key string) string {
	s.t.Helper()
	cs := s.begin(plugin)
	s.stage(cs, Item{Kind: KindEntity, Op: OpUpsert, SourceKey: key + "-" + s.tag, Payload: raw(map[string]any{
		"schema": schema, "name": key, "slug": key + "-" + s.tag, "attributes": map[string]any{"title": "x"}})})
	s.publish(cs)
	var id string
	if err := s.db.Pool().QueryRow(s.ctx, `
		SELECT e.id::text FROM hatamoto_entities e JOIN hatamoto_entity_schemas c ON c.id = e.schema_id WHERE c.slug = $1 AND e.slug = $2`,
		schema, key+"-"+s.tag).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// plugged counts the entries the plugin owns in the given schemas.
func (s *standingFixture) plugged(schemas ...string) int {
	s.t.Helper()
	return s.count(`
		SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas c ON c.id = e.schema_id
		WHERE c.slug = ANY($1) AND e.origin = $2`, schemas, Origin(standingPlugin))
}

func (s *standingFixture) autoMarked(cs int64) bool {
	s.t.Helper()
	var auto bool
	if err := s.db.Pool().QueryRow(s.ctx, `SELECT auto_published FROM hatamoto_changesets WHERE id = $1`, cs).Scan(&auto); err != nil {
		s.t.Fatal(err)
	}
	return auto
}

// mustWait asserts the gate refuses cs as a whole: a reason, nothing applied,
// the changeset still staged and not automatic, no entry created.
func (s *standingFixture) mustWait(cs int64, schemas ...string) PolicyVerdict {
	s.t.Helper()
	if len(schemas) == 0 {
		schemas = []string{s.slug}
	}
	before := s.plugged(schemas...)
	v, res, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 9)
	if err != nil {
		s.t.Fatal(err)
	}
	if v.OK || len(v.Reasons) == 0 || res.Applied != 0 {
		s.t.Fatalf("not refused: %+v applied=%d", v, res.Applied)
	}
	if st := s.state(cs); st != StateStaged {
		s.t.Errorf("state = %s, want staged", st)
	}
	if s.autoMarked(cs) {
		s.t.Error("refused changeset is marked automatic")
	}
	if n := s.plugged(schemas...); n != before {
		s.t.Errorf("entries created: %d -> %d", before, n)
	}
	return v
}

func TestStandingCreatePublishes(t *testing.T) {
	s := newStandingFixture(t)
	s.withCreate(20)
	origin := func(slug string) (string, string) {
		var o, st string
		if err := s.db.Pool().QueryRow(s.ctx, `SELECT e.origin, e.status FROM hatamoto_entities e WHERE e.schema_id = (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1) AND e.slug = $2`,
			s.slug, slug).Scan(&o, &st); err != nil {
			t.Fatal(err)
		}
		return o, st
	}
	publish := func(items ...Item) (int64, PublishResult) {
		cs := s.stageAll(items...)
		v, res, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 5)
		if err != nil || !v.OK {
			t.Fatalf("publish: %+v %v", v, err)
		}
		if res.Applied != len(items) {
			t.Fatalf("applied %d of %d items", res.Applied, len(items))
		}
		if !s.autoMarked(cs) {
			t.Fatal("not marked automatic")
		}
		return cs, res
	}

	t.Run("empty_media_shaped_values", func(t *testing.T) {
		publish(s.newItem("emp", map[string]any{"attributes": map[string]any{"title": "T", "images": []any{}, "banner": map[string]any{"image": ""}}}))
	})

	t.Run("published_and_draft", func(t *testing.T) {
		events := s.events.count()
		publish(s.newItem("pub", map[string]any{"status": "published"}), s.newItem("dr", map[string]any{"status": "draft"}), s.newItem("dflt", nil))
		if o, st := origin("pub-" + s.tag); o != "plugin:"+standingPlugin || st != "published" {
			t.Errorf("published entry: origin %q status %q", o, st)
		}
		if o, st := origin("dr-" + s.tag); o != "plugin:"+standingPlugin || st != "draft" {
			t.Errorf("draft entry: origin %q status %q", o, st)
		}
		if _, st := origin("dflt-" + s.tag); st != "draft" {
			t.Errorf("default status = %q", st)
		}
		if n := s.events.count(); n != events+1 {
			t.Errorf("events %d -> %d, want one batch", events, n)
		}
	})

	t.Run("html_is_sanitised", func(t *testing.T) {
		publish(s.newItem("html", map[string]any{"attributes": map[string]any{"title": "T", "content": "<p>hello</p><script>alert(1)</script>"}}))
		var content string
		if err := s.db.Pool().QueryRow(s.ctx, `SELECT attributes->>'content' FROM hatamoto_entities WHERE slug = $1`, "html-"+s.tag).Scan(&content); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(content, "script") || !strings.Contains(content, "hello") {
			t.Errorf("stored content = %q", content)
		}
	})

	t.Run("generated_slug_suffixes_on_collision", func(t *testing.T) {
		a, b := s.newItem("ga", map[string]any{"name": "Same " + s.tag}), s.newItem("gb", map[string]any{"name": "Same " + s.tag})
		for _, it := range []*Item{&a, &b} {
			var p map[string]any
			_ = jsonx.Unmarshal(it.Payload, &p, jsonx.StoredLoose)
			delete(p, "slug")
			it.Payload = raw(p)
		}
		publish(a, b)
		if n := s.count(`SELECT count(DISTINCT slug) FROM hatamoto_entities WHERE schema_id = (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1) AND name = $2`, s.slug, "Same "+s.tag); n != 2 {
			t.Errorf("distinct slugs = %d, want 2", n)
		}
		if n := s.count(`SELECT count(*) FROM hatamoto_entities WHERE schema_id = (SELECT id FROM hatamoto_entity_schemas WHERE slug = $1) AND name = $2 AND slug LIKE '%-2'`, s.slug, "Same "+s.tag); n != 1 {
			t.Errorf("suffixed slugs = %d, want 1", n)
		}
	})

	t.Run("translation_of_same_changeset", func(t *testing.T) {
		src := s.newItem("tsrc", map[string]any{"locale": "en"})
		cp := s.newItem("tcopy", map[string]any{"locale": "es", "translation_of": src.SourceKey})
		publish(src, cp)
		g1 := s.groupOf(s.idOfSlugIn(s.slug, "tsrc-"+s.tag))
		g2 := s.groupOf(s.idOfSlugIn(s.slug, "tcopy-"+s.tag))
		if g1 == nil || g2 == nil || *g1 != *g2 {
			t.Errorf("translation groups: %v %v", g1, g2)
		}
	})

	t.Run("parent_key_of_same_changeset", func(t *testing.T) {
		par := s.newItem("par", nil)
		kid := s.newItem("kid", map[string]any{"parent_key": par.SourceKey})
		publish(par, kid)
		if n := s.count(`SELECT count(*) FROM hatamoto_entities WHERE slug = $1 AND parent_id IS NOT NULL`, "kid-"+s.tag); n != 1 {
			t.Errorf("child has no parent")
		}
	})
}

func (s *standingFixture) idOfSlugIn(schema, slug string) string {
	s.t.Helper()
	var id string
	if err := s.db.Pool().QueryRow(s.ctx, `
		SELECT e.id::text FROM hatamoto_entities e JOIN hatamoto_entity_schemas c ON c.id = e.schema_id WHERE c.slug = $1 AND e.slug = $2`,
		schema, slug).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func TestStandingCreateRefusals(t *testing.T) {
	s := newStandingFixture(t)
	other := s.otherSchema()
	otherOwned := s.liveEntity(standingPlugin, other, "oo")
	ownerOwned := s.liveEntity("", s.slug, "ow")
	cases := []struct {
		name  string
		build func() int64
		why   string // substring of a reason, "" for any
	}{
		{"schema_not_in_create_list", func() int64 {
			s.policy.Schemas = []string{s.slug, other}
			return s.stageAll(s.newItem("e1", map[string]any{"schema": other}))
		}, "not covered"},
		{"schema_unlisted", func() int64 {
			return s.stageAll(s.newItem("e2", map[string]any{"schema": "nope-" + s.tag}))
		}, "not covered"},
		{"new_term", func() int64 {
			return s.stageAll(s.newItem("e3", nil), Item{Kind: KindTerm, Op: OpUpsert, SourceKey: s.tag + "t3",
				Payload: raw(map[string]any{"name": s.tag + "t3", "slug": s.tag + "t3", "taxonomy": "tag"})})
		}, "only page"},
		{"existing_term_item", func() int64 {
			return s.stageAll(s.newItem("e4", nil), Item{Kind: KindTerm, Op: OpUpsert, TargetID: uuid.NewString(),
				Payload: raw(map[string]any{"name": s.tag + "t4", "slug": s.tag + "t4", "taxonomy": "tag"})})
		}, "only page"},
		{"translation_of_foreign_uuid", func() int64 {
			return s.stageAll(s.newItem("e5", map[string]any{"translation_of": ownerOwned}))
		}, "translation source"},
		{"translation_of_unlisted_schema", func() int64 {
			return s.stageAll(s.newItem("e6", map[string]any{"translation_of": otherOwned}))
		}, "translation source"},
		{"translation_of_missing", func() int64 {
			return s.stageAll(s.newItem("e7a", map[string]any{"translation_of": "ghost-" + s.tag}),
				s.newItem("e7b", map[string]any{"translation_of": uuid.NewString()}))
		}, "translation source"},
		{"blob_keys", func() int64 {
			// Staging refuses files on entity items, so plant the key as a
			// damaged store would: the gate must still refuse it.
			cs := s.stageAll(s.newItem("e9", nil))
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_changeset_items SET blob_keys = ARRAY['staged/x.png'] WHERE changeset_id = $1`, cs); err != nil {
				t.Fatal(err)
			}
			return cs
		}, "files"},
		{"media_attribute", func() int64 {
			return s.stageAll(s.newItem("e10", map[string]any{"attributes": map[string]any{"title": "T", "photo": "/media/x.png"}}))
		}, "image or file"},
		{"media_attribute_nested", func() int64 {
			return s.stageAll(s.newItem("e10n", map[string]any{"attributes": map[string]any{"title": "T", "gallery": []any{"/media/x.png"}}}))
		}, "image or file"},
		{"media_attribute_by_name", func() int64 {
			return s.stageAll(s.newItem("e10b", map[string]any{"attributes": map[string]any{"title": "T", "featured_image": "/media/x.png"}}))
		}, "image or file"},
		{"media_array_of_uri_by_name", func() int64 {
			return s.stageAll(s.newItem("e10a", map[string]any{"attributes": map[string]any{"title": "T", "images": []any{"/media/a.jpg"}}}))
		}, "image or file"},
		{"media_nested_property_by_name", func() int64 {
			return s.stageAll(s.newItem("e10h", map[string]any{"attributes": map[string]any{"title": "T", "banner": map[string]any{"image": "/media/a.jpg"}}}))
		}, "image or file"},
		{"media_url_hint_by_name", func() int64 {
			return s.stageAll(s.newItem("e10x", map[string]any{"attributes": map[string]any{"title": "T", "hero_image": "/media/a.jpg"}}))
		}, "image or file"},
		{"media_name_plain_og_image", func() int64 {
			return s.stageAll(s.newItem("e10o", map[string]any{"attributes": map[string]any{"title": "T", "og_image": "x.jpg"}}))
		}, "image or file"},
		{"media_name_plain_image", func() int64 {
			return s.stageAll(s.newItem("e10i", map[string]any{"attributes": map[string]any{"title": "T", "image": "x.jpg"}}))
		}, "image or file"},
		{"media_path_value", func() int64 {
			return s.stageAll(s.newItem("e10p", map[string]any{"attributes": map[string]any{"title": "/media/x.jpg"}}))
		}, "media library"},
		{"media_path_in_markup", func() int64 {
			return s.stageAll(s.newItem("e10m", map[string]any{"attributes": map[string]any{"title": "T", "content": `<p><img src="/uploads/2026/x.jpg"></p>`}}))
		}, "media library"},
		{"media_absolute_own_url", func() int64 {
			return s.stageAll(s.newItem("e10q", map[string]any{"attributes": map[string]any{"title": "https://" + s.siteHost() + "/media/2026/x.jpg"}}))
		}, "media library"},
		{"media_path_under_meta_key", func() int64 {
			return s.stageAll(s.newItem("e10t", map[string]any{"attributes": map[string]any{"title": "T", "meta_description": "/media/x.jpg"}}))
		}, "media library"},
		{"media_path_as_map_key", func() int64 {
			return s.stageAll(s.newItem("e10r", map[string]any{"attributes": map[string]any{"title": "T", "nested": map[string]any{"/media/x.jpg": 1}}}))
		}, "media library"},
		{"media_path_in_array", func() int64 {
			return s.stageAll(s.newItem("e10y", map[string]any{"attributes": map[string]any{"title": "T", "nested": map[string]any{"x": []any{"a", "/media/x.jpg"}}}}))
		}, "media library"},
		{"media_library_id", func() int64 {
			return s.stageAll(s.newItem("e10d", map[string]any{"attributes": map[string]any{"title": s.mediaRow("https://cdn.example/pics/" + s.tag + ".jpg")}}))
		}, "media library"},
		{"media_library_public_url", func() int64 {
			u := "https://" + s.siteHost() + "/pics/" + s.tag + "b.jpg"
			s.mediaRow(u)
			return s.stageAll(s.newItem("e10e", map[string]any{"attributes": map[string]any{"title": u}}))
		}, "media library"},
		{"media_alias_of_image_field", func() int64 {
			return s.stageAll(s.newItem("e10z", map[string]any{"attributes": map[string]any{"title": "T", "caption": "x.jpg"}}))
		}, "image or file"},
		{"undeclared_attribute_map_key", func() int64 {
			return s.stageAll(s.newItem("e10k", map[string]any{"attributes": map[string]any{"title": "T", "hero": map[string]any{"/media/a.jpg": nil}}}))
		}, "does not declare"},
		{"undeclared_attribute", func() int64 {
			return s.stageAll(s.newItem("e10u", map[string]any{"attributes": map[string]any{"title": "T", "hero": "/media/x.png"}}))
		}, "does not declare"},
		{"slug_taken", func() int64 {
			return s.stageAll(s.newItem("e11", map[string]any{"slug": "s-" + s.tag}))
		}, "taken"},
		{"slug_repeated_in_changeset", func() int64 {
			return s.stageAll(s.newItem("e11a", map[string]any{"slug": "rep-" + s.tag}), s.newItem("e11b", map[string]any{"slug": "rep-" + s.tag}))
		}, "taken"},
		{"mapped_source_key_is_update_rule", func() int64 {
			// The key already belongs to a live entry: this is an update, so
			// it obeys the update rule and may not change text.
			a := s.attrs()
			a["title"] = "Changed"
			return s.stageAll(s.newItem("e12", nil), Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: s.payload("s-"+s.tag, a)})
		}, `"title"`},
		{"mapped_but_gone", func() int64 {
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET origin = 'owner' WHERE id = $1`, s.target); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Pool().Exec(s.ctx, `INSERT INTO hatamoto_changeset_source_keys (origin, kind, source_key, target_id) VALUES ($1, 'entity', $2, $3)`,
				Origin(standingPlugin), "gone-"+s.tag, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			it := s.newItem("e13", nil)
			it.SourceKey = "gone-" + s.tag
			return s.stageAll(it)
		}, "creating never qualifies"},
		{"status_other", func() int64 {
			return s.stageAll(s.newItem("e14a", map[string]any{"status": "archived"}), s.newItem("e14b", map[string]any{"status": "scheduled"}))
		}, "status"},
		{"over_cap_single", func() int64 {
			s.withCreate(2)
			return s.stageAll(s.newItem("e15a", nil), s.newItem("e15b", nil), s.newItem("e15c", nil))
		}, "new entries in an hour"},
		{"over_cap_rolling", func() int64 {
			s.withCreate(3)
			cs := s.stageAll(s.newItem("e16a", nil), s.newItem("e16b", nil))
			if v, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 1); err != nil || !v.OK {
				t.Fatalf("first run: %+v %v", v, err)
			}
			return s.stageAll(s.newItem("e16c", nil), s.newItem("e16d", nil))
		}, "new entries in an hour"},
		{"mixed_foreign_update", func() int64 {
			a := s.attrs()
			a["price"] = 2.0
			cs := s.stageAll(s.newItem("e18", nil), Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: s.payload("s-"+s.tag, a)})
			// The owner took the entry over after the run staged its update.
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET origin = 'owner' WHERE id = $1`, s.target); err != nil {
				t.Fatal(err)
			}
			return cs
		}, "did not create"},
		{"mixed_delete", func() int64 {
			return s.stageAll(s.newItem("e19d", nil), Item{Kind: KindEntity, Op: OpDelete, TargetID: s.target})
		}, "closer look"},
		{"mixed_redirect", func() int64 {
			return s.stageAll(s.newItem("e19r", nil), Item{Kind: KindRedirect, Op: OpUpsert, Payload: raw(map[string]any{"source_path": "/" + s.tag + "-old", "target": "/new"})})
		}, "closer look"},
		{"mixed_menu", func() int64 {
			return s.stageAll(s.newItem("e19m", nil), Item{Kind: KindMenu, Op: OpUpsert, Payload: raw(map[string]any{"name": "M", "slug": s.tag + "-menu"})})
		}, "only page"},
		{"mixed_proposal", func() int64 {
			cs := s.stageAll(s.newItem("e20", nil))
			if _, err := s.db.Pool().Exec(s.ctx, `INSERT INTO hatamoto_run_proposals (run_id, kind, payload) SELECT run_id, 'settings', '{}' FROM hatamoto_changesets WHERE id = $1`, cs); err != nil {
				t.Fatal(err)
			}
			return cs
		}, "suggests"},
		{"parent_id", func() int64 {
			return s.stageAll(s.newItem("e21a", map[string]any{"parent_id": ownerOwned}))
		}, "existing page"},
		{"parent_key_foreign", func() int64 {
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET origin = 'owner' WHERE id = $1`, s.target); err != nil {
				t.Fatal(err)
			}
			return s.stageAll(s.newItem("e21b", map[string]any{"parent_key": "k-" + s.tag}))
		}, "parent"},
		{"parent_key_unlisted_schema", func() int64 {
			return s.stageAll(s.newItem("e21c", map[string]any{"parent_key": "oo-" + s.tag}))
		}, "parent"},
		{"external_link", func() int64 {
			return s.stageAll(s.newItem("e22", map[string]any{"attributes": map[string]any{"title": "T", "content": `<p><a href="https://evil.example/x">more</a></p>`}}))
		}, "closer look"},
		{"external_embed", func() int64 {
			return s.stageAll(s.newItem("e22e", map[string]any{"attributes": map[string]any{"title": "T", "content": `<iframe src="https://evil.example/x"></iframe>`}}))
		}, "closer look"},
		{"canonical_address", func() int64 {
			return s.stageAll(s.newItem("e22c", map[string]any{"seo": map[string]any{"canonical": "https://evil.example/"}}))
		}, "closer look"},
		{"duplicate_source_key", func() int64 {
			a, b := s.newItem("e22d", nil), s.newItem("e22d", map[string]any{"slug": "other-" + s.tag})
			return s.stageAll(a, b)
		}, "source key"},
		{"cap_zero", func() int64 {
			s.withCreate(0)
			return s.stageAll(s.newItem("e22z", nil))
		}, "creating never qualifies"},
		{"no_create_rule", func() int64 {
			s.policy.Create = nil
			return s.stageAll(s.newItem("e23", nil))
		}, "creating never qualifies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func(p StandingPolicy) { s.policy = p }(s.policy)
			s.withCreate(10)
			s.policy.Schemas = []string{s.slug}
			if _, err := s.db.Pool().Exec(s.ctx, `UPDATE hatamoto_entities SET attributes = $2, origin = $3 WHERE id = $1`,
				s.target, raw(s.attrs()), Origin(standingPlugin)); err != nil {
				t.Fatal(err)
			}
			cs := c.build()
			v := s.mustWait(cs, s.slug, other)
			if !strings.Contains(strings.Join(v.Reasons, "|"), c.why) {
				t.Errorf("reasons %q lack %q", v.Reasons, c.why)
			}
		})
	}
}

// Creates and updates have separate hourly budgets.
func TestStandingCreateAndUpdateBudgetsAreSeparate(t *testing.T) {
	s := newStandingFixture(t)
	s.withCreate(3)
	s.policy.MaxPerHour = 1
	run := func(cs int64) PolicyVerdict {
		v, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Three creates fill the create budget and leave the update budget alone.
	if v := run(s.stageAll(s.newItem("b1", nil), s.newItem("b2", nil), s.newItem("b3", nil))); !v.OK {
		t.Fatalf("creates: %+v", v)
	}
	if n := s.count(`SELECT count(*) FROM hatamoto_changeset_items i JOIN hatamoto_changesets c ON c.id = i.changeset_id
		WHERE c.plugin = $1 AND c.auto_published AND i.result = 'applied'`, standingPlugin); n != 3 {
		t.Fatalf("applied automatic items = %d, want 3", n)
	}
	// An update plus a create in one changeset: the update fits its budget of 1,
	// the create is over its budget of 3.
	if v := run(s.stageAll(s.newItem("b4", nil), Item{Kind: KindEntity, Op: OpUpsert, SourceKey: "k-" + s.tag, Payload: s.payload("s-"+s.tag, func() map[string]any { a := s.attrs(); a["price"] = 2.0; return a }())})); v.OK {
		t.Fatalf("a fourth create fit the budget: %+v", v)
	}
	// The update alone still fits: creates did not eat its budget.
	up := s.update(func(a map[string]any) { a["price"] = 3.0 })
	if v := run(up); !v.OK {
		t.Fatalf("update after creates: %+v", v)
	}
	// The update is spent now (1 of 1), and the create budget is untouched by it.
	if v := run(s.update(func(a map[string]any) { a["price"] = 4.0 })); v.OK {
		t.Fatalf("second update fit the budget: %+v", v)
	}
	s.now = s.now.Add(61 * time.Minute)
	s.withCreate(1)
	if v := run(s.stageAll(s.newItem("b5", nil))); !v.OK {
		t.Fatalf("create after an hour: %+v", v)
	}
	// A spent create budget does not stop updates, and vice versa.
	if v := run(s.update(func(a map[string]any) { a["price"] = 5.0 })); !v.OK {
		t.Fatalf("update with the create budget spent: %+v", v)
	}
	if v := run(s.stageAll(s.newItem("b6", nil))); v.OK {
		t.Fatalf("create past the budget: %+v", v)
	}
}

// Two runs of one plugin that finish together cannot both pass a create cap
// only one fits.
func TestStandingCreateCapHoldsAcrossConcurrentGates(t *testing.T) {
	s := newStandingFixture(t)
	s.withCreate(2)
	a := s.stageAll(s.newItem("ca1", nil), s.newItem("ca2", nil))
	b := s.stageAll(s.newItem("cb1", nil), s.newItem("cb2", nil))
	var wg sync.WaitGroup
	oks := make([]bool, 2)
	for i, cs := range []int64{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, err := s.eng.PublishUnderPolicy(s.ctx, cs, s.policy, 1)
			if err != nil {
				t.Error(err)
			}
			oks[i] = v.OK
		}()
	}
	wg.Wait()
	if oks[0] == oks[1] {
		t.Fatalf("both or neither published: %v", oks)
	}
	if n := s.plugged(s.slug); n != 3 { // the fixture entry plus the two creates
		t.Fatalf("plugin entries = %d, want 3", n)
	}
}

func (s *standingFixture) translationGroup(id string) *string {
	s.t.Helper()
	var g *string
	if err := s.db.Pool().QueryRow(s.ctx, `SELECT translation_group::text FROM hatamoto_entities WHERE id = $1`, id).Scan(&g); err != nil {
		s.t.Fatal(err)
	}
	return g
}

// A source key shaped like an entry id must not steer translation_of: the
// writer reads an id as the entity, so the gate must judge that entity.
func TestStandingCreateTranslationOfUUIDSourceKeyStaysStaged(t *testing.T) {
	s := newStandingFixture(t)
	s.withCreate(10)
	ownerEntry := s.liveEntity("", s.slug, "ow")
	before := s.translationGroup(ownerEntry)
	a := s.newItem("tk", nil)
	a.SourceKey = ownerEntry
	b := s.newItem("tb", map[string]any{"locale": "fr", "translation_of": ownerEntry})
	cs := s.stageAll(a, b)
	v := s.mustWait(cs)
	if !strings.Contains(strings.Join(v.Reasons, "|"), "translation source") {
		t.Errorf("reasons %q lack the translation source", v.Reasons)
	}
	if after := s.translationGroup(ownerEntry); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("owner entry translation_group changed: %v -> %v", before, after)
	}
}

// The gate and the writer must agree on what a UUID-shaped reference means.
func TestStandingCreateTranslationOfIDResolvesLikeTheWriter(t *testing.T) {
	t.Run("translation_of_owned_id_is_that_entity", func(t *testing.T) {
		s := newStandingFixture(t)
		s.withCreate(10)
		v, res, err := s.eng.PublishUnderPolicy(s.ctx, s.stageAll(s.newItem("tr", map[string]any{"locale": "fr", "translation_of": s.target})), s.policy, 9)
		if err != nil || !v.OK || res.Applied != 1 {
			t.Fatalf("verdict %+v applied=%d err=%v", v, res.Applied, err)
		}
		if s.translationGroup(s.target) == nil || s.translationGroup(s.idOfSlugIn(s.slug, "tr-"+s.tag)) == nil {
			t.Error("the owned entry and the new one are not linked")
		}
	})
}

// The writer reads parent_key only as a source key, never as an entity id.
func TestStandingCreateParentKeyIsNeverAnEntityID(t *testing.T) {
	s := newStandingFixture(t)
	s.withCreate(10)
	// s.target is an owned entry in a listed schema, but its id is no source key.
	v := s.mustWait(s.stageAll(s.newItem("pk", map[string]any{"parent_key": s.target})))
	if !strings.Contains(strings.Join(v.Reasons, "|"), "parent") {
		t.Errorf("reasons %q lack the parent", v.Reasons)
	}
}
