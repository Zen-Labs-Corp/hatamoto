package admin

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

func TestReviewVersionsRestoreIsStaged(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusPublished)
	// Give the entry an earlier version to restore.
	changed := []byte(`{"summary":"changed"}`)
	if _, err := c.ents.UpdateEntity(t.Context(), e.ID, nil, &entity.UpdateEntityInput{Attributes: (*jsontext.Value)(&changed)}); err != nil {
		t.Fatal(err)
	}
	path := "/admin/entities/" + e.ID.String() + "/versions/1/restore"
	tok := c.freshToken(author.ID)
	events := c.record()

	// Review off: the restore is written and audited, as before.
	rec := c.do("POST", path, tok, url.Values{}, nil)
	if rec.Code != 303 {
		t.Fatalf("restore = %d\n%s", rec.Code, rec.Body)
	}
	wantRestoreRows(t, events(), 1)
	if got, _ := c.ents.GetEntity(t.Context(), e.ID); !strings.Contains(string(got.Attributes), "The first") && !strings.Contains(string(got.Attributes), `"s"`) {
		t.Fatalf("attributes after an unheld restore: %s", got.Attributes)
	}

	// Review holds: nothing is restored and no restore is audited.
	*held = true
	before, _ := c.ents.GetEntity(t.Context(), e.ID)
	attrs, version := string(before.Attributes), before.Version
	rec = c.do("POST", path, tok, url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/entities/"+e.ID.String() {
		t.Fatalf("held restore = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	has(t, c.next(rec, "/admin/entities/"+e.ID.String()), "Sent for review. It goes live when a reviewer approves it.")
	lacks(t, c.next(rec, "/admin/entities/"+e.ID.String()), "Restored version")
	wantRestoreRows(t, events(), 1)
	if got, _ := c.ents.GetEntity(t.Context(), e.ID); string(got.Attributes) != attrs || got.Version != version {
		t.Errorf("the held restore changed the entry: %s v%d", got.Attributes, got.Version)
	}
}

func TestReviewSEOInlineIsStaged(t *testing.T) {
	rv := &fakeReviewFlow{held: true}
	c, users, held := newReviewEditorRig(t, rv)
	*held = true
	author := users[user.RoleAuthor]
	e := c.entityBy(author, entity.StatusPublished)
	tok := c.freshToken(author.ID)
	form := url.Values{"title": {"Staged title"}, "orig_title": {""}, "description": {""}, "orig_description": {""}, "orig_noindex": {""}}
	hdr := map[string]string{"Accept": "application/json"}

	rec := c.do("POST", "/admin/entities/"+e.ID.String()+"/seo", tok, form, hdr)
	if rec.Code != 200 {
		t.Fatalf("seo = %d: %s", rec.Code, rec.Body)
	}
	if r := decodeSEOReply(t, rec); r.Status != "pending_review" || r.Message != "Sent for review." {
		t.Errorf("reply = %+v", r)
	}
	if got, _ := c.ents.GetEntity(t.Context(), e.ID); strings.Contains(string(got.Attributes), "Staged title") {
		t.Errorf("the live row changed: %s", got.Attributes)
	}

	// Without JSON the person is redirected with a notice, not an error.
	rec = c.do("POST", "/admin/entities/"+e.ID.String()+"/seo", tok, form, nil)
	if rec.Code != 303 {
		t.Fatalf("form seo = %d", rec.Code)
	}
	has(t, c.next(rec, "/admin/entities?view=seo"), "Sent for review.")
}

// wantRestoreRows checks how many restore rows were audited so far.
func wantRestoreRows(t *testing.T, got []AuditEvent, n int) {
	t.Helper()
	count := 0
	for _, e := range got {
		if e.Action == "entity.restore_version" {
			count++
		}
	}
	if count != n {
		t.Errorf("restore audit rows = %d, want %d (%v)", count, n, got)
	}
}

// ownPendingFlow is a review flow that holds, where the viewer is the requester of
// a change already waiting for every entry.
type ownPendingFlow struct{ fakeReviewFlow }

func (f *ownPendingFlow) Pending(ctx context.Context, id string) (PendingEdit, bool, error) {
	return PendingEdit{ID: 7, EntityID: id, Requester: userFrom(ctx).ID}, true, nil
}

// A partial edit built from the live row would supersede the requester's own
// waiting change and delete it: it is refused and nothing is staged.
func TestReviewOwnPendingIsNotReplacedByPartialEdits(t *testing.T) {
	t.Run("inline SEO", func(t *testing.T) {
		c, users, held := newReviewEditorRig(t, &fakeReviewFlow{held: true})
		c.a.deps.Review = &ownPendingFlow{fakeReviewFlow{held: true}}
		*held = true
		author := users[user.RoleAuthor]
		e := c.entityBy(author, entity.StatusPublished)
		form := url.Values{"title": {"Typed title"}, "orig_title": {""}, "description": {""}, "orig_description": {""}, "orig_noindex": {""}}
		rec := c.do("POST", "/admin/entities/"+e.ID.String()+"/seo", c.freshToken(author.ID), form, map[string]string{"Accept": "application/json"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("seo = %d, want 409: %s", rec.Code, rec.Body)
		}
		r := decodeSEOReply(t, rec)
		if r.Status != "error" || !strings.Contains(r.Message, "change waiting for review") || r.Values.Title != "Typed title" {
			t.Errorf("reply = %+v", r)
		}
	})
	t.Run("node restore", func(t *testing.T) {
		r := newVersionsRig(t)
		r.a.deps.Review = &ownPendingFlow{fakeReviewFlow{held: true}}
		code, body := r.restore("owner-token", "2", "stats", baseForm("3"), nil)
		if code != http.StatusConflict || !strings.Contains(body, "change waiting for review") {
			t.Fatalf("restore = %d %s", code, body)
		}
		if v := r.ents.ents[r.page.ID].Version; v != 3 {
			t.Errorf("version = %d, the refused restore wrote", v)
		}
	})
}
