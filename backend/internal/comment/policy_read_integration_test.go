//go:build integration

package comment

import (
	"testing"
	"time"
)

func (f *fixture) setComments(json string) {
	f.t.Helper()
	f.exec(`UPDATE hatamoto_entity_schemas SET settings = jsonb_set(settings, '{comments}', $1::jsonb) WHERE slug = 'cmm-note'`, json)
}

func TestEntityConfigReadsModeOrderPolicy(t *testing.T) {
	f := newFixture(t)
	if cfg, err := f.svc.EntityConfig(f.ctx, f.entity); err != nil || cfg != (EntityConfig{Mode: ModeOff}) {
		t.Fatalf("comments off: %+v, %v", cfg, err)
	}
	f.enable()
	if cfg, _ := f.svc.EntityConfig(f.ctx, f.entity); cfg.Mode != ModeOpen || cfg.Order != "" || cfg.Policy.Enabled {
		t.Errorf("unset = %+v", cfg)
	}
	f.setComments(`{"mode":"closed","order":"oldest","policy":{"enabled":true,"max_depth":20,"edit_window_minutes":60,"slow_mode_seconds":5}}`)
	cfg, err := f.svc.EntityConfig(f.ctx, f.entity)
	if err != nil {
		t.Fatal(err)
	}
	want := EntityConfig{Mode: ModeClosed, Order: OrderOldest, Policy: Policy{Enabled: true, MaxDepth: 20, EditWindowMinutes: 60, SlowModeSeconds: 5}}
	if cfg != want {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
	f.setComments(`{"order":"sideways","policy":"yes"}`)
	cfg, err = f.svc.EntityConfig(f.ctx, f.entity)
	if err != nil || cfg.Order != "" || cfg.Policy.Enabled {
		t.Errorf("junk = %+v, %v; want unset, no error", cfg, err)
	}
}

func TestThreadFollowsTypeOrder(t *testing.T) {
	f := newFixture(t)
	f.enable() // site sort: newest
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i, b := range []string{"first", "second", "third"} {
		c := f.store(Draft{EntityID: f.entity, BodyRaw: b, BodyHTML: "<p>" + b + "</p>", Status: StatusApproved})
		f.exec(`UPDATE hatamoto_comments SET created_at = $2 WHERE id = $1`, c.ID, base.Add(time.Duration(i)*time.Minute))
	}
	first := func() string {
		t.Helper()
		page, err := f.svc.Thread(f.ctx, f.entity, 1)
		if err != nil || len(page.Comments) != 3 {
			t.Fatalf("thread: %d comments, %v", len(page.Comments), err)
		}
		return page.Comments[0].BodyHTML
	}
	for _, tc := range []struct{ settings, want string }{
		{`{}`, "<p>third</p>"},
		{`{"order":"oldest"}`, "<p>first</p>"},
		{`{"order":"newest"}`, "<p>third</p>"},
		{`{"order":"rank"}`, "<p>third</p>"},
	} {
		f.setComments(tc.settings)
		if got := first(); got != tc.want {
			t.Errorf("%s: first = %s, want %s", tc.settings, got, tc.want)
		}
	}
}
