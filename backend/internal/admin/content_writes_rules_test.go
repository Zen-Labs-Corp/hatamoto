package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/user"
)

// contentWritesRulesUpload posts one replacement file to target as the session token tok.
func contentWritesRulesUpload(t *testing.T, c *contentRig, target, tok string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, map[string][]byte{"fresh.png": png1x1}, true)
	req := httptest.NewRequest("POST", target, bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", ct)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: tok})
	c.addFetchMeta(req, tok, map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// contentWritesRulesMedia adds a file uploaded by u (nil: unknown uploader) and returns its id.
func contentWritesRulesMedia(c *contentRig, n int, u *user.User) uuid.UUID {
	m := c.media.add(n, "file-"+uuid.NewString()[:4]+".png", "image/png", "")
	if u != nil {
		m.UploadedBy = &u.ID
	}
	return m.ID
}

func contentWritesRulesHasMedia(c *contentRig, id uuid.UUID) bool {
	_, err := c.media.Get(context.Background(), id)
	return err == nil
}

func TestContentWritesRulesVersionRestoreAndSEO(t *testing.T) {
	c, users := newRolesRig(t)
	author, other := users[user.RoleAuthor], users[user.RoleReviewer]
	tok := c.freshToken(author.ID)

	theirs := c.entityBy(other, entity.StatusDraft)
	mine := c.entityBy(author, entity.StatusDraft)
	for _, e := range []*entity.Entity{theirs, mine} {
		c.ents.versions[e.ID] = []*entity.EntityVersion{{EntityID: e.ID, Version: 1, Attributes: []byte(`{"summary":"old"}`)}}
	}

	restore := func(e *entity.Entity) int {
		return c.do("POST", "/admin/entities/"+e.ID.String()+"/versions/1/restore", tok, url.Values{}, nil).Code
	}
	if got := restore(theirs); got != 403 {
		t.Errorf("author restores a version of another's entity = %d, want 403", got)
	}
	if got := restore(mine); got != 303 {
		t.Errorf("author restores a version of own entity = %d, want 303", got)
	}

	seo := func(e *entity.Entity) int {
		return c.do("POST", "/admin/entities/"+e.ID.String()+"/seo", tok,
			url.Values{"orig_title": {""}, "title": {"A new title"}}, map[string]string{"Accept": "application/json"}).Code
	}
	if got := seo(theirs); got != 403 {
		t.Errorf("author SEO inline on another's entity = %d, want 403", got)
	}
	if got := seo(mine); got != 200 {
		t.Errorf("author SEO inline on own entity = %d, want 200", got)
	}
}

func TestContentWritesRulesMediaUpdateReplaceDelete(t *testing.T) {
	c, users := newRolesRig(t)
	author, other := users[user.RoleAuthor], users[user.RoleReviewer]
	tok := c.freshToken(author.ID)
	theirs := contentWritesRulesMedia(c, 61, other)
	mine := contentWritesRulesMedia(c, 62, author)
	path := func(id uuid.UUID, tail string) string { return "/admin/media/" + id.String() + tail }
	alt := url.Values{"alt_text": {"x"}}

	if got := c.do("POST", path(theirs, ""), tok, alt, nil).Code; got != 403 {
		t.Errorf("update of another's media = %d, want 403", got)
	}
	if got := c.do("POST", path(mine, ""), tok, alt, nil).Code; got != 303 {
		t.Errorf("update of own media = %d, want 303", got)
	}
	if got := contentWritesRulesUpload(t, c, path(theirs, "/replace"), tok).Code; got != 403 {
		t.Errorf("replace of another's media = %d, want 403", got)
	}
	if got := contentWritesRulesUpload(t, c, path(mine, "/replace"), tok).Code; got != 303 {
		t.Errorf("replace of own media = %d, want 303", got)
	}
	if len(c.media.replaced) != 1 || c.media.replaced[0] != mine {
		t.Errorf("replaced = %v, want only the author's file", c.media.replaced)
	}
	if got := c.do("POST", path(theirs, "/delete"), tok, url.Values{}, nil).Code; got != 403 || !contentWritesRulesHasMedia(c, theirs) {
		t.Errorf("delete of another's media = %d (kept %v), want 403 and kept", got, contentWritesRulesHasMedia(c, theirs))
	}
	if got := c.do("POST", path(mine, "/delete"), tok, url.Values{}, nil).Code; got != 303 || contentWritesRulesHasMedia(c, mine) {
		t.Errorf("delete of own media = %d, want 303 and gone", got)
	}
}

func TestContentWritesRulesContributorDeletesOwnMedia(t *testing.T) {
	c, users := newRolesRig(t)
	contrib := users[user.RoleContributor]
	mine := contentWritesRulesMedia(c, 63, contrib)
	rec := c.do("POST", "/admin/media/"+mine.String()+"/delete", c.freshToken(contrib.ID), url.Values{}, nil)
	if rec.Code != 303 || contentWritesRulesHasMedia(c, mine) {
		t.Errorf("contributor deletes own media = %d, want 303 and gone", rec.Code)
	}
}

func TestContentWritesRulesBulkSkipsOthersMedia(t *testing.T) {
	c, users := newRolesRig(t)
	author, other := users[user.RoleAuthor], users[user.RoleReviewer]
	tok := c.freshToken(author.ID)
	theirs := contentWritesRulesMedia(c, 64, other)
	mine := contentWritesRulesMedia(c, 65, author)

	rec := c.do("POST", "/admin/media/bulk", tok, url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {mine.String(), theirs.String()}}, nil)
	if rec.Code != 303 {
		t.Fatalf("bulk delete = %d, want 303", rec.Code)
	}
	if contentWritesRulesHasMedia(c, mine) || !contentWritesRulesHasMedia(c, theirs) {
		t.Errorf("bulk delete: own kept %v, other's kept %v; want own gone, other's kept", contentWritesRulesHasMedia(c, mine), contentWritesRulesHasMedia(c, theirs))
	}
	body := c.next(rec, "/admin/media")
	if !strings.Contains(body, "1 item was skipped: you can change only media you uploaded.") {
		t.Error("no skipped note after the bulk delete")
	}

	mine = contentWritesRulesMedia(c, 66, author)
	rec = c.do("POST", "/admin/media/bulk", tok, url.Values{"action": {"move"}, "new_folder": {"Press"}, "id": {mine.String(), theirs.String()}}, nil)
	var moved, left *media.Media
	moved, _ = c.media.Get(context.Background(), mine)
	left, _ = c.media.Get(context.Background(), theirs)
	if moved.Folder != "/Press" || left.Folder != "/" {
		t.Errorf("bulk move folders: own %q, other's %q; want /Press and /", moved.Folder, left.Folder)
	}
	if body := c.next(rec, "/admin/media"); !strings.Contains(body, "1 item was skipped") {
		t.Error("no skipped note after the bulk move")
	}
}
