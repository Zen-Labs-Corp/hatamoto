//go:build integration

package packages

import (
	"context"
	"slices"
	"testing"
)

func TestUpdateEntityWriteChangeNeedsConfirmation(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.mgr.store.Put(ctx, entityWritePkg(t, "1.0.0", "story", `"url"`), SourceUpload, true); err != nil {
		t.Fatal(err)
	}
	prev, err := r.mgr.store.Get(ctx, KindPlugin, "board")
	if err != nil {
		t.Fatal(err)
	}

	c, err := r.mgr.needsConfirmation(ctx, entityWritePkg(t, "2.0.0", "note", `"url"`), prev, SourceUpload)
	if err != nil || c == nil || !c.WidenedAccess || !c.EntitySchemaChanged || c.OldEntitySchema != "story" || c.NewEntitySchema != "note" {
		t.Fatalf("schema change: %+v, %v", c, err)
	}
	c, err = r.mgr.needsConfirmation(ctx, entityWritePkg(t, "2.0.0", "story", `"url","text"`), prev, SourceUpload)
	if err != nil || c == nil || !c.WidenedAccess || c.EntitySchemaChanged || !slices.Equal(c.NewEntityFields, []string{"text"}) {
		t.Fatalf("fields widen: %+v, %v", c, err)
	}
	c, err = r.mgr.needsConfirmation(ctx, entityWritePkg(t, "2.0.0", "story", `"url"`), prev, SourceUpload)
	if err != nil || c != nil {
		t.Fatalf("same schema and fields: %+v, %v", c, err)
	}
}
