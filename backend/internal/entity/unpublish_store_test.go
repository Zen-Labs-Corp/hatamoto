//go:build integration

package entity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnpublishStore(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "p", Slug: "p", Locale: "en", Attributes: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	st := NewUnpublishStore(db)
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)

	if u, err := st.Get(ctx, e.ID); err != nil || u != nil {
		t.Fatalf("Get before Set = %v, %v", u, err)
	}
	if err := st.Set(ctx, e.ID, at, "/new"); err != nil {
		t.Fatal(err)
	}
	u, err := st.Get(ctx, e.ID)
	if err != nil || u == nil || !u.At.Equal(at) || u.RedirectTarget != "/new" {
		t.Fatalf("Get = %+v, %v", u, err)
	}
	if err := st.Set(ctx, e.ID, at.Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Map(ctx, []uuid.UUID{e.ID, uuid.New()})
	if len(m) != 1 || m[e.ID].RedirectTarget != "" {
		t.Fatalf("Map = %+v", m)
	}
	if err := st.Clear(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Get(ctx, e.ID); u != nil {
		t.Fatalf("Clear left %+v", u)
	}

	// A row that created a redirect survives Clear and is taken once.
	_ = st.Set(ctx, e.ID, at, "/new")
	rid := uuid.New()
	if err := st.SetRedirect(ctx, e.ID, rid); err != nil {
		t.Fatal(err)
	}
	_ = st.Clear(ctx, e.ID)
	got, err := st.TakeRedirect(ctx, e.ID)
	if err != nil || got == nil || *got != rid {
		t.Fatalf("TakeRedirect = %v, %v", got, err)
	}
	if got, _ := st.TakeRedirect(ctx, e.ID); got != nil {
		t.Fatalf("second TakeRedirect = %v", got)
	}
}
