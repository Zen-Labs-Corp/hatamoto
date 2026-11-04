package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// fakeDuplicator copies through the fake entity store, as the service does:
// name "Copy of X", slug "x-copy", people and blocks refused.
type fakeDuplicator struct {
	ents *fakeEntities
}

func (f *fakeDuplicator) Duplicate(ctx context.Context, _ uuid.UUID, id uuid.UUID) (*entity.Entity, error) {
	src, err := f.ents.GetEntity(ctx, id)
	if err != nil {
		return nil, err
	}
	if !entity.CanDuplicate(src.Schema) {
		return nil, entity.ErrDuplicateUnsupported
	}
	return f.ents.CreateEntity(ctx, nil, &entity.CreateEntityInput{
		SchemaID: src.SchemaID, Name: "Copy of " + src.Name, Slug: src.Slug + "-copy", Attributes: src.Attributes})
}

func TestEntityDuplicateRedirectsToTheCopyAndAudits(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	rec := c.post("/admin/entities/"+fid(14).String()+"/duplicate", url.Values{})
	if rec.Code != 303 {
		t.Fatalf("duplicate = %d\n%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	cp := c.ents.ents[fid(101)]
	if cp == nil || cp.Name != "Copy of About us" || cp.Status != entity.StatusDraft {
		t.Fatalf("copy = %+v", cp)
	}
	if want := "/admin/entities/" + cp.ID.String() + "/edit"; loc != want {
		t.Errorf("redirect = %q, want %q", loc, want)
	}
	wantAudit(t, rows(), "entity.duplicate")
	d := rows()[0]
	if d.Target != cp.ID.String() || d.Detail["from"] != fid(14).String() || d.Detail["from_name"] != "About us" || d.Detail["slug"] != "about-copy" {
		t.Errorf("audit = %+v", d)
	}
	has(t, c.next(rec, loc), "Copied. This is a draft copy of")
}

func TestEntityDuplicateOfAPersonIsRefusedWithAFlash(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	person := c.ents.addSchema(9, "Person", "person", `{"type":"object"}`, `{}`, true)
	c.ents.addEntity(70, person, "Ada", "ada", entity.StatusPublished, `{}`, t0)
	rec := c.post("/admin/entities/"+fid(70).String()+"/duplicate", url.Values{})
	if rec.Code != 303 {
		t.Fatalf("duplicate person = %d", rec.Code)
	}
	has(t, c.next(rec, rec.Header().Get("Location")), "cannot be duplicated")
	if len(rows()) != 0 || len(c.ents.ents) != 5 {
		t.Errorf("audit %v, entities %d: a refusal must write nothing", rows(), len(c.ents.ents))
	}
}

func TestEntityDuplicateNeedsAnEditor(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.editor.Role = "viewer"
	if rec := c.do("POST", "/admin/entities/"+fid(14).String()+"/duplicate", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("viewer = %d, want 403", rec.Code)
	}
	if len(c.ents.ents) != 4 {
		t.Errorf("a viewer created a copy")
	}
}

func TestEntityListRowMenuOffersDuplicate(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	if n := strings.Count(body, `action="/admin/entities/`+fid(14).String()+`/duplicate"`); n != 1 {
		t.Errorf("duplicate forms for About us = %d, want 1", n)
	}
	has(t, body, "Duplicate")

	c.a.deps.Content.Duplicator = nil
	lacks(t, c.get(t, "owner-token", "/admin/entities", ""), "/duplicate")
}

type failingDuplicator struct{ err error }

func (f failingDuplicator) Duplicate(context.Context, uuid.UUID, uuid.UUID) (*entity.Entity, error) {
	return nil, f.err
}

func TestEntityDuplicateDomainFailuresFlashInsteadOfA500(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"validation": entity.ErrValidationFailed,
		"plugin":     entity.ErrRejectedByPlugin,
		"doc":        entity.ErrInvalidDoc,
		"parent":     entity.ErrParentTooDeep,
	} {
		c := newContentRig(t)
		c.a.deps.Content.Duplicator = failingDuplicator{err: fmt.Errorf("%w: detail", err)}
		rec := c.post("/admin/entities/"+fid(14).String()+"/duplicate", url.Values{})
		if rec.Code != 303 {
			t.Fatalf("%s: duplicate = %d, want a redirect", name, rec.Code)
		}
		has(t, c.next(rec, rec.Header().Get("Location")), "The copy could not be created")
	}
	c := newContentRig(t)
	c.a.deps.Content.Duplicator = failingDuplicator{err: errors.New("boom")}
	if rec := c.post("/admin/entities/"+fid(14).String()+"/duplicate", url.Values{}); rec.Code != 500 {
		t.Errorf("unknown error = %d, want 500", rec.Code)
	}
}
