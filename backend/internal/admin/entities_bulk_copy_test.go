package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

func bulkCopyRig(t *testing.T, tags ...string) (*contentRig, *fakeCopier) {
	t.Helper()
	c := newContentRig(t)
	cp := newFakeCopier()
	c.a.deps.Content.Groups = newFakeGroups()
	c.a.deps.Content.Copier = cp
	site := c.a.deps.Site.(*fakeSite)
	for _, tag := range tags {
		site.cfg.Locales = append(site.cfg.Locales, siteconfig.LocaleEntry{Tag: tag, Segment: strings.ToLower(tag)})
	}
	return c, cp
}

func TestBulkCreateDrafts(t *testing.T) {
	t.Parallel()
	c, cp := bulkCopyRig(t, "en", "pt-BR")
	cp.errFor[fid(12)] = entity.ErrLocaleTaken
	audit := c.record()
	rec := c.post("/admin/entities/bulk", url.Values{"action": {"copy:pt-BR"}, "id": {fid(11).String(), fid(12).String(), fid(13).String()}})
	if rec.Code != 303 {
		t.Fatalf("%d", rec.Code)
	}
	if len(cp.calls) != 3 || !cp.calls[0].Opts.CreateParents || !cp.calls[0].Opts.AutoSlug || cp.calls[0].Opts.Locale != "pt-BR" {
		t.Errorf("calls = %+v", cp.calls)
	}
	for _, call := range cp.calls {
		if call.Opts.From != entity.CopyFromThis {
			t.Errorf("From = %q, want the selected row (%q)", call.Opts.From, entity.CopyFromThis)
		}
	}
	has(t, c.next(rec, "/admin/entities"), "2 drafts created in pt-BR, 1 skipped")
	wantAudit(t, audit(), "entity.create_copy", "entity.create_copy")
}

func TestBulkCreateDraftsOffOnSingleLocale(t *testing.T) {
	t.Parallel()
	c, _ := bulkCopyRig(t)
	lacks(t, c.get(t, "owner-token", "/admin/entities", ""), "Create drafts in")
	c2, _ := bulkCopyRig(t, "en", "pt-BR")
	body := c2.get(t, "owner-token", "/admin/entities", "")
	has(t, body, "Create drafts in", `name="locale"`, `<option value="pt-BR" selected>pt-BR</option>`, `<option value="en">en</option>`)
	if n := strings.Count(body, "Create drafts in"); n != 1 {
		t.Errorf("%d Create drafts buttons, want one for every language", n)
	}
}

func TestBulkCreateDraftsPostsTheChosenLocale(t *testing.T) {
	t.Parallel()
	c, cp := bulkCopyRig(t, "en", "pt-BR")
	rec := c.post("/admin/entities/bulk", url.Values{"action": {"copy"}, "locale": {"pt-BR"}, "id": {fid(11).String()}})
	if rec.Code != 303 || len(cp.calls) != 1 || cp.calls[0].Opts.Locale != "pt-BR" {
		t.Fatalf("%d, calls %+v", rec.Code, cp.calls)
	}
	has(t, c.next(rec, "/admin/entities"), "1 draft created in pt-BR")
	rec = c.post("/admin/entities/bulk", url.Values{"action": {"copy"}, "locale": {"xx"}, "id": {fid(11).String()}})
	has(t, c.next(rec, "/admin/entities"), "Choose an action for the selected entities.")
	if len(cp.calls) != 1 {
		t.Errorf("unknown locale copied: %+v", cp.calls)
	}
}

func TestBulkCreateDraftsNothingCopiedIsAWarning(t *testing.T) {
	t.Parallel()
	c, cp := bulkCopyRig(t, "en", "pt-BR")
	cp.errFor[fid(11)], cp.errFor[fid(12)] = entity.ErrLocaleTaken, entity.ErrLocaleTaken
	rec := c.post("/admin/entities/bulk", url.Values{"action": {"copy:pt-BR"}, "id": {fid(11).String(), fid(12).String()}})
	body := c.next(rec, "/admin/entities")
	has(t, body, "hm-flash--warning", "Nothing copied: all 2 already have a pt-BR copy.")
	lacks(t, body, "0 drafts created", "hm-flash--success")
}
