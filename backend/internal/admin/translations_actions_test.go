package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"hatamoto.dev/core/internal/entity"
)

const trBase = "/admin/entities/00000000-0000-0000-0000-000000000011/translations"

func TestTranslationLinkAuditsAndInvalidates(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	rows := c.record()
	purged := 0
	c.a.deps.Invalidate = func(context.Context) { purged++ }
	rec := c.post(trBase+"/link", url.Values{"page": {fid(12).String()}})
	if rec.Code != 303 || rec.Header().Get("Location") != trBase {
		t.Fatalf("link: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(g.linked) != 1 || g.linked[0][0] != fid(11) || g.linked[0][1] != fid(12) {
		t.Errorf("Link called with %v, want this page first", g.linked)
	}
	wantAudit(t, rows(), "entity.translation_link")
	if purged != 1 {
		t.Errorf("cache purged %d times, want 1", purged)
	}
	has(t, c.next(rec, trBase), "Linked the page.")
}

func TestTranslationLinkRejectedFlash(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	rows := c.record()
	purged := 0
	c.a.deps.Invalidate = func(context.Context) { purged++ }
	// A clash in the store: flash, no audit, no purge.
	failing := &linkFails{fakeGroups: g}
	c.a.deps.Content.Groups = failing
	rec := c.post(trBase+"/link", url.Values{"page": {fid(12).String()}})
	if rec.Code != 303 {
		t.Fatalf("link: %d", rec.Code)
	}
	has(t, c.next(rec, trBase), "That page cannot be linked")
	if len(rows()) != 0 || purged != 0 {
		t.Errorf("a refused link audited %d, purged %d", len(rows()), purged)
	}
	// A page that already has translations is refused before the store is asked.
	c.a.deps.Content.Groups = g
	g.setGroup(entity.Member{ID: fid(12), Group: fid(900)}, entity.Member{ID: fid(13), Group: fid(900)})
	rec = c.post(trBase+"/link", url.Values{"page": {fid(12).String()}})
	has(t, c.next(rec, trBase), "already has translations")
	if len(g.linked) != 0 {
		t.Errorf("linked a grouped page: %v", g.linked)
	}
	// No page picked.
	rec = c.post(trBase+"/link", url.Values{})
	has(t, c.next(rec, trBase), "Pick the page to link first.")
}

// linkFails makes Link answer err, ErrLocaleTaken when nil.
type linkFails struct {
	*fakeGroups
	err error
}

func (l *linkFails) Link(context.Context, ...uuid.UUID) (uuid.UUID, error) {
	if l.err != nil {
		return uuid.UUID{}, l.err
	}
	return uuid.UUID{}, entity.ErrLocaleTaken
}

// A unique-index violation from the store is a flash, not a 500.
func TestTranslationLinkUniqueViolationFlash(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	c.a.deps.Content.Groups = &linkFails{fakeGroups: g, err: fmt.Errorf("link entities: %w", &pgconn.PgError{Code: "23505"})}
	rec := c.post(trBase+"/link", url.Values{"page": {fid(12).String()}})
	if rec.Code != 303 {
		t.Fatalf("link: %d, want a redirect", rec.Code)
	}
	has(t, c.next(rec, trBase), "That page cannot be linked")
}

func TestTranslationUnlinkConfirmRow(t *testing.T) {
	t.Parallel()
	c, g := groupedRig(t)
	body := c.get(t, "owner-token", trBase, "")
	has(t, body, `data-hm-post="`+trBase+`/unlink?member=`+fid(12).String()+`"`, `href="`+trBase+`/unlink?member=`+fid(12).String()+`"`)
	// The no-JS confirmation page asks first and changes nothing.
	body = c.get(t, "owner-token", trBase+"/unlink?member="+fid(12).String(), "")
	has(t, body, "Unlink this copy", `value="`+fid(12).String()+`"`)
	if len(g.unlinked) != 0 {
		t.Fatal("unlinked by the confirmation page")
	}
	rows := c.record()
	rec := c.post(trBase+"/unlink", url.Values{"member": {fid(12).String()}})
	if rec.Code != 303 || len(g.unlinked) != 1 || g.unlinked[0] != fid(12) {
		t.Fatalf("unlink: %d %v", rec.Code, g.unlinked)
	}
	wantAudit(t, rows(), "entity.translation_unlink")
	// The JS dialog posts the member in the query.
	rec = c.post(trBase+"/unlink?member="+fid(13).String(), nil)
	if rec.Code != 303 || len(g.unlinked) != 2 || g.unlinked[1] != fid(13) {
		t.Fatalf("unlink via query: %d %v", rec.Code, g.unlinked)
	}
	// A page outside the group is refused.
	if rec := c.post(trBase+"/unlink", url.Values{"member": {fid(14).String()}}); rec.Code != 404 {
		t.Errorf("foreign member: %d", rec.Code)
	}
	if rec := c.post(trBase+"/unlink", url.Values{"member": {"nope"}}); rec.Code != 404 {
		t.Errorf("bad member: %d", rec.Code)
	}
}

func TestTranslationOriginalPost(t *testing.T) {
	t.Parallel()
	c, g := groupedRig(t)
	body := c.get(t, "owner-token", trBase, "")
	// Make original is offered on the copies, not on the original.
	has(t, body, `name="member" value="`+fid(12).String()+`"`, "Make original")
	if n := strings.Count(body, ">Make original<"); n != 2 {
		t.Errorf("Make original offered %d times, want 2 (fr and es)", n)
	}
	lacks(t, body, `name="member" value="`+fid(11).String()+`"`)
	// The row-menu form carries the CSRF token (without it the POST is refused with 403).
	has(t, body, `action="`+trBase+`/original"><input type="hidden" name="_csrf" value="`)
	rows := c.record()
	rec := c.post(trBase+"/original", url.Values{"member": {fid(12).String()}})
	if rec.Code != 303 || len(g.originals) != 1 || g.originals[0] != fid(12) {
		t.Fatalf("original: %d %v", rec.Code, g.originals)
	}
	wantAudit(t, rows(), "entity.translation_original")
	has(t, c.next(rec, trBase), "Changed the original.")
}

func TestTranslationActionsRequireEditor(t *testing.T) {
	t.Parallel()
	c, g := groupedRig(t)
	c.editor.Role = "viewer"
	for _, p := range []string{"/link", "/unlink", "/original"} {
		if rec := c.do("POST", trBase+p, "editor-token", url.Values{"page": {fid(12).String()}, "member": {fid(12).String()}}, nil); rec.Code != 403 {
			t.Errorf("viewer POST %s = %d, want 403", p, rec.Code)
		}
	}
	if rec := c.do("GET", trBase+"/candidates", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("viewer candidates = %d, want 403", rec.Code)
	}
	if len(g.linked)+len(g.unlinked)+len(g.originals) != 0 {
		t.Error("a viewer changed links")
	}
	// A viewer may read the page, but sees no link controls.
	rec := c.do("GET", trBase, "editor-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("viewer GET = %d", rec.Code)
	}
	lacks(t, rec.Body.String(), "Link an existing page", "Make original", "Unlink")
}

func TestTranslationCandidatesJSON(t *testing.T) {
	t.Parallel()
	c, g := translationsRig(t, "en", "fr")
	g.candidates = []entity.Candidate{{ID: fid(12), Locale: "fr", Slug: "beta", Name: "Beta service"}}
	rec := c.do("GET", trBase+"/candidates?q=bet", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("candidates: %d", rec.Code)
	}
	var got []pickJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Value != fid(12).String() || got[0].Label != "Beta service" {
		t.Errorf("candidates = %+v, %v", got, err)
	}
	if g.lastQuery != "bet" {
		t.Errorf("query = %q", g.lastQuery)
	}
	g.err = errors.New("boom")
	if rec := c.do("GET", trBase+"/candidates", "owner-token", nil, nil); rec.Code != 500 {
		t.Errorf("store error: %d", rec.Code)
	}
}

func TestTranslationsPageLinkDialog(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	body := c.get(t, "owner-token", trBase, "")
	has(t, body, `id="link-copy"`, `action="`+trBase+`/link"`, `data-src="`+trBase+`/candidates"`, `data-hm-open="link-copy"`, `name="page"`)
	// The form starts with the CSRF field.
	i := strings.Index(body, `id="link-copy-form"`)
	if i < 0 || !strings.HasPrefix(body[i+strings.Index(body[i:], ">")+1:], `<input type="hidden" name="_csrf"`) {
		t.Errorf("link form does not start with the CSRF field: %.200s", body[max(i, 0):])
	}
}
