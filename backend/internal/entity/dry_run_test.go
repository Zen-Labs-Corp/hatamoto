package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/testutil"
)

func TestDiffAttributes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		before, after string
		want          string
	}{
		{"unchanged", `{"a":1,"b":"x"}`, `{"b":"x","a":1}`, `[]`},
		{"scalar change", `{"a":1,"b":"x"}`, `{"a":2,"b":"x"}`, `[{"path":"a","before":1,"after":2}]`},
		{"nested leaf", `{"hours":{"open":"9","close":"5"}}`, `{"hours":{"open":"10","close":"5"}}`, `[{"path":"hours.open","before":"9","after":"10"}]`},
		{"added key", `{"a":1}`, `{"a":1,"b":2}`, `[{"path":"b","before":null,"after":2}]`},
		{"removed key", `{"a":1,"b":2}`, `{"a":1}`, `[{"path":"b","before":2,"after":null}]`},
		{"array is one change", `{"t":[1,2]}`, `{"t":[1,3]}`, `[{"path":"t","before":[1,2],"after":[1,3]}]`},
		{"object replaced by scalar", `{"o":{"x":1}}`, `{"o":5}`, `[{"path":"o","before":{"x":1},"after":5}]`},
		{"big number is exact", `{"n":9007199254740993}`, `{"n":9007199254740992}`, `[{"path":"n","before":9007199254740993,"after":9007199254740992}]`},
		{"sorted", `{"z":1,"a":1,"m":1}`, `{"z":2,"a":2,"m":2}`, `[{"path":"a","before":1,"after":2},{"path":"m","before":1,"after":2},{"path":"z","before":1,"after":2}]`},
		{"non-object root", `[1]`, `[2]`, `[{"path":"","before":[1],"after":[2]}]`},
		{"empty before", ``, `{"a":1}`, `[{"path":"a","before":null,"after":1}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := diffAttributes(jsontext.Value(tc.before), jsontext.Value(tc.after))
			testutil.RequireNoError(t, err)
			out, err := jsonx.Marshal(got)
			testutil.RequireNoError(t, err)
			if tc.name == "unchanged" || tc.name == "big number is exact" {
				testutil.Equal(t, tc.want, string(out))
				return
			}
			testutil.JSONEqual(t, tc.want, string(out))
		})
	}
}

// countingLinkSync signals on calls for every link-sync method that runs.
type countingLinkSync struct{ calls chan uuid.UUID }

func (c *countingLinkSync) Reindex(_ context.Context, id uuid.UUID) error {
	c.calls <- id
	return nil
}

func (c *countingLinkSync) RewriteForEntity(_ context.Context, id uuid.UUID, _, _ string, _ *uuid.UUID) error {
	c.calls <- id
	return nil
}

func (c *countingLinkSync) ResolveCanonical(e *Entity) (string, error) {
	c.calls <- e.ID
	return "", nil
}

func attrsValue(s string) *jsontext.Value {
	v := jsontext.Value(s)
	return &v
}

func TestDryRunUpdateStoresAndEmitsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr))
	sink := &recordingSink{}
	svc.SetEvents(sink)
	var live, paths atomic.Int32
	svc.SetLiveObserver(func(context.Context, []uuid.UUID) { live.Add(1) })
	svc.SetPathObserver(func(context.Context, *Entity, *Entity, *uuid.UUID) { paths.Add(1) })
	ls := &countingLinkSync{calls: make(chan uuid.UUID, 4)}
	svc.SetLinkSync(ls)

	slug := "changed-slug"
	p, err := svc.DryRunUpdate(ctx, ent.ID, &testUserID, &UpdateEntityInput{Slug: &slug, Attributes: attrsValue(`{"title":"New","body":"World"}`)})
	testutil.RequireNoError(t, err)
	testutil.True(t, p.DryRun)
	testutil.Equal(t, 1, len(p.Changes))
	testutil.Equal(t, "title", p.Changes[0].Path)
	testutil.Equal(t, 1, ent.Version)
	testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(ent.Attributes))
	testutil.NotEqual(t, slug, ent.Slug)
	testutil.Equal(t, 0, len(sink.kinds()))
	testutil.Equal(t, int32(0), live.Load())
	testutil.Equal(t, int32(0), paths.Load())
	select {
	case <-ls.calls:
		t.Fatal("link sync ran on a dry run")
	case <-time.After(50 * time.Millisecond):
	}

	// Control: the real write drives every observer, so the zero counts above mean something.
	_, err = svc.UpdateEntity(ctx, ent.ID, &testUserID, &UpdateEntityInput{Slug: &slug, Attributes: attrsValue(`{"title":"New","body":"World"}`)})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(sink.kinds()))
	select {
	case <-ls.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("control: link sync did not run on the real write")
	}
}

func TestDryRunUpdateHeldStagesNothing(t *testing.T) {
	g, gate, _ := gateRig(t, true)
	me := uuid.New()
	e := g.entity(t, &me, StatusPublished)
	attrs := attrsValue(`{"title":"New","body":"W"}`)
	p, err := g.svc.DryRunUpdate(asRole("author", me), e.ID, &me, &UpdateEntityInput{Attributes: attrs})
	testutil.RequireNoError(t, err)
	testutil.True(t, p.PendingReview)
	testutil.Equal(t, 0, len(gate.subs))
	testutil.Equal(t, 0, g.repo.updated)
}

func TestDryRunUpdateSanitisedAfter(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	schema := seedSchema(t, sr, func(s *EntitySchema) { s.Schema = jsontext.Value(`{}`) })
	ent := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = jsontext.Value(`{"content":"<p>old</p>","other":"x"}`) })
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, &UpdateEntityInput{
		Attributes: attrsValue(`{"content":"<p>new</p><script>alert(1)</script>","other":"x"}`),
	})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Changes))
	testutil.Equal(t, "content", p.Changes[0].Path)
	after, _ := p.Changes[0].After.(string)
	testutil.NotContains(t, after, "<script")
	testutil.Contains(t, after, "new")
}

func TestDryRunUpdateValidationErrorIsAnswer(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr, func(s *EntitySchema) { s.Schema = strictSchemaJSON }))
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, &UpdateEntityInput{Attributes: attrsValue(`{"body":"x"}`)})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Errors))
	testutil.Contains(t, p.Errors[0], "attribute validation failed")
	testutil.NotNil(t, p.Changes)
	testutil.Equal(t, 0, len(p.Changes))
	testutil.Equal(t, ent.Version, p.Version)
}

func TestDryRunUpdatePluginRefusalIsAnswer(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr))
	svc.SetBeforeSave(func(context.Context, *BeforeSaveInput) error { return errors.New("no") })
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, &UpdateEntityInput{Attributes: attrsValue(`{"title":"New"}`)})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Errors))
	testutil.True(t, strings.HasPrefix(p.Errors[0], "save rejected by plugin"))
	testutil.Equal(t, 0, len(p.Changes))
}

func TestDryRunUpdateStaleVersion(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr), func(e *Entity) { e.Version = 3 })
	stale := 2
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, &UpdateEntityInput{ExpectedVersion: &stale, Attributes: attrsValue(`{"title":"New"}`)})
	var vc *VersionConflictError
	if !errors.As(err, &vc) || vc.Current != 3 || p != nil {
		t.Fatalf("p = %v, err = %v, want VersionConflictError{Current: 3}", p, err)
	}
}

func TestDryRunUpdateForbiddenRevealsNothing(t *testing.T) {
	g := newAuthzRig(t)
	me, other := uuid.New(), uuid.New()
	e := g.entity(t, &other, StatusDraft)
	// strict-invalid input: an unauthorised caller must get the refusal, not an answer
	p, err := g.svc.DryRunUpdate(asRole("author", me), e.ID, &me, &UpdateEntityInput{Attributes: attrsValue(`{}`)})
	if !errors.Is(err, ErrNotAllowed) || p != nil {
		t.Fatalf("p = %v, err = %v, want ErrNotAllowed", p, err)
	}
}

func TestDryRunUpdateUnchanged(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr))
	name := "Renamed"
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, &UpdateEntityInput{Name: &name})
	testutil.RequireNoError(t, err)
	testutil.NotNil(t, p.Changes)
	testutil.Equal(t, 0, len(p.Changes))
	testutil.Equal(t, 0, len(p.Errors))
	testutil.Equal(t, ent.Version, p.Version)
}

func TestDryRunUpdateMergeShowsMergedResult(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr))
	in := mergeInput(`{"body":"New"}`)
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, in)
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Changes))
	testutil.Equal(t, "body", p.Changes[0].Path)
	testutil.Equal(t, "World", p.Changes[0].Before.(string))
	testutil.Equal(t, "New", p.Changes[0].After.(string))
	testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(ent.Attributes))
	testutil.Equal(t, 1, ent.Version)
	testutil.True(t, !in.MergeAttributes)
}

func TestDryRunUpdateMergeNullIsAnswer(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr))
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, mergeInput("null"))
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Errors))
	testutil.Contains(t, p.Errors[0], "null in a merge patch")
	testutil.Equal(t, 0, len(p.Changes))
}

func TestDryRunUpdateMergeRemovesRequiredIsAnswer(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	ent := seedEntity(t, er, seedSchema(t, sr, func(s *EntitySchema) { s.Schema = strictSchemaJSON }))
	p, err := svc.DryRunUpdate(context.Background(), ent.ID, &testUserID, mergeInput(`{"title":null}`))
	testutil.RequireNoError(t, err)
	testutil.Equal(t, 1, len(p.Errors))
	testutil.Contains(t, p.Errors[0], "title")
	testutil.JSONEqual(t, `{"title":"Hello","body":"World"}`, string(ent.Attributes))
}

// writeCountingBylines counts the calls that write through the byline resolver.
type writeCountingBylines struct {
	fakeBylines
	resolves, mirrors int
}

func (f *writeCountingBylines) Resolve(ctx context.Context, name string) (Byline, error) {
	f.resolves++
	return f.fakeBylines.Resolve(ctx, name)
}

func (f *writeCountingBylines) Mirror(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	f.mirrors++
	return f.fakeBylines.Mirror(ctx, id)
}

func TestDryRunUpdateWritesNoBylines(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	setup := func() (*Service, *Entity, *writeCountingBylines, uuid.UUID) {
		svc, sr, er := newTestService()
		ent := seedEntity(t, er, seedSchema(t, sr))
		f := &writeCountingBylines{}
		svc.bylines = f
		bob, err := f.fakeBylines.Resolve(ctx, "Bob")
		testutil.RequireNoError(t, err)
		return svc, ent, f, *bob.PersonID
	}
	byID := func(id uuid.UUID) *UpdateEntityInput { return &UpdateEntityInput{BylinePersonID: &id} }
	byText := func() *UpdateEntityInput {
		return &UpdateEntityInput{Attributes: attrsValue(`{"title":"Hello","body":"World","author":"Jane Typo"}`)}
	}

	t.Run("byline_person_id", func(t *testing.T) {
		svc, ent, f, bob := setup()
		_, err := svc.DryRunUpdate(ctx, ent.ID, &testUserID, byID(bob))
		testutil.RequireNoError(t, err)
		testutil.Equal(t, 0, f.resolves)
		testutil.Equal(t, 0, f.mirrors)
		_, err = svc.UpdateEntity(ctx, ent.ID, &testUserID, byID(bob))
		testutil.RequireNoError(t, err)
		testutil.True(t, f.mirrors > 0)
	})
	t.Run("new author text", func(t *testing.T) {
		svc, ent, f, _ := setup()
		p, err := svc.DryRunUpdate(ctx, ent.ID, &testUserID, byText())
		testutil.RequireNoError(t, err)
		testutil.Equal(t, 0, len(p.Errors))
		testutil.Equal(t, 0, f.resolves)
		testutil.Equal(t, 0, f.mirrors)
		_, ok := f.people["Jane Typo"]
		testutil.True(t, !ok)
		_, err = svc.UpdateEntity(ctx, ent.ID, &testUserID, byText())
		testutil.RequireNoError(t, err)
		testutil.True(t, f.resolves > 0 && f.mirrors > 0)
	})
}
