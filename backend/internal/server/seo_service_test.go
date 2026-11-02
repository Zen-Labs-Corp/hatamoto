package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/seosvc"
)

// fakeSEOBackend is the settings row plus the schema writes of one saveWithSettings.
type fakeSEOBackend struct {
	set     seo.Settings
	saves   int
	patches []schemaPatch
	failOn  error // returned by saveWithSettings after the checks
}

func (f *fakeSEOBackend) Get(context.Context) (seo.Settings, error) { return f.set.Clone(), nil }

func (f *fakeSEOBackend) saveWithSettings(_ context.Context, patches []schemaPatch, fn func(*seo.Settings)) error {
	if f.failOn != nil {
		return f.failOn
	}
	next := f.set.Clone()
	fn(&next)
	if errs := next.Validate(); len(errs) > 0 {
		return &seo.ValidationError{Errors: errs}
	}
	f.saves++
	f.patches = append(f.patches, patches...)
	f.set = next
	return nil
}

type fakeSEOSchemas []*entity.EntitySchema

func (f fakeSEOSchemas) ListSchemas(context.Context) ([]*entity.EntitySchema, error) { return f, nil }

type fakeRobots struct {
	denied []agents.Purpose
	sets   int
}

func (f *fakeRobots) DeniedPurposes(context.Context) ([]agents.Purpose, error) { return f.denied, nil }
func (f *fakeRobots) SetDenied(_ context.Context, d []agents.Purpose) error {
	f.sets++
	f.denied = d
	return nil
}

type seoRig struct {
	svc     *seoService
	back    *fakeSEOBackend
	robots  *fakeRobots
	audits  []admin.AuditEvent
	purged  int
	docType *entity.EntitySchema
}

func newSEORig(t *testing.T) *seoRig {
	t.Helper()
	r := &seoRig{back: &fakeSEOBackend{set: seo.Defaults()}, robots: &fakeRobots{}}
	r.docType = &entity.EntitySchema{ID: uuid.New(), Slug: "doc", Name: "Docs", Settings: json.RawMessage(`{"archive_title":"All docs","sitemap":{"priority":0.4,"changefreq":"weekly"}}`)}
	block := &entity.EntitySchema{ID: uuid.New(), Slug: entity.BlockSchemaSlug}
	post := &entity.EntitySchema{ID: uuid.New(), Slug: "post", IsBuiltin: true}
	r.svc = &seoService{
		backend: r.back, schemas: fakeSEOSchemas{r.docType, block, post}, robots: r.robots,
		adminPath:  func() string { return "/admin" },
		audit:      func(_ context.Context, e admin.AuditEvent) { r.audits = append(r.audits, e) },
		invalidate: func(context.Context) { r.purged++ },
	}
	return r
}

func patchOf(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var keyActor = seosvc.Actor{Via: "api", KeyName: "deploy", KeyID: uuid.NewString()}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *seosvc.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a validation error", err)
	}
	return ve.Fields
}

func TestSEOServiceGetShapeAndRedaction(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	r.back.set.IndexNow = seo.IndexNow{Enabled: true, Key: "SECRETKEY123", Endpoint: "api.indexnow.org"}
	r.robots.denied = []agents.Purpose{agents.PurposeTraining}
	doc, err := r.svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), "SECRETKEY123") {
		t.Fatalf("the IndexNow key is in the document: %s", raw)
	}
	if in := doc["indexnow"].(map[string]any); in["enabled"] != true || in["endpoint"] != "api.indexnow.org" || len(in) != 2 {
		t.Errorf("indexnow = %v", in)
	}
	for _, k := range append([]string{seoTypeSettingsKey, seoAICrawlersKey}, seoSections...) {
		if _, ok := doc[k]; !ok {
			t.Errorf("section %q missing", k)
		}
	}
	ts := doc[seoTypeSettingsKey].(map[string]any)
	if _, ok := ts[entity.BlockSchemaSlug]; ok {
		t.Error("a reuse type has SEO settings")
	}
	d := ts["doc"].(map[string]any)
	sm := d["sitemap"].(map[string]any)
	if d["archive_title"] != "All docs" || sm["priority"] != 0.4 || sm["change_freq"] != "weekly" || d["faq_from_content"] != true {
		t.Errorf("doc type = %v", d)
	}
	if got := doc[seoAICrawlersKey].(seoAICrawlers).DeniedPurposes; !reflect.DeepEqual(got, []string{"training"}) {
		t.Errorf("denied = %v", got)
	}
}

func TestSEOServiceUpdateMergesWithinASection(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	r.back.set.Social = seo.Social{Image: "https://example.com/a.png", TwitterSite: "@old", Types: map[string]seo.SocialType{"doc": {Title: "T"}, "post": {Image: "i"}}}
	r.back.set.Separator = "|"
	_, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"social":{"twitter_site":"@new","types":{"doc":{"image":"/x.png"},"post":null}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := r.back.set.Social
	if got.TwitterSite != "@new" || got.Image != "https://example.com/a.png" {
		t.Errorf("siblings changed: %+v", got)
	}
	if want := (map[string]seo.SocialType{"doc": {Title: "T", Image: "/x.png"}}); !reflect.DeepEqual(got.Types, want) {
		t.Errorf("types = %+v, want %+v (merged entry, deleted null)", got.Types, want)
	}
	if r.back.set.Separator != "|" {
		t.Error("another section changed")
	}
	if r.purged != 1 {
		t.Errorf("purged %d times", r.purged)
	}
}

func TestSEOServiceUpdateTypeTemplatesAndDefaults(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	r.back.set.Types["doc"] = seo.TypeTemplates{Title: "%title%", Description: "keep"}
	_, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"types":{"doc":{"title":"Docs: %title%"}},"home":"  ","archives":{"author":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.back.set.Types["doc"]; got.Title != "Docs: %title%" || got.Description != "keep" {
		t.Errorf("type = %+v", got)
	}
	if r.back.set.Home != seo.DefaultTitle || r.back.set.Archives.Author != seo.DefaultTitle {
		t.Errorf("empty templates not normalised: %q %q", r.back.set.Home, r.back.set.Archives.Author)
	}
}

func TestSEOServiceRefusesAndChangesNothing(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ patch, field string }{
		"unknown variable":    {`{"types":{"doc":{"title":"%nope%"}}}`, "type[doc][title]"},
		"unknown key":         {`{"typos":{}}`, "typos"},
		"unknown field":       {`{"social":{"twiter":"x"}}`, "social"},
		"admin address":       {`{"robots":{"extra_lines":["Disallow: /admin/"]}}`, "robots[extra]"},
		"unknown type":        {`{"type_settings":{"nope":{"archive_title":"x"}}}`, "type_settings[nope]"},
		"built-in archive":    {`{"type_settings":{"post":{"archive_title":"x"}}}`, "type_settings[post][archive_title]"},
		"bad purpose":         {`{"ai_crawlers":{"denied_purposes":["telepathy"]}}`, "ai_crawlers[denied_purposes]"},
		"indexnow key":        {`{"indexnow":{"key":"abc"}}`, "indexnow"},
		"bad priority":        {`{"type_settings":{"doc":{"sitemap":{"priority":"high"}}}}`, "type_settings[doc][sitemap][priority]"},
		"type value not json": {`{"type_settings":{"doc":7}}`, "type_settings[doc]"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newSEORig(t)
			// A valid change next to the bad one must not be written either.
			patch := patchOf(t, c.patch)
			patch["separator"] = json.RawMessage(`"|"`)
			before := r.back.set.Clone()
			_, err := r.svc.Update(context.Background(), keyActor, patch)
			if _, ok := fieldsOf(t, err)[c.field]; !ok {
				t.Errorf("fields = %v, want %q", fieldsOf(t, err), c.field)
			}
			if r.back.saves != 0 || !reflect.DeepEqual(r.back.set, before) || r.robots.sets != 0 || len(r.audits) != 0 || r.purged != 0 {
				t.Errorf("a refused patch changed something: saves=%d sets=%d audits=%d purged=%d", r.back.saves, r.robots.sets, len(r.audits), r.purged)
			}
		})
	}
}

func TestSEOServiceWritesSchemaPatchesAndAIPolicy(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	_, err := r.svc.Update(context.Background(), keyActor, patchOf(t,
		`{"type_settings":{"doc":{"archive_title":"Handbook","schema_org_type":"Article","sitemap":{"exclude":true,"priority":null}}},"ai_crawlers":{"denied_purposes":["training","training","other_ai"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.back.patches) != 1 || r.back.patches[0].id != r.docType.ID {
		t.Fatalf("patches = %+v", r.back.patches)
	}
	p := r.back.patches[0].patch
	if *p.ArchiveTitle != "Handbook" || *p.SchemaOrgType != "Article" || p.ArchiveDescription != nil {
		t.Errorf("patch = %+v", p)
	}
	// The sitemap patch replaces the three owned keys, so the stored
	// changefreq is carried over and only exclude and priority change.
	if sp := p.Sitemap; sp == nil || !sp.Exclude || sp.Priority != nil || sp.ChangeFreq != "weekly" {
		t.Errorf("sitemap patch = %+v", p.Sitemap)
	}
	if want := []agents.Purpose{agents.PurposeTraining, agents.PurposeOtherAI}; !reflect.DeepEqual(r.robots.denied, want) {
		t.Errorf("denied = %v, want %v", r.robots.denied, want)
	}
}

func TestSEOServiceIndexNowKeyIsNeverSetByThePatch(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	r.back.set.IndexNow = seo.IndexNow{Enabled: true, Key: "KEEPME12345", Endpoint: "api.indexnow.org"}
	if _, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"indexnow":{"endpoint":"www.bing.com"}}`)); err != nil {
		t.Fatal(err)
	}
	if got := r.back.set.IndexNow; got.Key != "KEEPME12345" || got.Endpoint != "www.bing.com" || !got.Enabled {
		t.Errorf("indexnow = %+v", got)
	}
	// Turning it on generates a key (as the admin does); the document never shows it.
	r = newSEORig(t)
	doc, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"indexnow":{"enabled":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.back.set.IndexNow.Key == "" {
		t.Error("no key generated on enable")
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), r.back.set.IndexNow.Key) {
		t.Error("the generated key is in the response")
	}
}

func TestSEOServiceAuditNamesKeyAndSectionsOnly(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	_, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"monitor404":{"ignore_patterns":["/secret-path"]},"indexnow":{"enabled":true},"home":"%title%"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.audits) != 1 {
		t.Fatalf("audits = %d", len(r.audits))
	}
	e := r.audits[0]
	if e.Action != "seo.settings" || e.Target != "seo" || e.Actor != "api-key:deploy" {
		t.Errorf("event = %+v", e)
	}
	if e.Detail["key"] != "deploy" || e.Detail["via"] != "api" || !reflect.DeepEqual(e.Detail["sections"], []string{"home", "indexnow", "monitor404"}) {
		t.Errorf("detail = %v", e.Detail)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "secret-path") || strings.Contains(string(raw), r.back.set.IndexNow.Key) {
		t.Errorf("audit leaks a value: %s", raw)
	}
}

func TestSEOServiceOwnerAuditUsesEmail(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	owner := seosvc.Actor{Via: "api", UserID: uuid.NewString(), Email: "owner@example.com"}
	if _, err := r.svc.Update(context.Background(), owner, patchOf(t, `{"separator":"-"}`)); err != nil {
		t.Fatal(err)
	}
	if e := r.audits[0]; e.Actor != "owner@example.com" || e.ActorID != owner.UserID || e.Detail["key"] != nil {
		t.Errorf("event = %+v", e)
	}
}

func TestSEOServiceEmptyPatchWritesNothing(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	if _, err := r.svc.Update(context.Background(), keyActor, map[string]json.RawMessage{}); err != nil {
		t.Fatal(err)
	}
	if r.back.saves != 0 || len(r.audits) != 0 || r.purged != 0 {
		t.Error("an empty patch wrote")
	}
}

func TestSEOServicePolicyOnlyPatchSkipsSettingsWrite(t *testing.T) {
	t.Parallel()

	r := newSEORig(t)
	if _, err := r.svc.Update(context.Background(), keyActor, patchOf(t, `{"ai_crawlers":{"denied_purposes":[]}}`)); err != nil {
		t.Fatal(err)
	}
	if r.back.saves != 0 || r.robots.sets != 1 || len(r.audits) != 1 {
		t.Errorf("saves=%d sets=%d audits=%d", r.back.saves, r.robots.sets, len(r.audits))
	}
}

func TestSEOWriteErrorMapsEntityRefusals(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	slugs := map[uuid.UUID]string{id: "doc"}
	wrap := func(err error) error { return &admin.SEOPatchError{ID: id, Err: err} }

	got := seoWriteError(wrap(&entity.SettingsError{Field: "schema_org_type", Msg: "not allowed"}), slugs)
	if got == nil || got.Fields["type_settings[doc][schema_org_type]"] != "not allowed" {
		t.Errorf("settings error = %+v", got)
	}
	if got := seoWriteError(wrap(entity.ErrCannotModifyBuiltin), slugs); got == nil || len(got.Fields) != 1 {
		t.Errorf("builtin = %+v", got)
	}
	if got := seoWriteError(&seo.ValidationError{Errors: []seo.FieldError{{Field: "home", Message: "bad"}}}, slugs); got == nil || got.Fields["home"] != "bad" {
		t.Errorf("seo error = %+v", got)
	}
	if got := seoWriteError(errors.New("db down"), slugs); got != nil {
		t.Errorf("a database error became a validation error: %+v", got)
	}
}
