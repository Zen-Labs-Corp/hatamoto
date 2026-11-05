//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnpublishStore(t *testing.T) {
	ctx := context.Background()
	svc, schemaID, _ := localeFixture(t, `{}`)
	db := newIntegrationDB(t)
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "p", Slug: "p", Locale: "en", Attributes: jsontext.Value(`{}`)})
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

	// The created redirect is tracked apart from the schedule: a fired
	// schedule is not a pending one, and Clear / Get never see the redirect.
	_ = st.Set(ctx, e.ID, at, "/new")
	rid := uuid.New()
	if err := st.SetRedirect(ctx, e.ID, rid); err != nil {
		t.Fatal(err)
	}
	if err := st.Clear(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Get(ctx, e.ID); u != nil {
		t.Fatalf("Clear left %+v", u)
	}
	got, err := st.TakeRedirect(ctx, e.ID)
	if err != nil || got == nil || *got != rid {
		t.Fatalf("TakeRedirect = %v, %v", got, err)
	}
	if got, _ := st.TakeRedirect(ctx, e.ID); got != nil {
		t.Fatalf("second TakeRedirect = %v", got)
	}

	// Claim takes a due row once and leaves a postponed one alone.
	_ = st.Set(ctx, e.ID, time.Now().Add(time.Hour), "")
	if c, err := st.Claim(ctx, e.ID); err != nil || c != nil {
		t.Fatalf("Claim of a future row = %v, %v", c, err)
	}
	past := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	_ = st.Set(ctx, e.ID, past, "/t")
	c, err := st.Claim(ctx, e.ID)
	if err != nil || c == nil || !c.At.Equal(past) || c.RedirectTarget != "/t" {
		t.Fatalf("Claim = %+v, %v", c, err)
	}
	if again, _ := st.Claim(ctx, e.ID); again != nil {
		t.Fatalf("second Claim = %+v", again)
	}
	_ = st.Requeue(ctx, *c)
	_ = st.Set(ctx, e.ID, at, "/newer")
	_ = st.Requeue(ctx, *c) // does not overwrite a newer schedule
	if u, _ := st.Get(ctx, e.ID); u == nil || u.RedirectTarget != "/newer" {
		t.Fatalf("Requeue overwrote: %+v", u)
	}
}
