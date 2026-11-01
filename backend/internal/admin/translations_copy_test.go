package admin

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

// copyRig is groupedRig with the copier wired: Alpha (en, original), Beta
// (fr) and Gamma (es) are linked; it (hidden) has no copy.
func copyRig(t *testing.T) (*contentRig, *fakeCopier) {
	t.Helper()
	c, _ := groupedRig(t)
	cp := newFakeCopier()
	c.a.deps.Content.Copier = cp
	return c, cp
}

var fieldNames = regexp.MustCompile(`<(?:input|select|textarea)[^>]* name="([^"]+)"`)

// formNames are the sorted distinct field names of the markup between from and to.
func formNames(t *testing.T, body, from, to string) []string {
	t.Helper()
	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("no %q in the page", from)
	}
	rest := body[i:]
	if j := strings.Index(rest, to); j >= 0 {
		rest = rest[:j]
	}
	var names []string
	for _, m := range fieldNames.FindAllStringSubmatch(rest, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	slices.Sort(names)
	return names
}

func TestTranslationsTabOffersCreateOnlyWithoutCopy(t *testing.T) {
	t.Parallel()
	c, _ := copyRig(t)
	body := c.get(t, "owner-token", translationsTab, "")
	has(t, body, `id="create-copy-it"`, `data-hm-open="create-copy-it"`, trBase+"/new?locale=it", `action="`+trBase+`/copy"`,
		"Create a it copy of", "Create copy")
	createButton(t, body, "create-copy-it")
	lacks(t, body, `id="create-copy-fr"`, `id="create-copy-en"`)
	if body := c.get(t, "editor-token", translationsTab, ""); !strings.Contains(body, `id="create-copy-it"`) {
		t.Error("an editor gets no Create copy")
	}
}

func TestTranslationsTabNoCreateWithoutCopierOrOnSingleLocale(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t) // no Copier
	lacks(t, c.get(t, "owner-token", translationsTab, ""), "create-copy", "Create copy")
	single, _ := translationsRig(t)
	single.a.deps.Content.Copier = newFakeCopier()
	lacks(t, single.get(t, "owner-token", translationsTab, ""), "create-copy", "Create copy")
}

// TestCopyDialogAndPageShareFields is the UI-10 check: the dialog and the /new
// page render one partial, so they post the same fields.
func TestCopyDialogAndPageShareFields(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	cp.missing = []entity.MissingParent{{ID: fid(20), Name: "Company", Slug: "company"}}
	// Parents only show on a nested schema with a parent; make Alpha one.
	c.ents.ents[fid(11)].ParentID = ptr(fid(14))
	c.ents.schemas[0].Settings = []byte(`{"url_pattern":"/services/{path}"}`)
	tab := c.get(t, "owner-token", translationsTab, "")
	page := c.get(t, "owner-token", trBase+"/new?locale=it", "")
	dialog := formNames(t, tab, `id="copy-it-form"`, `</form>`)
	pageForm := formNames(t, page, `class="hm-cardform"`, `</form>`)
	if want := []string{"_csrf", "from", "locale", "name", "parents", "publish", "slug"}; !slices.Equal(dialog, want) {
		t.Errorf("dialog fields = %v, want %v", dialog, want)
	}
	if !slices.Equal(dialog, pageForm) {
		t.Errorf("dialog fields %v differ from page fields %v", dialog, pageForm)
	}
	has(t, page, "Company")
	const help = "Copies the last saved version. Save first to include your latest edits."
	has(t, tab, help)
	has(t, page, help)
}

func TestCopyNewPageUnknownLocale(t *testing.T) {
	t.Parallel()
	c, _ := copyRig(t)
	if rec := c.do("GET", trBase+"/new?locale=zz", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown locale = %d, want 404", rec.Code)
	}
}

func TestCopyCreatesAuditsAndOpensTheCopy(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	rows := c.record()
	purged := 0
	c.a.deps.Invalidate = func(context.Context) { purged++ }
	cp.warn = "Terms were not copied."
	rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"Alfa"}, "slug": {"alfa"}, "from": {"original"}, "parents": {"true"}, "publish": {"true"}})
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/entities/") {
		t.Fatalf("copy: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(cp.calls) != 1 {
		t.Fatalf("CreateCopy called %d times", len(cp.calls))
	}
	o := cp.calls[0].Opts
	if cp.calls[0].Src != fid(11) || o.Locale != "it" || o.Name != "Alfa" || o.Slug != "alfa" || o.From != entity.CopyFromOriginal ||
		!o.CreateParents || !o.Publish || o.DefaultLocale != "en" {
		t.Errorf("CreateCopy(%v, %+v)", cp.calls[0].Src, o)
	}
	wantAudit(t, rows(), "entity.create_copy")
	if purged != 1 {
		t.Errorf("cache purged %d times, want 1", purged)
	}
	has(t, c.next(rec, rec.Header().Get("Location")+"/translations"), "Created and published the it copy.", "Terms were not copied.")
}

func TestCopyDraftByDefault(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"Alfa"}, "slug": {"alfa"}, "from": {"this"}, "publish": {"false"}})
	if len(cp.calls) != 1 || cp.calls[0].Opts.Publish || cp.calls[0].Opts.CreateParents {
		t.Errorf("calls = %+v", cp.calls)
	}
}

func TestCopySlugClashRendersNewPage422(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	cp.errFor[fid(11)] = entity.ErrSlugAlreadyExists
	rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"Alfa"}, "slug": {"alfa"}, "from": {"this"}})
	if rec.Code != 422 {
		t.Fatalf("slug clash = %d, want 422", rec.Code)
	}
	has(t, rec.Body.String(), "That address is already used in it", `value="alfa"`)
}

func TestCopyRejections(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {""}, "slug": {"alfa"}, "from": {"this"}}); rec.Code != 422 {
		t.Errorf("empty title = %d, want 422", rec.Code)
	}
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"A"}, "slug": {"a"}, "from": {"bogus"}}); rec.Code != 422 {
		t.Errorf("bad start = %d, want 422", rec.Code)
	}
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"zz"}, "name": {"A"}, "slug": {"a"}, "from": {"this"}}); rec.Code != 303 {
		t.Errorf("unknown locale = %d, want redirect", rec.Code)
	}
	cp.errFor[fid(11)] = entity.ErrLocaleTaken
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"A"}, "slug": {"a"}, "from": {"this"}}); rec.Code != 303 {
		t.Errorf("locale taken = %d, want redirect", rec.Code)
	}
	cp.errFor[fid(11)] = errors.New("boom")
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"A"}, "slug": {"a"}, "from": {"this"}}); rec.Code != 500 {
		t.Errorf("failure = %d, want 500", rec.Code)
	}
}

func TestCopyOffWithoutCopier(t *testing.T) {
	t.Parallel()
	c, _ := groupedRig(t)
	if rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"A"}, "slug": {"a"}}); rec.Code != 404 {
		t.Errorf("no copier = %d, want 404", rec.Code)
	}
	if rec := c.do("GET", trBase+"/new?locale=it", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("no copier GET = %d, want 404", rec.Code)
	}
}

// createButton asserts the Create opener of dialogID is a visible button that
// keeps its no-JS address, not an item of a row menu.
func createButton(t *testing.T, body, dialogID string) {
	t.Helper()
	re := regexp.MustCompile(`<a class="hm-btn [^"]*" href="[^"]*/new\?locale=[^"]*" aria-label="Create [^"]* copy" data-hm-open="` + regexp.QuoteMeta(dialogID) + `"`)
	if !re.MatchString(body) {
		t.Errorf("no visible Create button opening %s", dialogID)
	}
}

// A copy whose publishing failed is a draft: the audit and the flash say so.
func TestCopyPublishFailureIsNotReportedAsPublished(t *testing.T) {
	t.Parallel()
	c, cp := copyRig(t)
	cp.publishFails = true
	rows := c.record()
	rec := c.post(trBase+"/copy", url.Values{"locale": {"it"}, "name": {"Alfa"}, "slug": {"alfa"}, "from": {"original"}, "publish": {"true"}})
	if rec.Code != 303 {
		t.Fatalf("copy: %d", rec.Code)
	}
	body := c.next(rec, rec.Header().Get("Location")+"/translations")
	has(t, body, "Created the it copy.")
	lacks(t, body, "published")
	for _, r := range rows() {
		if r.Detail["published"] == true {
			t.Errorf("audit says published: %v", r.Detail)
		}
	}
}
