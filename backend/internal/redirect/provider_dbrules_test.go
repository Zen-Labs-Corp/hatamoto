package redirect

import (
	"context"
	"testing"
)

func TestDBRulesProviderWithPatterns(t *testing.T) {
	exact := map[string]*Redirect{"/a": {SourcePath: "/a", TargetURL: "/exact", StatusCode: 302}}
	lookup := func(_ context.Context, p string) *Redirect { return exact[p] }
	pattern := func(_ context.Context, p string) *Redirect {
		if p == "/w/1" {
			return &Redirect{SourcePath: "/w/*", TargetURL: "/t/1"}
		}
		return nil
	}
	ctx := context.Background()

	without := NewDBRulesProvider(lookup)
	if m, _ := without.Resolve(ctx, "/w/1"); m != nil {
		t.Fatalf("patterns must be opt-in, got %+v", m)
	}

	p := NewDBRulesProvider(lookup).WithPatterns(pattern)
	if m, variant := p.Resolve(ctx, "/a"); m == nil || variant != "exact" || m.Status != 302 {
		t.Fatalf("exact: %+v %q", m, variant)
	}
	if m, variant := p.Resolve(ctx, "/a/"); m == nil || variant != "slash-toggled" {
		t.Fatalf("slash: %+v %q", m, variant)
	}
	m, variant := p.Resolve(ctx, "/w/1")
	if m == nil || variant != "pattern" || m.Target != "/t/1" || m.Status != 301 || m.Rule != "/w/*" {
		t.Fatalf("pattern: %+v %q", m, variant)
	}
	if m, _ := p.Resolve(ctx, "/none"); m != nil {
		t.Fatalf("unexpected: %+v", m)
	}
}
