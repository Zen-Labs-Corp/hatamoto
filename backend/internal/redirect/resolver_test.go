package redirect

import (
	"context"
	"testing"
)

func TestNormalise(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"//", "/"},
		{"/old/", "/old"},
		{"/old//seo", "/old/seo"},
		{"/foo//bar//", "/foo/bar"},
		{"/old/seo", "/old/seo"},
		{"no-leading-slash", "/no-leading-slash"},
		{"/trailing///", "/trailing"},
		// Case is deliberately preserved. Folding it would make paths
		// that 404 today start redirecting.
		{"/En", "/En"},
		{"/The-Guide/", "/The-Guide"},
	}
	for _, c := range cases {
		if got := Normalise(c.in); got != c.want {
			t.Errorf("Normalise(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToggleSlash(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/", "/"},
		{"/old", "/old/"},
		{"/old/", "/old"},
		{"/a/b", "/a/b/"},
	}
	for _, c := range cases {
		if got := ToggleSlash(c.in); got != c.want {
			t.Errorf("ToggleSlash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// stubProvider matches one exact path.
type stubProvider struct {
	name     string
	priority int
	on       string
	target   string
	calls    *int
}

func (s *stubProvider) Name() string  { return s.name }
func (s *stubProvider) Priority() int { return s.priority }
func (s *stubProvider) Resolve(_ context.Context, path string) (*Match, string) {
	if s.calls != nil {
		*s.calls++
	}
	if path == s.on {
		return &Match{Target: s.target, Status: 301, Rule: s.on}, "exact"
	}
	return nil, ""
}

func TestChainRunsInPriorityOrder(t *testing.T) {
	low := &stubProvider{name: "low", priority: 500, on: "/x", target: "/from-low"}
	high := &stubProvider{name: "high", priority: 100, on: "/x", target: "/from-high"}

	// Registered out of order on purpose.
	ans := NewChain(low, high).Resolve(context.Background(), "/x")

	if !ans.Found() {
		t.Fatal("expected a match")
	}
	if ans.Redirect.Target != "/from-high" {
		t.Errorf("target = %q, want /from-high (lower priority number wins)", ans.Redirect.Target)
	}
	if ans.MatchedBy != "high" {
		t.Errorf("MatchedBy = %q, want high", ans.MatchedBy)
	}
}

func TestChainStopsAtFirstMatch(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := &stubProvider{name: "first", priority: 100, on: "/x", target: "/a", calls: &firstCalls}
	second := &stubProvider{name: "second", priority: 200, on: "/x", target: "/b", calls: &secondCalls}

	NewChain(first, second).Resolve(context.Background(), "/x")

	if firstCalls != 1 {
		t.Errorf("first provider called %d times, want 1", firstCalls)
	}
	if secondCalls != 0 {
		t.Errorf("second provider called %d times, want 0: the chain must stop at the first match", secondCalls)
	}
}

func TestChainRecordsEveryStageIncludingMisses(t *testing.T) {
	a := &stubProvider{name: "a", priority: 100, on: "/nope", target: "/a"}
	b := &stubProvider{name: "b", priority: 200, on: "/nope", target: "/b"}
	c := &stubProvider{name: "c", priority: 300, on: "/x", target: "/c"}

	ans := NewChain(a, b, c).Resolve(context.Background(), "/x")

	if len(ans.Stages) != 3 {
		t.Fatalf("recorded %d stages, want 3: misses must be recorded too", len(ans.Stages))
	}
	if ans.Stages[0].Matched || ans.Stages[1].Matched {
		t.Error("first two stages should be recorded as misses")
	}
	if !ans.Stages[2].Matched {
		t.Error("third stage should be recorded as the match")
	}
	if ans.Stages[2].Rule != "/x" {
		t.Errorf("matching stage Rule = %q, want /x", ans.Stages[2].Rule)
	}
}

func TestChainNormalisesBeforeMatching(t *testing.T) {
	p := &stubProvider{name: "p", priority: 100, on: "/old/seo", target: "/t"}
	ans := NewChain(p).Resolve(context.Background(), "/old//seo/")
	if !ans.Found() {
		t.Fatal("expected the normalised path to match")
	}
	if ans.Normalised != "/old/seo" {
		t.Errorf("Normalised = %q, want /old/seo", ans.Normalised)
	}
	if ans.Path != "/old//seo/" {
		t.Errorf("Path = %q, want the original preserved", ans.Path)
	}
}

func TestChainNoMatchIsNotFound(t *testing.T) {
	p := &stubProvider{name: "p", priority: 100, on: "/other", target: "/t"}
	ans := NewChain(p).Resolve(context.Background(), "/x")
	if ans.Found() {
		t.Error("expected no match")
	}
	if len(ans.Stages) != 1 {
		t.Errorf("recorded %d stages, want 1", len(ans.Stages))
	}
}

func TestChainRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	p := &stubProvider{name: "p", priority: 100, on: "/x", target: "/t", calls: &calls}
	ans := NewChain(p).Resolve(ctx, "/x")
	if calls != 0 {
		t.Error("provider must not run on a cancelled context")
	}
	if ans.Found() {
		t.Error("cancelled resolve must not report a match")
	}
}

func TestDBRulesExactMatch(t *testing.T) {
	p := NewDBRulesProvider(func(_ context.Context, path string) *Redirect {
		if path == "/old/seo-services" {
			return &Redirect{SourcePath: path, TargetURL: "/old/seo", StatusCode: 301}
		}
		return nil
	})
	m, variant := p.Resolve(context.Background(), "/old/seo-services")
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.Target != "/old/seo" || m.Status != 301 {
		t.Errorf("got %+v", m)
	}
	if variant != "exact" {
		t.Errorf("variant = %q, want exact", variant)
	}
}

func TestDBRulesRetriesWithSlashToggled(t *testing.T) {
	// Mirrors the real table: rules stored WITH a trailing slash, looked
	// up from a normalised path that has none.
	p := NewDBRulesProvider(func(_ context.Context, path string) *Redirect {
		if path == "/old/content-marketing/" {
			return &Redirect{SourcePath: path, TargetURL: "/old/copywriting-agency", StatusCode: 301}
		}
		return nil
	})
	m, variant := p.Resolve(context.Background(), "/old/content-marketing")
	if m == nil {
		t.Fatal("expected the slash-toggled retry to match")
	}
	if variant != "slash-toggled" {
		t.Errorf("variant = %q, want slash-toggled", variant)
	}
}

func TestDBRulesCoercesInvalidStatus(t *testing.T) {
	p := NewDBRulesProvider(func(_ context.Context, _ string) *Redirect {
		return &Redirect{SourcePath: "/x", TargetURL: "/y", StatusCode: 999}
	})
	m, _ := p.Resolve(context.Background(), "/x")
	if m.Status != 301 {
		t.Errorf("status = %d, want 301 for an out-of-range code", m.Status)
	}
}

func TestDBRulesNilLookupDeclines(t *testing.T) {
	p := NewDBRulesProvider(nil)
	if m, _ := p.Resolve(context.Background(), "/x"); m != nil {
		t.Error("a nil lookup must decline, not panic")
	}
}
