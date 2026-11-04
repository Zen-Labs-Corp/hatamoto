//go:build integration

package changeset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
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
	slug := "pr-" + f.tag
	sc, err := entity.NewPostgresSchemaRepository(f.db).Create(f.ctx, &entity.CreateSchemaInput{Name: "pr " + f.tag, Slug: slug,
		Schema: json.RawMessage(`{"type":"object","properties":{
			"price":{"type":"number"},"stock":{"type":"integer"},"active":{"type":"boolean"},
			"on":{"type":"string","format":"date"},"tier":{"type":"string","enum":["a","b"]},
			"title":{"type":"string"},"nested":{"type":"object"}}}`)})
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

func (s *standingFixture) payload(slug string, attrs map[string]any) json.RawMessage {
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
				if _, err := s.eng.PublishReviewed(s.ctx, cs); err != nil {
					t.Fatal(err)
				}
				if err := s.eng.MarkAutoPublished(s.ctx, cs, 7); err != nil {
					t.Fatal(err)
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
	if _, err := s.eng.PublishReviewed(s.ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.eng.MarkAutoPublished(s.ctx, first, 1); err != nil {
		t.Fatal(err)
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
