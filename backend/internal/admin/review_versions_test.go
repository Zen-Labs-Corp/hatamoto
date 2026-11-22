package admin

import (
	"encoding/json/jsontext"
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
