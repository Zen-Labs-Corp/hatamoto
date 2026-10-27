package entity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/testutil"
)

// On a credited entity the author text is output only: an older copy or an
// edited name is overwritten with the credited name, never adopted.
func TestCreditUpdate_CreditedEntityIgnoresAuthorText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeBylines{}
	s := &Service{bylines: f}
	_, _ = f.Resolve(ctx, "Jane Doe")
	bob, _ := f.Resolve(ctx, "Bob")
	jane := f.people["Jane Doe"]
	oldCopy := json.RawMessage(`{"author":"Jane Doe","author_name":"Jane Doe","content":"x"}`)
	existing := &Entity{Attributes: oldCopy, BylinePersonID: &jane}

	// 1. credit Bob explicitly; 2. an update carries the older copy.
	step := &UpdateEntityInput{BylinePersonID: bob.PersonID}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, step))
	existing = &Entity{Attributes: *step.Attributes, BylinePersonID: bob.PersonID}

	raw := json.RawMessage(`{"author":"Jane Doe","author_name":"Jane Doe","content":"edited"}`)
	in := &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.Nil(t, in.BylinePersonID)
	testutil.True(t, in.AuthorID == nil, "mirror untouched")
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":"Bob"`), "%s", *in.Attributes)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author_name":"Bob"`), "%s", *in.Attributes)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"content":"edited"`), "%s", *in.Attributes)

	// An edited name does not move the credit either.
	raw = json.RawMessage(`{"author":"Carol"}`)
	in = &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.Nil(t, in.BylinePersonID)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":"Bob"`), "%s", *in.Attributes)

	// Organisation credit: the older copy becomes the organisation name.
	org := &Entity{Attributes: oldCopy, BylineOrg: true}
	raw = json.RawMessage(`{"author":"Jane Doe"}`)
	in = &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, s.creditUpdate(ctx, org, in))
	testutil.Nil(t, in.BylinePersonID)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":"Acme"`), "%s", *in.Attributes)

	// Explicit fields still change the credit.
	in = &UpdateEntityInput{BylinePersonID: &jane}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.Equal(t, jane, *in.BylinePersonID)
}

// An uncredited entity still adopts the credit its author text names.
func TestCreditUpdate_UncreditedTextCredits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeBylines{}
	s := &Service{bylines: f}
	existing := &Entity{Attributes: json.RawMessage(`{"content":"x"}`)}
	raw := json.RawMessage(`{"author":"Carol"}`)
	in := &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.RequireNotNil(t, in.BylinePersonID)
	testutil.Equal(t, f.people["Carol"], *in.BylinePersonID)
}

// Non-string author values are left alone and a missing resolver is safe.
func TestSyncBylineText_Guards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeBylines{}
	bob, _ := f.Resolve(ctx, "Bob")
	raw := json.RawMessage(`{"author":{"x":1},"author_name":"Old"}`)
	in := &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, (&Service{bylines: f}).syncBylineText(ctx, &Entity{}, in, bob.PersonID))
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":{"x":1}`), "%s", *in.Attributes)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author_name":"Bob"`), "%s", *in.Attributes)

	in = &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, (&Service{}).syncBylineText(ctx, &Entity{}, in, bob.PersonID))
}

// Picking a person explicitly rewrites the stored text, so it cannot win later.
func TestCreditUpdate_ExplicitBylineRewritesText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeBylines{}
	s := &Service{bylines: f}
	_, _ = f.Resolve(ctx, "Jane Doe")
	bob, _ := f.Resolve(ctx, "Bob")
	existing := &Entity{Attributes: json.RawMessage(`{"author":"Jane Doe","content":"x"}`)}

	in := &UpdateEntityInput{BylinePersonID: bob.PersonID}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.RequireNotNil(t, in.Attributes)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":"Bob"`), "%s", *in.Attributes)
	testutil.True(t, strings.Contains(string(*in.Attributes), `"content":"x"`), "%s", *in.Attributes)

	on := true
	in = &UpdateEntityInput{BylineOrg: &on}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.True(t, strings.Contains(string(*in.Attributes), `"author":"Acme"`), "%s", *in.Attributes)
}

// Switching to the organisation clears author_id too: it mirrors the person for
// a rollback, which would otherwise show the old person again.
func TestCreditUpdate_OrganisationClearsAuthorMirror(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeBylines{}
	s := &Service{bylines: f}
	bob, _ := f.Resolve(ctx, "Bob")
	mirror, _ := f.Mirror(ctx, *bob.PersonID)
	existing := &Entity{Attributes: json.RawMessage(`{"author":"Bob"}`), BylinePersonID: bob.PersonID, AuthorID: mirror}

	on := true
	in := &UpdateEntityInput{BylineOrg: &on}
	testutil.RequireNoError(t, s.creditUpdate(ctx, existing, in))
	testutil.True(t, in.ClearAuthor && in.AuthorID == nil, "author_id cleared: %+v", in)
	testutil.True(t, in.ClearBylinePerson, "person cleared")

	// The same through the byline text of an uncredited entity ("Acme" is the organisation).
	raw := json.RawMessage(`{"author":"Acme"}`)
	in = &UpdateEntityInput{Attributes: &raw}
	testutil.RequireNoError(t, s.creditUpdate(ctx, &Entity{}, in))
	testutil.True(t, in.ClearAuthor && in.ClearBylinePerson, "text switch to the organisation clears the mirror: %+v", in)

	// A new entity credited to the organisation carries no author row.
	legacy := uuid.New()
	create := &CreateEntityInput{BylineOrg: true, AuthorID: &legacy}
	testutil.RequireNoError(t, s.creditCreate(ctx, create))
	testutil.True(t, create.AuthorID == nil, "author_id kept on an organisation create")
}

// UpdateEntity (the path builder_save, the page editor and plugins share) applies
// the credited-entity rule before the repository sees the input.
func TestService_UpdateEntity_CreditedAuthorTextIsOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sr, er := newTestService()
	f := &fakeBylines{}
	svc.SetBylines(f)
	schema := seedSchema(t, sr)
	bob, _ := f.Resolve(ctx, "Bob")
	existing := seedEntity(t, er, schema)
	existing.BylinePersonID = bob.PersonID

	attrs := json.RawMessage(`{"title":"Hello","body":"Edited","author":"Jane Doe"}`)
	got, err := svc.UpdateEntity(ctx, existing.ID, &testUserID, &UpdateEntityInput{Attributes: &attrs})
	testutil.RequireNoError(t, err)
	testutil.True(t, strings.Contains(string(got.Attributes), `"author":"Bob"`), "%s", got.Attributes)
}
