package admin

import (
	"context"
	"net/http/httptest"

	"encoding/json/jsontext"
	"github.com/google/uuid"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// racingEntities answers conditional writes the way the store does and lets a
// test slip another writer in between the handler's read and its write.
type racingEntities struct {
	*fakeEntities
	races int // writes that find another version stored
}

func (r *racingEntities) UpdateEntity(ctx context.Context, id uuid.UUID, actor *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	if in.ExpectedVersion != nil {
		r.mu.Lock()
		e := r.ents[id]
		if r.races > 0 {
			r.races--
			e.Version++
		}
		cur := e.Version
		r.mu.Unlock()
		if *in.ExpectedVersion != cur {
			return nil, &entity.VersionConflictError{Current: cur}
		}
	}
	return r.fakeEntities.UpdateEntity(ctx, id, actor, in)
}

func (c *contentRig) seedAttrs(n int, attrs string) {
	c.ents.ents[fid(n)].Attributes = jsontext.Value(attrs)
}

func (c *contentRig) seoPost(n int, token string, form url.Values, json bool) *httptest.ResponseRecorder {
	hdr := map[string]string{}
	if json {
		hdr["Accept"] = "application/json"
	}
	return c.do("POST", "/admin/entities/"+fid(n).String()+"/seo", token, form, hdr)
}

func decodeSEOReply(t *testing.T, rec *httptest.ResponseRecorder) seoInlineReply {
	t.Helper()
	var out seoInlineReply
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("reply %d is not JSON: %v\n%s", rec.Code, err, rec.Body)
	}
	return out
}

func TestEntitySEOInlineSavesOnlySEO(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.seedAttrs(11, `{"summary":"The first","featured":true,"canonical_url":"https://example.com/a","og_image":"/i.png","seo":{"robots":"nofollow"}}`)
	rows := c.record()
	form := url.Values{"title": {"Alpha for search"}, "orig_title": {""}, "description": {""}, "orig_description": {""}, "orig_noindex": {""}}
	rec := c.seoPost(11, "editor-token", form, true)
	if rec.Code != 200 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if r := decodeSEOReply(t, rec); r.Status != "saved" || r.Values.Title != "Alpha for search" || r.Counters["title"].Len != 16 {
		t.Errorf("reply = %+v", r)
	}
	stored := c.ents.ents[fid(11)]
	attrs := attrsMap(stored.Attributes)
	if attrs["meta_title"] != "Alpha for search" || attrs["canonical_url"] != "https://example.com/a" || attrs["og_image"] != "/i.png" || attrs["summary"] != "The first" || attrs["featured"] != true {
		t.Errorf("attrs = %v", attrs)
	}
	if robots := str(attrs["seo"].(map[string]any), "robots"); robots != "nofollow" {
		t.Errorf("robots = %q", robots)
	}
	if stored.Name != "Alpha service" || stored.Slug != "alpha" || stored.Status != entity.StatusPublished {
		t.Errorf("entity = %q %q %s", stored.Name, stored.Slug, stored.Status)
	}
	got := rows()
	if len(got) != 1 || got[0].Action != "entity.update" || got[0].Actor != "ed@example.com" {
		t.Fatalf("audit = %+v", got)
	}
	if d := got[0].Detail; d["inline"] != true || d["bulk"] != false || strings.Join(d["fields"].([]string), ",") != "title" || d["name"] != "Alpha service" {
		t.Errorf("detail = %v", d)
	}
}

func TestEntitySEOInlineNoIndexAndNoJS(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	form := url.Values{"noindex": {"true"}, "orig_noindex": {""}, "back": {"/admin/entities?view=seo&page=2"}}
	rec := c.seoPost(11, "owner-token", form, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities?view=seo&page=2" {
		t.Fatalf("no-JS save = %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	if attrsMap(c.ents.ents[fid(11)].Attributes)["no_index"] != true {
		t.Error("noindex not stored")
	}
	// Switch it off again: the box is absent from the post, the loaded copy says it was on.
	rec = c.seoPost(11, "owner-token", url.Values{"orig_noindex": {"true"}}, true)
	if rec.Code != 200 || decodeSEOReply(t, rec).Values.NoIndex {
		t.Fatalf("off = %d %s", rec.Code, rec.Body)
	}
	if _, ok := attrsMap(c.ents.ents[fid(11)].Attributes)["no_index"]; ok {
		t.Error("noindex still stored")
	}
}

func TestEntitySEOInlineConflictKeepsOtherFields(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.seedAttrs(11, `{"summary":"The first","meta_title":"Theirs"}`)
	rows := c.record()
	// The row was loaded with an empty title; someone has since set "Theirs".
	form := url.Values{"title": {"Mine"}, "orig_title": {""}, "description": {"Short and sweet"}, "orig_description": {""}}
	rec := c.seoPost(11, "owner-token", form, true)
	if rec.Code != 409 {
		t.Fatalf("conflict = %d", rec.Code)
	}
	r := decodeSEOReply(t, rec)
	if r.Status != "conflict" || r.Conflicts["title"] != "Theirs" || r.Values.Title != "Theirs" || r.Values.Description != "Short and sweet" {
		t.Errorf("reply = %+v", r)
	}
	attrs := attrsMap(c.ents.ents[fid(11)].Attributes)
	if attrs["meta_title"] != "Theirs" || attrs["meta_description"] != "Short and sweet" {
		t.Errorf("attrs = %v", attrs)
	}
	if got := rows()[0].Detail["fields"].([]string); strings.Join(got, ",") != "description" {
		t.Errorf("audited fields = %v", got)
	}
	// Only a conflict, or nothing changed: no write, no audit.
	n := len(rows())
	v := c.ents.ents[fid(11)].Version
	c.seoPost(11, "owner-token", url.Values{"title": {"Mine"}, "orig_title": {""}}, true)
	c.seoPost(11, "owner-token", url.Values{"title": {"Theirs"}, "orig_title": {"Theirs"}}, true)
	if len(rows()) != n || c.ents.ents[fid(11)].Version != v {
		t.Errorf("a no-op post wrote: %d rows, version %d", len(rows())-n, c.ents.ents[fid(11)].Version)
	}
}

func TestEntitySEOInlineRetriesVersionRace(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	race := &racingEntities{fakeEntities: c.ents, races: 2}
	c.a.deps.Content.Entities = race
	form := url.Values{"title": {"Raced"}, "orig_title": {""}}
	if rec := c.seoPost(11, "owner-token", form, true); rec.Code != 200 {
		t.Fatalf("two races then a write = %d: %s", rec.Code, rec.Body)
	}
	if attrsMap(c.ents.ents[fid(11)].Attributes)["meta_title"] != "Raced" {
		t.Error("retry did not store the title")
	}
	// Three races in a row: reported, never forced through.
	race.races = 3
	rec := c.seoPost(11, "owner-token", url.Values{"title": {"Forced"}, "orig_title": {"Raced"}}, true)
	if rec.Code != 409 || decodeSEOReply(t, rec).Status != "error" {
		t.Fatalf("exhausted retries = %d: %s", rec.Code, rec.Body)
	}
	if attrsMap(c.ents.ents[fid(11)].Attributes)["meta_title"] != "Raced" {
		t.Error("a lost race overwrote the newer version")
	}
	wantAudit(t, rows(), "entity.update")
}

func TestEntitySEOInlineRefusals(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rows := c.record()
	rec := c.seoPost(11, "owner-token", url.Values{"title": {strings.Repeat("x", 300)}, "orig_title": {""}}, true)
	if rec.Code != 422 || decodeSEOReply(t, rec).Status != "error" {
		t.Errorf("too long = %d: %s", rec.Code, rec.Body)
	}
	// A type that has no keyword field never stores one.
	rec = c.seoPost(11, "owner-token", url.Values{"keyword": {"go"}, "orig_keyword": {""}}, true)
	if rec.Code != 200 || len(rows()) != 0 {
		t.Errorf("keyword on a type without it: %d, %d audit rows", rec.Code, len(rows()))
	}
	if rec := c.seoPost(99, "owner-token", url.Values{"title": {"x"}, "orig_title": {""}}, true); rec.Code != 404 {
		t.Errorf("missing entity = %d", rec.Code)
	}
	if rec := c.seoPost(11, "", url.Values{"title": {"x"}}, true); rec.Code == 200 {
		t.Errorf("signed-out post = %d", rec.Code)
	}
}
