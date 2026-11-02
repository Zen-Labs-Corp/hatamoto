//go:build integration

package server

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seosvc"
)

// seoState is everything a SEO patch may write, as stored text.
type seoState struct{ settings, schemas, policy, audit string }

func readSEOState(t *testing.T, env *wireEnv) seoState {
	t.Helper()
	var st seoState
	q := func(dst *string, sql string) {
		if err := env.db.Pool().QueryRow(context.Background(), sql).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	st.settings = seoRowText(t, env)
	q(&st.schemas, `SELECT coalesce(string_agg(slug || settings::text, '|' ORDER BY slug), '') FROM hatamoto_entity_schemas`)
	q(&st.policy, `SELECT coalesce(string_agg(purpose, ',' ORDER BY purpose), '') FROM hatamoto_agent_robots_policy`)
	q(&st.audit, `SELECT count(*)::text FROM hatamoto_admin_audit WHERE action = 'seo.settings'`)
	return st
}

// Writes through the service: the stored rows change on a valid patch, and a
// refused one (settings fine, a per-type value bad) leaves settings, schema rows,
// the AI policy and the audit log exactly as they were.
//
//	go test -tags integration ./internal/server -run SEOServiceIntegration
func TestSEOServiceIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, nil)
	s, _ := env.server(t, "/admin")
	svc := s.newSEOService()
	if svc == nil {
		t.Fatal("no SEO service on the server")
	}
	slug := "seo-svc-" + uuid.NewString()[:6]
	schemaID := uuid.New()
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, slug, `{"url_pattern":"/`+slug+`/{slug}","custom_key":"keep"}`); err != nil {
		t.Fatal(err)
	}
	actor := seosvc.Actor{Via: "api", KeyName: "ci", KeyID: uuid.NewString()}
	patch := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// A valid patch: templates, a per-type archive title and the crawler policy.
	doc, err := svc.Update(ctx, actor, patch(`{"types":{"`+slug+`":{"title":"Docs: %title%"}},"type_settings":{"`+slug+`":{"archive_title":"All docs","schema_org_type":"Article"}},"ai_crawlers":{"denied_purposes":["training"]},"indexnow":{"enabled":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc["types"].(map[string]any)[slug].(map[string]any)["title"]; got != "Docs: %title%" {
		t.Errorf("stored type title = %v", got)
	}
	if got := doc[seoTypeSettingsKey].(map[string]any)[slug].(map[string]any); got["archive_title"] != "All docs" || got["schema_org_type"] != "Article" {
		t.Errorf("type settings = %v", got)
	}
	if got := doc[seoAICrawlersKey].(seoAICrawlers).DeniedPurposes; len(got) != 1 || got[0] != string(agents.PurposeTraining) {
		t.Errorf("ai policy = %v", got)
	}
	st, err := s.entityService.GetSchema(ctx, schemaID)
	if err != nil || !strings.Contains(string(st.Settings), `"custom_key": "keep"`) {
		t.Errorf("unknown schema key lost: %s %v", st.Settings, err)
	}
	var key, auditDetail string
	if err := env.db.Pool().QueryRow(ctx, `SELECT settings->'indexnow'->>'key' FROM hatamoto_seo_settings WHERE id`).Scan(&key); err != nil || key == "" {
		t.Fatalf("no IndexNow key stored on enable: %q %v", key, err)
	}
	if err := env.db.Pool().QueryRow(ctx, `SELECT detail::text FROM hatamoto_admin_audit WHERE action='seo.settings' ORDER BY at DESC, id DESC LIMIT 1`).Scan(&auditDetail); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Via, Key string
		Sections []string
	}
	if err := json.Unmarshal([]byte(auditDetail), &d); err != nil || d.Via != "api" || d.Key != "ci" || len(d.Sections) != 4 {
		t.Errorf("audit detail = %s (%v)", auditDetail, err)
	}
	if strings.Contains(auditDetail, key) {
		t.Errorf("the IndexNow key is in the audit detail: %s", auditDetail)
	}
	if raw, _ := json.Marshal(doc); strings.Contains(string(raw), key) {
		t.Error("the IndexNow key is in the response")
	}

	// Refused patches: nothing is written, whatever else the patch holds.
	bad := map[string]string{
		"unknown variable": `{"separator":"—","types":{"` + slug + `":{"title":"%nope%"}}}`,
		"admin address":    `{"separator":"—","robots":{"extra_lines":["Disallow: /admin/"]}}`,
		"schema type":      `{"separator":"—","ai_crawlers":{"denied_purposes":[]},"type_settings":{"` + slug + `":{"schema_org_type":"FAQPage"}}}`,
		"archive length":   `{"separator":"—","ai_crawlers":{"denied_purposes":[]},"type_settings":{"` + slug + `":{"archive_title":"` + strings.Repeat("x", entity.MaxArchiveTitleLen+1) + `"}}}`,
		"indexnow key":     `{"separator":"—","indexnow":{"key":"ATTACKERCHOSEN1"}}`,
		"bad purpose":      `{"separator":"—","ai_crawlers":{"denied_purposes":["nope"]}}`,
	}
	for name, p := range bad {
		before := readSEOState(t, env)
		_, err := svc.Update(ctx, actor, patch(p))
		if _, ok := err.(*seosvc.ValidationError); !ok {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
		if after := readSEOState(t, env); after != before {
			t.Errorf("%s: stored state changed\n before %+v\n after  %+v", name, before, after)
		}
	}
	if cur, _ := s.seoStore.Get(ctx); cur.Separator == "—" {
		t.Error("a refused patch reached the cached settings")
	}
}

var (
	titleRE = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	metaRE  = regexp.MustCompile(`<meta (?:name|property)="([^"]+)" content="([^"]*)"`)
)

// Preview equals the public <head>, and Analyze matches the panel on the stored entry.
func TestSEOServicePreviewMatchesPublicHeadIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, map[string]string{"PUBLIC_BASE_URL": "https://seo-it.example.test"})
	s, _ := env.server(t, "/admin")
	svc := s.newSEOService()
	slug := "seo-pv-" + uuid.NewString()[:6]
	schemaID, entityID := uuid.New(), uuid.New()
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object","properties":{"content":{"type":"string"}}}',$3)`,
		schemaID, slug, `{"url_pattern":"/{slug}"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Handbook',$3,'published',now(),$4::jsonb)`, entityID, schemaID, slug,
		`{"content":"<h1>Handbook</h1><p>How do we ship?</p>","meta_description":"Hand-written description","og_image":"/img/share.png"}`); err != nil {
		t.Fatal(err)
	}
	actor := seosvc.Actor{Via: "api", KeyName: "ci"}
	if _, err := svc.Update(ctx, actor, map[string]json.RawMessage{"types": json.RawMessage(`{"` + slug + `":{"title":"Guide: %title% %sep% %sitename%"}}`)}); err != nil {
		t.Fatal(err)
	}

	pv, err := svc.Preview(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(s, "/"+slug)
	if rec.Code != 200 {
		t.Fatalf("public page: %d", rec.Code)
	}
	body := rec.Body.String()
	if m := titleRE.FindStringSubmatch(body); m == nil || html.UnescapeString(m[1]) != pv.Title {
		t.Errorf("title: preview %q, page %v", pv.Title, m)
	}
	if !strings.Contains(pv.Title, "Guide: Handbook") {
		t.Errorf("the type template did not apply: %q", pv.Title)
	}
	tags := map[string]string{}
	for _, m := range metaRE.FindAllStringSubmatch(body, -1) {
		tags[m[1]] = html.UnescapeString(m[2])
	}
	if tags["description"] != pv.Description || tags["description"] != "Hand-written description" {
		t.Errorf("description: preview %q, page %q", pv.Description, tags["description"])
	}
	if tags["robots"] != pv.Robots {
		t.Errorf("robots: preview %q, page %q", pv.Robots, tags["robots"])
	}
	if want := "https://seo-it.example.test/" + slug; !strings.Contains(body, `rel="canonical" href="`+want+`"`) || pv.Canonical != want {
		t.Errorf("canonical: preview %q", pv.Canonical)
	}
	for _, m := range pv.OG {
		if got, ok := tags[m.Key]; !ok || got != m.Content {
			t.Errorf("%s: preview %q, page %q (%v)", m.Key, m.Content, got, ok)
		}
	}
	if len(pv.OG) == 0 || len(pv.Twitter) == 0 {
		t.Errorf("no social tags in the preview: %+v", pv)
	}

	panel, err := svc.Analyze(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if panel.SERP.Title != pv.Title || panel.Total == 0 || panel.Words == 0 {
		t.Errorf("panel = %+v", panel.SERP)
	}

	if _, err := svc.Preview(ctx, uuid.New()); err == nil {
		t.Error("preview of an unknown entry succeeded")
	}
}

// The homepage entry is previewed as "/" serves it, not at its own slug.
func TestSEOServicePreviewHomepageIntegration(t *testing.T) {
	ctx := context.Background()
	env := newWireEnv(t, map[string]string{"PUBLIC_BASE_URL": "https://seo-it.example.test"})
	slug := "seo-home-" + uuid.NewString()[:6]
	schemaID, entityID := uuid.New(), uuid.New()
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`,
		schemaID, slug, `{"url_pattern":"/{slug}"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
		VALUES ($1,$2,'Welcome',$3,'published',now(),'{"meta_description":"The front door"}'::jsonb)`, entityID, schemaID, slug); err != nil {
		t.Fatal(err)
	}
	var prevHome *uuid.UUID
	hadSite := env.db.Pool().QueryRow(ctx, `SELECT homepage_entity_id FROM hatamoto_site_config`).Scan(&prevHome) == nil
	if _, err := env.db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name, homepage_entity_id) VALUES ('Home IT', $1)
		ON CONFLICT (id) DO UPDATE SET homepage_entity_id = EXCLUDED.homepage_entity_id`, entityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadSite {
			_, _ = env.db.Pool().Exec(context.Background(), `UPDATE hatamoto_site_config SET homepage_entity_id=$1`, prevHome)
		}
	})
	s, _ := env.server(t, "/admin")
	svc := s.newSEOService()

	pv, err := svc.Preview(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	rec := get(s, "/")
	if rec.Code != 200 {
		t.Fatalf("homepage: %d", rec.Code)
	}
	body := rec.Body.String()
	if m := titleRE.FindStringSubmatch(body); m == nil || html.UnescapeString(m[1]) != pv.Title {
		t.Errorf("title: preview %q, page %v", pv.Title, m)
	}
	if want := "https://seo-it.example.test/"; pv.Canonical != want || !strings.Contains(body, `rel="canonical" href="`+want+`"`) {
		t.Errorf("canonical: preview %q, want %q", pv.Canonical, want)
	}
}
