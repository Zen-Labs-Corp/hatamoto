package pluginhost

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// filterEntities records the filter of the last List and honours an exact term.
type filterEntities struct {
	*locEntities
	last *entity.ListEntitiesFilter
	// tagged maps an entity to the terms it carries.
	tagged map[uuid.UUID][]uuid.UUID
}

func (f *filterEntities) List(_ context.Context, flt *entity.ListEntitiesFilter, _ *entity.KeysetPage) (*entity.PagedResult[*entity.Entity], error) {
	f.last = flt
	var items []*entity.Entity
	for _, e := range f.locEntities.list {
		if flt.TermID != nil && !slices.Contains(f.tagged[e.ID], *flt.TermID) {
			continue
		}
		items = append(items, e)
	}
	return &entity.PagedResult[*entity.Entity]{Items: items}, nil
}

// fakeTermRepo serves terms by slug and entity terms from memory.
type fakeTermRepo struct {
	taxonomy.Repository
	bySlug   map[string]*taxonomy.Term
	assigned map[uuid.UUID][]taxonomy.EntityTermRef
	batches  int
	err      error
}

func (f *fakeTermRepo) GetTermBySlug(_ context.Context, kind, slug string) (*taxonomy.Term, error) {
	if t, ok := f.bySlug[kind+"/"+slug]; ok {
		return t, nil
	}
	return nil, taxonomy.ErrTermNotFound
}

func (f *fakeTermRepo) TermsForEntities(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]taxonomy.EntityTermRef, error) {
	f.batches++
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID][]taxonomy.EntityTermRef{}
	for _, id := range ids {
		if refs, ok := f.assigned[id]; ok {
			out[id] = refs
		}
	}
	return out, nil
}

type termsRig struct {
	api    *API
	ents   *filterEntities
	terms  *fakeTermRepo
	grants *GrantStore
	page   *entity.EntitySchema
	news   *taxonomy.Term
}

func newTermsRig(t *testing.T) *termsRig {
	t.Helper()
	page := &entity.EntitySchema{ID: uuid.New(), Slug: "page", Name: "Page", Schema: jsontext.Value(`{"type":"object"}`)}
	fe := &filterEntities{locEntities: &locEntities{}, tagged: map[uuid.UUID][]uuid.UUID{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &termsRig{ents: fe, page: page, grants: NewGrantStore(nil), news: &taxonomy.Term{ID: uuid.New(), Kind: "category", Slug: "news", Name: "News"}}
	r.terms = &fakeTermRepo{
		bySlug:   map[string]*taxonomy.Term{"category/news": r.news},
		assigned: map[uuid.UUID][]taxonomy.EntityTermRef{},
	}
	svc := entity.NewService(&locSchemas{schemas: []*entity.EntitySchema{page}}, fe, log)
	r.api = NewAPI(Services{Entities: svc, Terms: taxonomy.NewService(r.terms, log)}, r.grants, nil, log)
	return r
}

func (r *termsRig) seed() *entity.Entity {
	return r.ents.add(&entity.Entity{ID: uuid.New(), SchemaID: r.page.ID, Name: "n", Slug: "s-" + uuid.NewString()[:8],
		Locale: "en-XX", Status: entity.StatusDraft, Attributes: jsontext.Value(`{}`), Version: 1})
}

func (r *termsRig) call(t *testing.T, fn func(context.Context, *API, *callEnv, jsontext.Value) (any, error), sdkRange string, grant bool, args any) (map[string]any, error) {
	t.Helper()
	perms := []sdk.Permission{sdk.PermEntitiesRead}
	if grant {
		perms = append(perms, sdk.PermTermsRead)
	}
	if err := r.grants.Set(context.Background(), "p", perms); err != nil {
		t.Fatal(err)
	}
	raw, _ := jsonx.MarshalPinned(args)
	res, err := fn(context.Background(), r.api, &callEnv{plugin: "p", manifest: sdk.Manifest{SDK: sdkRange}}, raw)
	if err != nil {
		return nil, err
	}
	b, _ := jsonx.MarshalPinned(res)
	var out map[string]any
	if err := jsonx.UnmarshalPinned(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// withoutTerms returns the result with every "terms" key removed: what a
// plugin that cannot see terms must receive, byte for byte in content.
func withoutTerms(res map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range res {
		if k == "terms" {
			continue
		}
		if items, ok := v.([]any); ok {
			cp := make([]any, len(items))
			for i, it := range items {
				if m, ok := it.(map[string]any); ok {
					cp[i] = withoutTerms(m)
				} else {
					cp[i] = it
				}
			}
			v = cp
		}
		out[k] = v
	}
	return out
}

func TestEntitiesGetTerms(t *testing.T) {
	r := newTermsRig(t)
	e, bare := r.seed(), r.seed()
	r.terms.assigned[e.ID] = []taxonomy.EntityTermRef{{Kind: "category", Slug: "news", Name: "News"}, {Kind: "tag", Slug: "go", Name: "Go"}}

	got, err := r.call(t, fnEntitiesGet, "^0.20", true, map[string]any{"id": e.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"kind": "category", "slug": "news", "name": "News"},
		map[string]any{"kind": "tag", "slug": "go", "name": "Go"},
	}
	if !reflect.DeepEqual(got["terms"], want) {
		t.Errorf("terms = %v, want %v", got["terms"], want)
	}

	// An entitled plugin reads "no terms" as an empty list, not a missing key.
	got, err = r.call(t, fnEntitiesGet, "^0.20", true, map[string]any{"id": bare.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if terms, ok := got["terms"].([]any); !ok || len(terms) != 0 {
		t.Errorf("terms of an entity without terms = %#v, want []", got["terms"])
	}

	full, err := r.call(t, fnEntitiesGet, "^0.20", true, map[string]any{"id": e.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	wantBare := withoutTerms(full)
	for name, c := range map[string]struct {
		sdk   string
		grant bool
	}{
		"older sdk with grant":  {"^0.19", true},
		"0.20 without grant":    {"^0.20", false},
		"older sdk, no grant":   {"^0.5", false},
		"unparsable sdk range ": {"garbage", true},
	} {
		got, err := r.call(t, fnEntitiesGet, c.sdk, c.grant, map[string]any{"id": e.ID.String()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, wantBare) {
			t.Errorf("%s: result = %v, want %v", name, got, wantBare)
		}
	}
}

// A plugin that is not entitled gets the bytes it always got.
func TestEntitiesGetTermsAbsentBytes(t *testing.T) {
	r := newTermsRig(t)
	e := r.seed()
	r.terms.assigned[e.ID] = []taxonomy.EntityTermRef{{Kind: "tag", Slug: "go", Name: "Go"}}
	if err := r.grants.Set(context.Background(), "p", []sdk.Permission{sdk.PermEntitiesRead, sdk.PermTermsRead}); err != nil {
		t.Fatal(err)
	}
	raw, _ := jsonx.MarshalPinned(map[string]any{"id": e.ID.String()})
	res, err := fnEntitiesGet(context.Background(), r.api, &callEnv{plugin: "p", manifest: sdk.Manifest{SDK: "^0.19"}}, raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := jsonx.MarshalPinned(res)
	if bytes.Contains(b, []byte("terms")) {
		t.Errorf("^0.19 result mentions terms: %s", b)
	}
	if r.terms.batches != 0 {
		t.Errorf("terms loaded %d times for a plugin that cannot see them", r.terms.batches)
	}
}

func TestEntitiesListTerms(t *testing.T) {
	r := newTermsRig(t)
	a, b, c := r.seed(), r.seed(), r.seed()
	r.terms.assigned[a.ID] = []taxonomy.EntityTermRef{{Kind: "category", Slug: "news", Name: "News"}}
	r.terms.assigned[b.ID] = []taxonomy.EntityTermRef{{Kind: "tag", Slug: "go", Name: "Go"}}
	_ = c

	got, err := r.call(t, fnEntitiesList, "^0.20", true, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	items := got["items"].([]any)
	if len(items) != 3 || r.terms.batches != 1 {
		t.Fatalf("items = %d, term batches = %d, want 3 and 1", len(items), r.terms.batches)
	}
	for i, want := range []int{1, 1, 0} {
		if terms, ok := items[i].(map[string]any)["terms"].([]any); !ok || len(terms) != want {
			t.Errorf("item %d terms = %#v, want %d entries", i, items[i].(map[string]any)["terms"], want)
		}
	}

	wantBare := withoutTerms(got)
	for name, c := range map[string]struct {
		sdk   string
		grant bool
	}{"^0.19 with grant": {"^0.19", true}, "^0.20 without grant": {"^0.20", false}} {
		r.terms.batches = 0
		got, err = r.call(t, fnEntitiesList, c.sdk, c.grant, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, wantBare) {
			t.Errorf("%s: result = %v, want %v", name, got, wantBare)
		}
		if r.terms.batches != 0 {
			t.Errorf("%s: %d terms queries, want 0", name, r.terms.batches)
		}
	}
}

// The term filter is gated by terms.read alone, whatever the sdk range.
func TestEntitiesListTermFilterOlderSDK(t *testing.T) {
	r := newTermsRig(t)
	a, _ := r.seed(), r.seed()
	r.ents.tagged[a.ID] = []uuid.UUID{r.news.ID}

	got, err := r.call(t, fnEntitiesList, "^0.19", true, map[string]any{"term": map[string]any{"kind": "category", "slug": "news"}})
	if err != nil {
		t.Fatal(err)
	}
	if f := r.ents.last; f.TermID == nil || *f.TermID != r.news.ID || !f.TermExact {
		t.Errorf("filter = %+v, want the news term matched exactly", f)
	}
	items := got["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != a.ID.String() {
		t.Errorf("items = %v, want only the tagged entity", items)
	}
	if _, ok := items[0].(map[string]any)["terms"]; ok {
		t.Error("^0.19 item carries terms")
	}
}

func TestEntitiesListTermFilter(t *testing.T) {
	r := newTermsRig(t)
	a, b := r.seed(), r.seed()
	r.ents.tagged[a.ID] = []uuid.UUID{r.news.ID}
	_ = b

	got, err := r.call(t, fnEntitiesList, "^0.20", true, map[string]any{"term": map[string]any{"kind": "category", "slug": "news"}})
	if err != nil {
		t.Fatal(err)
	}
	if f := r.ents.last; f.TermID == nil || *f.TermID != r.news.ID || !f.TermExact {
		t.Errorf("filter = %+v, want the news term matched exactly", f)
	}
	if items := got["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != a.ID.String() {
		t.Errorf("items = %v, want only the tagged entity", items)
	}

	// An unknown term is an empty page with the usual shape.
	r.ents.last = nil
	got, err = r.call(t, fnEntitiesList, "^0.20", true, map[string]any{"term": map[string]any{"kind": "category", "slug": "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if items, ok := got["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %#v, want []", got["items"])
	}
	if v, ok := got["next_cursor"]; !ok || v != nil {
		t.Errorf("next_cursor = %#v, want present and null", v)
	}
	if r.ents.last != nil {
		t.Error("an unknown term still listed entities")
	}

	// The filter works for any sdk range once terms.read is held.
	if _, err := r.call(t, fnEntitiesList, "^0.19", true, map[string]any{"term": map[string]any{"kind": "category", "slug": "news"}}); err != nil {
		t.Errorf("^0.19 with grant: %v", err)
	}

	_, err = r.call(t, fnEntitiesList, "^0.20", false, map[string]any{"term": map[string]any{"kind": "category", "slug": "news"}})
	wantCode(t, err, sdk.ErrCapabilityDenied)
	var he *sdk.HostError
	if errors.As(err, &he) && he.Capability != sdk.PermTermsRead {
		t.Errorf("capability = %q, want %q", he.Capability, sdk.PermTermsRead)
	}

	_, err = r.call(t, fnEntitiesList, "^0.20", true, map[string]any{"term": map[string]any{"kind": "Bad Kind", "slug": "x"}})
	wantCode(t, err, sdk.ErrInvalidArgument)
}

func TestEntitiesTermsLookupFailure(t *testing.T) {
	r := newTermsRig(t)
	e := r.seed()
	r.terms.err = errors.New("db down")
	_, err := r.call(t, fnEntitiesGet, "^0.20", true, map[string]any{"id": e.ID.String()})
	wantCode(t, err, sdk.ErrInternal)
	_, err = r.call(t, fnEntitiesList, "^0.20", true, map[string]any{})
	wantCode(t, err, sdk.ErrInternal)
}

// An unknown term must not hide a bad schema or status.
func TestEntitiesListUnknownTermStillValidates(t *testing.T) {
	r := newTermsRig(t)
	term := map[string]any{"kind": "category", "slug": "nope"}
	_, err := r.call(t, fnEntitiesList, "^0.20", true, map[string]any{"term": term, "schema_slug": "missing"})
	if !errors.Is(err, entity.ErrSchemaNotFound) {
		t.Errorf("unknown schema: err = %v, want ErrSchemaNotFound", err)
	}
	_, err = r.call(t, fnEntitiesList, "^0.20", true, map[string]any{"term": term, "status": "bogus"})
	wantCode(t, err, sdk.ErrInvalidArgument)
}
