package admin

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

var (
	_ GroupStore = (*entity.GroupStore)(nil)
	_ GroupStore = (*fakeGroups)(nil)
)

func TestFakeGroupsSatisfiesStore(t *testing.T) {
	var g GroupStore = newFakeGroups()
	ctx := context.Background()
	if ms, err := g.Members(ctx, fid(1)); err != nil || len(ms) != 0 {
		t.Errorf("Members = %v, %v; want none", ms, err)
	}
	if _, err := g.Link(ctx, fid(1), fid(2)); err != nil {
		t.Errorf("Link = %v", err)
	}
	f := g.(*fakeGroups)
	f.err = errors.New("boom")
	if err := g.Unlink(ctx, fid(1)); err == nil {
		t.Error("Unlink ignored the canned error")
	}
	if len(f.linked) != 1 || len(f.unlinked) != 0 {
		t.Errorf("recorded linked=%v unlinked=%v", f.linked, f.unlinked)
	}
}
