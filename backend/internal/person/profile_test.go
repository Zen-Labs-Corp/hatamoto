package person

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
)

func TestComplete(t *testing.T) {
	cases := []struct {
		name string
		p    *Person
		want bool
	}{
		{"nil", nil, false},
		{"name and photo", &Person{Name: "Ada", Photo: "/a.jpg"}, true},
		{"no photo", &Person{Name: "Ada"}, false},
		{"blank name", &Person{Name: "  ", Photo: "/a.jpg"}, false},
		{"blank photo", &Person{Name: "Ada", Photo: " "}, false},
	}
	for _, c := range cases {
		if got := Complete(c.p); got != c.want {
			t.Errorf("%s: Complete = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProfileInputValidate(t *testing.T) {
	good := ProfileInput{Name: " Ada ", Photo: " /a.jpg ", LinkedIn: "https://linkedin.com/in/ada"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good input: %v", err)
	}
	if good.Name != "Ada" || good.Photo != "/a.jpg" {
		t.Errorf("input not trimmed: %+v", good)
	}
	bad := ProfileInput{
		Name:     strings.Repeat("é", 121),
		JobTitle: strings.Repeat("x", 121),
		ShortBio: strings.Repeat("x", 301),
		LinkedIn: "linkedin.com/in/ada",
	}
	err := bad.Validate()
	if err == nil {
		t.Fatal("bad input accepted")
	}
	for _, f := range []string{"name", "photo", "job_title", "short_bio", "linkedin"} {
		if err.Fields[f] == "" {
			t.Errorf("no error for %s: %v", f, err.Fields)
		}
	}
	// 120 runes is fine even when it is more than 120 bytes.
	ok := ProfileInput{Name: strings.Repeat("é", 120), Photo: "/a.jpg"}
	if err := ok.Validate(); err != nil {
		t.Errorf("120 runes refused: %v", err)
	}
	if err := (&ProfileInput{}).Validate(); err == nil || err.Fields["name"] == "" || err.Fields["photo"] == "" {
		t.Errorf("empty input: %v", err)
	}
}

// fakeWorld is an in-memory entity service and person store.
type fakeWorld struct {
	ents    map[uuid.UUID]*entity.Entity
	links   map[uuid.UUID]uuid.UUID // user to person
	creates int
}

func newWorld() *fakeWorld {
	return &fakeWorld{ents: map[uuid.UUID]*entity.Entity{}, links: map[uuid.UUID]uuid.UUID{}}
}

func (w *fakeWorld) SchemaID(context.Context) (uuid.UUID, error) { return uuid.New(), nil }

func (w *fakeWorld) Get(_ context.Context, id uuid.UUID) (*Person, error) {
	e, ok := w.ents[id]
	if !ok {
		return nil, ErrNotFound
	}
	return fromAttributes(e.ID, e.Slug, e.Name, e.Attributes), nil
}

func (w *fakeWorld) ForUser(ctx context.Context, u uuid.UUID) (*Person, error) {
	id, ok := w.links[u]
	if !ok {
		return nil, ErrNotFound
	}
	return w.Get(ctx, id)
}

func (w *fakeWorld) Link(_ context.Context, u, p uuid.UUID) error {
	if _, ok := w.links[u]; ok {
		return ErrAlreadyLinked
	}
	w.links[u] = p
	return nil
}

func (w *fakeWorld) GetEntity(_ context.Context, id uuid.UUID) (*entity.Entity, error) {
	e, ok := w.ents[id]
	if !ok {
		return nil, entity.ErrEntityNotFound
	}
	return e, nil
}

func (w *fakeWorld) CreateEntity(_ context.Context, _ *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	for _, e := range w.ents {
		if e.Slug == in.Slug {
			return nil, entity.ErrSlugAlreadyExists
		}
	}
	w.creates++
	e := &entity.Entity{ID: uuid.New(), Name: in.Name, Slug: in.Slug, Attributes: in.Attributes, Status: entity.StatusDraft}
	w.ents[e.ID] = e
	return e, nil
}

func (w *fakeWorld) UpdateEntity(_ context.Context, id uuid.UUID, _ *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	e := w.ents[id]
	if in.Name != nil {
		e.Name = *in.Name
	}
	if in.Slug != nil {
		for _, o := range w.ents {
			if o.ID != id && o.Slug == *in.Slug {
				return nil, entity.ErrSlugAlreadyExists
			}
		}
		e.Slug = *in.Slug
	}
	if in.Attributes != nil {
		e.Attributes = *in.Attributes
		// The entity service derives the status from public.
		var a struct{ Public bool }
		_ = json.Unmarshal(e.Attributes, &a)
		e.Status = entity.StatusDraft
		if a.Public {
			e.Status = entity.StatusPublished
			if e.PublishedAt == nil {
				now := time.Now()
				e.PublishedAt = &now
			}
		}
	}
	return e, nil
}

func (w *fakeWorld) UpdateEntityStatus(_ context.Context, id uuid.UUID, _ *uuid.UUID, s entity.EntityStatus) (*entity.Entity, error) {
	w.ents[id].Status = s
	return w.ents[id], nil
}

func (w *fakeWorld) DeleteEntity(_ context.Context, id uuid.UUID) error {
	delete(w.ents, id)
	return nil
}

func newTestProfiles(w *fakeWorld) *Profiles {
	return &Profiles{profiles: w, entities: w}
}

// The person is made from the typed name on the first save, never from the
// email or the account name, and only once.
func TestSaveMakesThePersonFromTheTypedName(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	p := newTestProfiles(w)
	user := uuid.New()

	first, err := p.Save(ctx, user, ProfileInput{Name: "Alex Carter", Photo: "/a.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "Alex Carter" || first.Slug != "alex-carter" || first.Public {
		t.Errorf("new person = %+v, want the typed name and slug, not public", first)
	}
	if w.ents[first.ID].Status != entity.StatusDraft {
		t.Errorf("status = %s, want draft (status follows public)", w.ents[first.ID].Status)
	}
	if _, err := p.Save(ctx, user, ProfileInput{Name: "Alex Carter", Photo: "/a.jpg"}); err != nil || w.creates != 1 {
		t.Errorf("second save: %v; creates = %d, want 1", err, w.creates)
	}

	// A second user with the same name gets the next free slug.
	other, err := p.Save(ctx, uuid.New(), ProfileInput{Name: "Alex Carter", Photo: "/b.jpg"})
	if err != nil || other.Slug != "alex-carter-2" {
		t.Errorf("slug collision = %+v, %v", other, err)
	}
	// Invalid input makes nothing.
	n := w.creates
	if _, err := p.Save(ctx, uuid.New(), ProfileInput{Name: "No Photo"}); err == nil || w.creates != n {
		t.Errorf("invalid save: %v; creates %d -> %d", err, n, w.creates)
	}
}

// While a person has never been public the address follows the saved name; once
// it has been public it stays.
func TestSaveRederivesTheSlugUntilFirstPublic(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	p := newTestProfiles(w)
	user := uuid.New()
	first, err := p.Save(ctx, user, ProfileInput{Name: "Ada", Photo: "/a.jpg"})
	if err != nil || first.Slug != "ada" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	got, err := p.Save(ctx, user, ProfileInput{Name: "Ada Lovelace", Photo: "/a.jpg"})
	if err != nil || got.Slug != "ada-lovelace" {
		t.Fatalf("renamed while hidden = %+v, %v; want ada-lovelace", got, err)
	}
	got, err = p.Save(ctx, user, ProfileInput{Name: "Ada Lovelace", Photo: "/a.jpg", Public: true})
	if err != nil || got.Slug != "ada-lovelace" {
		t.Fatalf("made public = %+v, %v", got, err)
	}
	got, err = p.Save(ctx, user, ProfileInput{Name: "Ada King", Photo: "/a.jpg", Public: true})
	if err != nil || got.Slug != "ada-lovelace" {
		t.Errorf("renamed after public = %+v, %v; the address must stay", got, err)
	}
}

func TestSaveWritesProfileAndKeepsOtherAttributes(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	p := newTestProfiles(w)
	user := uuid.New()
	cur, err := p.ensureForUser(ctx, user, "Ada")
	if err != nil {
		t.Fatal(err)
	}
	w.ents[cur.ID].Attributes = json.RawMessage(`{"public":false,"bio":"<p>Long</p>","github":"https://g.example/ada"}`)

	if _, err := p.Save(ctx, user, ProfileInput{Name: "Ada L", Photo: ""}); err == nil {
		t.Fatal("missing photo saved")
	} else {
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Fields["photo"] == "" {
			t.Fatalf("error = %v", err)
		}
	}
	if w.ents[cur.ID].Name != "Ada" {
		t.Error("an invalid save wrote the name")
	}

	got, err := p.Save(ctx, user, ProfileInput{
		Name: "Ada L", Photo: "/a.jpg", JobTitle: "CTO", ShortBio: "Hi", LinkedIn: "https://linkedin.com/in/ada", Public: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ada L" || got.Photo != "/a.jpg" || got.JobTitle != "CTO" || !got.Public || got.Links["linkedin"] == "" {
		t.Errorf("saved = %+v", got)
	}
	if got.Bio != "<p>Long</p>" || got.Links["github"] == "" {
		t.Errorf("other attributes lost: %+v", got)
	}
	// Clearing an optional field removes it.
	got, err = p.Save(ctx, user, ProfileInput{Name: "Ada L", Photo: "/a.jpg"})
	if err != nil || got.JobTitle != "" || got.Public || got.Links["linkedin"] != "" {
		t.Errorf("cleared = %+v, %v", got, err)
	}
}

func TestCreateMakesAnUnlinkedPublishedPerson(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	p := newTestProfiles(w)
	got, err := p.Create(ctx, nil, "  Grace Hopper ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Grace Hopper" || got.Slug != "grace-hopper" || got.Public {
		t.Errorf("created = %+v", got)
	}
	if w.ents[got.ID].Status != entity.StatusDraft {
		t.Errorf("status = %s, want draft", w.ents[got.ID].Status)
	}
	if len(w.links) != 0 {
		t.Errorf("Create linked a user: %v", w.links)
	}
	again, err := p.Create(ctx, nil, "Grace Hopper")
	if err != nil || again.Slug != "grace-hopper-2" {
		t.Errorf("same name again = %+v, %v", again, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 121)} {
		var verr *ValidationError
		if _, err := p.Create(ctx, nil, bad); !errors.As(err, &verr) || verr.Fields["name"] == "" {
			t.Errorf("Create(%q) = %v, want a name error", bad, err)
		}
	}
}
