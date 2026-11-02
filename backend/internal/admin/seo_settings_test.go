package admin

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// fakeSEO keeps the settings in memory and records the schema archive writes.
type fakeSEO struct {
	set     seo.Settings
	merges  int
	patches map[uuid.UUID]entity.SchemaSEOPatch
	builtin map[uuid.UUID]bool
	// failLastPatch makes the last patch of a save fail after the earlier ones
	// were applied; failMerge makes the templates merge fail after every patch
	// went through.
	failLastPatch bool
	failMerge     error
	// appliedBeforeFailure counts the patches written before failLastPatch hit.
	appliedBeforeFailure int
}

func newFakeSEO() *fakeSEO {
	return &fakeSEO{set: seo.Defaults(), patches: map[uuid.UUID]entity.SchemaSEOPatch{}, builtin: map[uuid.UUID]bool{}}
}

func (f *fakeSEO) Get(context.Context) (seo.Settings, error) { return f.set, nil }
func (f *fakeSEO) Merge(_ context.Context, fn func(*seo.Settings)) error {
	next := f.set.Clone()
	fn(&next)
	if errs := next.Validate(); len(errs) > 0 {
		return &seo.ValidationError{Errors: errs}
	}
	f.set = next
	f.merges++
	return nil
}

// SaveTitles is all-or-nothing like the real one: a failing patch or merge
// leaves the patches and the settings as they were.
func (f *fakeSEO) SaveTitles(ctx context.Context, patches []SEOTitlePatch, fn func(*seo.Settings)) error {
	saved := maps.Clone(f.patches)
	for i, p := range patches {
		if f.builtin[p.ID] {
			f.patches = saved
			return &SEOPatchError{ID: p.ID, Err: entity.ErrCannotModifyBuiltin}
		}
		if f.failLastPatch && i == len(patches)-1 {
			f.appliedBeforeFailure = i
			f.patches = saved
			return &SEOPatchError{ID: p.ID, Err: errors.New("write failed")}
		}
		f.patches[p.ID] = p.Patch
	}
	if f.failMerge != nil {
		f.patches = saved
		return f.failMerge
	}
	if err := f.Merge(ctx, fn); err != nil {
		f.patches = saved
		return err
	}
	return nil
}

// SaveSchema records each patch; built-in types take the schema keys.
func (f *fakeSEO) SaveSchema(_ context.Context, patches []SEOSchemaPatch) error {
	for _, p := range patches {
		f.patches[p.ID] = p.Patch
	}
	return nil
}

type seoRig struct {
	*contentRig
	svc     *fakeSEO
	person  *entity.EntitySchema
	post    *entity.EntitySchema
	events  []AuditEvent
	flushed int
}

func newSEORig(t *testing.T, svc *fakeSEO) *seoRig {
	t.Helper()
	c := newContentRig(t)
	s := &seoRig{contentRig: c, svc: svc}
	// A person type is a built-in whose archive values stay editable.
	s.person = c.ents.addSchema(4, "Person", "person", `{"type":"object","properties":{}}`, `{"url_pattern":"/author/{slug}"}`, true)
	if svc != nil {
		c.a.deps.SEO = svc
		svc.builtin[c.page.ID] = true
	}
	c.a.deps.Invalidate = func(context.Context) { s.flushed++ }
	c.a.deps.Audit = func(_ context.Context, e AuditEvent) { s.events = append(s.events, e) }
	return s
}

func (s *seoRig) get(t *testing.T, path string) string {
	t.Helper()
	rec := s.do("GET", path, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	body := rec.Body.String()
	for _, issue := range a11y.Check(body, a11y.Options{Page: true}) {
		// The section heading comes from the settings registry entry for SEO.
		if !strings.Contains(issue, "<h1>") {
			t.Errorf("a11y: %v", issue)
		}
	}
	return body
}

// seoControlTag returns the opening tag of the control posted as name.
func seoControlTag(body, name string) string {
	i := strings.Index(body, `name="`+name+`"`)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<")
	return body[start : i+strings.Index(body[i:], ">")+1]
}

func TestSEOTitlesPageLists(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/titles")
	for _, want := range []string{
		"Separator", "Homepage title", `name="type[service][title]"`, `name="type[recipe][description]"`,
		`name="type[service][archive_title]"`, `name="taxonomy[tag]"`, `name="taxonomy[category]"`,
		`name="archives[search]"`, "Update preview", "Sample title | Acme Site",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// A built-in type other than person shows its archive values read-only
	// and disabled, so they look inert; a custom type's stay editable.
	for _, name := range []string{"archive_title", "archive_description"} {
		if tag := seoControlTag(body, "type[page]["+name+"]"); !strings.Contains(tag, "readonly") || !strings.Contains(tag, "disabled") {
			t.Errorf("built-in %s is not read-only and disabled: %s", name, tag)
		}
		if tag := seoControlTag(body, "type[service]["+name+"]"); strings.Contains(tag, "readonly") || strings.Contains(tag, "disabled") {
			t.Errorf("custom %s is not editable: %s", name, tag)
		}
	}
	if strings.Contains(body, "theme_part") {
		t.Error("reuse types are listed")
	}
}

func TestSEOGeneralPage(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Variables["brand"] = "Acme"
	body := s.get(t, "/admin/settings/seo/general")
	for _, want := range []string{"Acme Site", "%title%", "%searchphrase%", `name="variable_name"`, `value="brand"`, `value="Acme"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestSEOOwnerOnly(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, p := range []string{"/admin/settings/seo/general", "/admin/settings/seo/titles", "/admin/settings/seo/titles/preview"} {
		for _, m := range []string{"GET", "POST"} {
			if m == "GET" && strings.HasSuffix(p, "/preview") {
				continue
			}
			if rec := s.do(m, p, "editor-token", url.Values{"home": {"x"}}, nil); rec.Code != 403 {
				t.Errorf("editor %s %s = %d, want 403", m, p, rec.Code)
			}
		}
	}
	if s.svc.merges != 0 {
		t.Error("editor save reached the service")
	}
}

func TestSEOUnavailable(t *testing.T) {
	s := newSEORig(t, nil)
	for _, p := range []string{"/admin/settings/seo/general", "/admin/settings/seo/titles"} {
		if rec := s.do("GET", p, "owner-token", nil, nil); rec.Code != 503 {
			t.Errorf("GET %s without service = %d, want 503", p, rec.Code)
		}
	}
}

func goodTitlesForm() url.Values {
	return url.Values{
		"separator": {"-"}, "home": {"%sitename% %sep% %tagline%"},
		"archives[archive]": {"%archive% %sep% %sitename%"}, "archives[author]": {"%author% %sep% %sitename%"},
		"archives[date]": {"%title% %sep% %sitename%"}, "archives[search]": {"Results for %searchphrase%"},
		"archives[notfound]":   {"%title% %sep% %sitename%"},
		"type[service][title]": {"%title% %sep% Services"}, "type[service][description]": {"Read about %title%."},
		"type[service][archive_title]": {"All services"}, "type[service][archive_description]": {"What we do"},
		"type[recipe][title]": {""}, "type[recipe][description]": {""},
		"type[page][archive_title]": {"ignored"},
		"taxonomy[tag]":             {"Tagged %term% %sep% %sitename%"}, "taxonomy[category]": {""},
	}
}

// personTitlesForm adds the person row the page posts, with a new archive title.
func personTitlesForm(form url.Values, archiveTitle string) {
	form.Set("type[person][title]", "")
	form.Set("type[person][description]", "")
	form.Set("type[person][archive_title]", archiveTitle)
}

func TestSEOTitlesSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", goodTitlesForm(), nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/titles" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set
	if got.Separator != "-" || got.Home != "%sitename% %sep% %tagline%" || got.Archives.Search != "Results for %searchphrase%" {
		t.Fatalf("saved = %+v", got)
	}
	if got.Types["service"] != (seo.TypeTemplates{Title: "%title% %sep% Services", Description: "Read about %title%."}) {
		t.Errorf("service = %+v", got.Types["service"])
	}
	if _, ok := got.Types["recipe"]; ok {
		t.Error("an empty row was stored")
	}
	if got.Taxonomy["tag"] != "Tagged %term% %sep% %sitename%" || got.Taxonomy["category"] != "" {
		t.Errorf("taxonomy = %v", got.Taxonomy)
	}
	p, ok := s.svc.patches[s.service.ID]
	if !ok || *p.ArchiveTitle != "All services" || *p.ArchiveDescription != "What we do" {
		t.Errorf("archive patch = %+v", p)
	}
	if len(s.svc.patches) != 1 {
		t.Errorf("patches = %d, want only the changed non-built-in type", len(s.svc.patches))
	}
	if s.flushed != 1 {
		t.Errorf("page cache invalidated %d times, want 1", s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
}

func TestSEOTitlesUnchangedArchiveIsNotRewritten(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	form := goodTitlesForm()
	form.Del("type[service][archive_title]")
	form.Del("type[service][archive_description]")
	if rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	if len(s.svc.patches) != 0 {
		t.Errorf("patches = %v, want none", s.svc.patches)
	}
}

func TestSEOTitlesRejectsInvalidInput(t *testing.T) {
	for name, edit := range map[string]func(url.Values){
		"unknown variable": func(f url.Values) { f.Set("type[service][title]", "%title% %nope%") },
		"bad separator":    func(f url.Values) { f.Set("separator", "~") },
		"long archive": func(f url.Values) {
			f.Set("type[service][archive_title]", strings.Repeat("x", entity.MaxArchiveTitleLen+1))
		},
		"unknown home var":  func(f url.Values) { f.Set("home", "%bogus%") },
		"taxonomy variable": func(f url.Values) { f.Set("taxonomy[tag]", "%cv_missing%") },
	} {
		s := newSEORig(t, newFakeSEO())
		form := goodTitlesForm()
		edit(form)
		rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", form, nil)
		if rec.Code != 422 {
			t.Errorf("%s: status %d, want 422", name, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "problem") || !strings.Contains(body, "hm-field--invalid") {
			t.Errorf("%s: no error shown", name)
		}
		if s.svc.merges != 0 || len(s.svc.patches) != 0 || s.flushed != 0 {
			t.Errorf("%s: something was saved", name)
		}
	}
}

// A refused archive write is an error, never a silent partial save.
func TestSEOTitlesRefusedArchiveWriteSavesNothing(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.builtin[s.service.ID] = true
	rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", goodTitlesForm(), nil)
	if rec.Code != 500 {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	if s.svc.merges != 0 || s.flushed != 0 {
		t.Error("settings saved after a refused write")
	}
}

func TestSEOTitlesPreviewUsesTheTypedTemplates(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	form := goodTitlesForm()
	rec := s.do("POST", "/admin/settings/seo/titles/preview", "owner-token", form, nil)
	if rec.Code != 200 {
		t.Fatalf("preview = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Sample title - Services", "Read about Sample title.", "Tagged Sample term - Acme Site", "Results for sample"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %q", want)
		}
	}
	if s.svc.merges != 0 || len(s.svc.patches) != 0 || s.flushed != 0 {
		t.Error("preview saved something")
	}
	// The preview line is the resolver's own output.
	set := seo.Defaults()
	set.Separator = "-"
	set.Types["service"] = seo.TypeTemplates{Title: "%title% %sep% Services"}
	want := seo.Resolve(&set, seo.Input{Kind: seo.KindSingle, SchemaSlug: "service", Vars: seo.Vars{Title: "Sample title", SiteName: "Acme Site"}}).Title
	if !strings.Contains(body, want) {
		t.Errorf("preview differs from seo.Resolve: want %q", want)
	}
}

func TestSEOGeneralSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	form := url.Values{"variable_name": {"brand", "", "tagline_long"}, "variable_value": {"Acme", "", "Making things"}}
	rec := s.do("POST", "/admin/settings/seo/general", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/general" {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if got := s.svc.set.Variables; len(got) != 2 || got["brand"] != "Acme" || got["tagline_long"] != "Making things" {
		t.Fatalf("variables = %v", got)
	}
	if s.flushed != 1 || len(s.events) != 1 {
		t.Errorf("flushed %d, events %d", s.flushed, len(s.events))
	}
}

func TestSEOGeneralRejectsBadVariables(t *testing.T) {
	for name, form := range map[string]url.Values{
		"bad name":  {"variable_name": {"Bad Name"}, "variable_value": {"x"}},
		"duplicate": {"variable_name": {"a", "a"}, "variable_value": {"x", "y"}},
		"too long":  {"variable_name": {"a"}, "variable_value": {strings.Repeat("x", seo.MaxVariableLen+1)}},
	} {
		s := newSEORig(t, newFakeSEO())
		rec := s.do("POST", "/admin/settings/seo/general", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), "hm-field--invalid") {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if s.svc.merges != 0 {
			t.Errorf("%s: saved", name)
		}
	}
}

func TestSEOPagesGolden(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Variables["brand"] = "Acme"
	s.svc.set.Types["service"] = seo.TypeTemplates{Title: "%title% %sep% Services", Description: "Read about %title%."}
	compareGolden(t, "pages/settings-seo-general.html", normalizePage(s.get(t, "/admin/settings/seo/general")))
	compareGolden(t, "pages/settings-seo-titles.html", normalizePage(s.get(t, "/admin/settings/seo/titles")))
	s.svc.set.Social = seo.Social{Image: "/media/card.png", TwitterSite: "@example", SiteTags: true, Types: map[string]seo.SocialType{"service": {Title: "%title% %sep% Services"}}}
	compareGolden(t, "pages/settings-seo-social.html", normalizePage(s.get(t, "/admin/settings/seo/social")))
	s.a.deps.SiteGraph = func(*http.Request) (map[string]any, error) {
		return map[string]any{"@context": "https://schema.org", "@graph": []any{map[string]any{"@type": "Organization", "name": "Acme"}}}, nil
	}
	s.ents.addSchema(5, "Guide", "guide", annotatedSchema, `{}`, false)
	compareGolden(t, "pages/settings-seo-schema.html", normalizePage(s.get(t, "/admin/settings/seo/schema")))
	compareGolden(t, "pages/settings-seo-knowledge-graph.html", normalizePage(s.get(t, "/admin/settings/seo/knowledge-graph")))
}

func TestSEOTitlesFromFormKeepsTypesNotInTheForm(t *testing.T) {
	stored := titlesState{Set: seo.Defaults()}
	stored.Set.Types["news"] = seo.TypeTemplates{Title: "%title% News"}
	stored.Types = []seoTypeRow{{
		Slug: "news", Title: "%title% News", ArchiveTitle: "All news", ArchiveDescription: "Latest",
		storedArchiveTitle: "All news", storedArchiveDescript: "Latest",
	}}
	next := seoTitlesFromForm(url.Values{"separator": {"-"}}, stored)
	row := next.Types[0]
	if row.ArchiveTitle != "All news" || row.ArchiveDescription != "Latest" || row.Title != "%title% News" {
		t.Errorf("row = %+v, want the stored values", row)
	}
	if next.Set.Types["news"].Title != "%title% News" {
		t.Errorf("template = %+v, want it kept", next.Set.Types["news"])
	}
}

// A failure partway through the save leaves no archive patch, no settings
// change, no cache purge and no audit entry.
func TestSEOTitlesSaveRollsBackOnFailurePartway(t *testing.T) {
	for name, arrange := range map[string]func(*seoRig){
		"last patch": func(s *seoRig) { s.svc.failLastPatch = true },
		"templates":  func(s *seoRig) { s.svc.failMerge = errors.New("merge failed") },
	} {
		s := newSEORig(t, newFakeSEO())
		arrange(s)
		form := goodTitlesForm()
		personTitlesForm(form, "All people")
		rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", form, nil)
		if rec.Code != 500 {
			t.Fatalf("%s: status %d, want 500", name, rec.Code)
		}
		if name == "last patch" && s.svc.appliedBeforeFailure == 0 {
			t.Fatalf("%s: no patch was written before the failing one, the case proves nothing", name)
		}
		if len(s.svc.patches) != 0 || s.svc.merges != 0 || s.flushed != 0 || len(s.events) != 0 {
			t.Errorf("%s: patches=%d merges=%d purges=%d audit=%d, want none", name, len(s.svc.patches), s.svc.merges, s.flushed, len(s.events))
		}
	}
}

// The person type is built in, yet its archive controls are editable and a
// save patches it.
func TestSEOTitlesArchiveFieldsEditableForPersonType(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/titles")
	for _, name := range []string{"archive_title", "archive_description"} {
		tag := seoControlTag(body, "type[person]["+name+"]")
		if tag == "" || strings.Contains(tag, "readonly") || strings.Contains(tag, "disabled") {
			t.Errorf("person %s is not editable: %q", name, tag)
		}
	}
	form := goodTitlesForm()
	personTitlesForm(form, "Our people")
	if rec := s.do("POST", "/admin/settings/seo/titles", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	p, ok := s.svc.patches[s.person.ID]
	if !ok || p.ArchiveTitle == nil || *p.ArchiveTitle != "Our people" {
		t.Errorf("person patch = %+v", p)
	}
}

func TestSEOSettingsMarkup(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	general := s.get(t, "/admin/settings/seo/general")
	for _, want := range []string{`<dl class="hm-dl">`, `<dt><code>%title%</code></dt>`, `class="hm-pair"`} {
		if !strings.Contains(general, want) {
			t.Errorf("general: missing %q", want)
		}
	}
	titles := s.get(t, "/admin/settings/seo/titles")
	for _, want := range []string{`hm-field__chip`, `id="seo-preview"`, `<table class="hm-table">`, `<div class="hm-savebar">`} {
		if !strings.Contains(titles, want) {
			t.Errorf("titles: missing %q", want)
		}
	}
	if strings.Contains(titles, "hm-card__foot") {
		t.Error("titles: the save button belongs in the sticky save bar")
	}
}
