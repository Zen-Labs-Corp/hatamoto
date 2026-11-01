package admin

import (
	"context"
	"testing"
)

var (
	_ EntityCopier = (*fakeCopier)(nil)
	_ TermGrouper  = (*fakeTermGroups)(nil)
)

func TestMultiLocaleGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, _ := translationsRig(t, "en", "pt")
	set, ok := c.a.multiLocale(ctx)
	if !ok || set == nil || len(set.Tags()) != 2 {
		t.Fatalf("multi-locale site: set=%v ok=%v", set, ok)
	}

	single, _ := translationsRig(t)
	if _, ok := single.a.multiLocale(ctx); ok {
		t.Error("single-locale site passed the gate")
	}

	c.a.deps.Content.Groups = nil
	if _, ok := c.a.multiLocale(ctx); ok {
		t.Error("nil group store passed the gate")
	}

	c.a.deps.Content.Groups = newFakeGroups()
	c.a.deps.Site = nil
	if _, ok := c.a.multiLocale(ctx); ok {
		t.Error("nil site store passed the gate")
	}
}
