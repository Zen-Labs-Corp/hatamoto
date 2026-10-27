//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/user"
)

// The builder against a migrated Postgres, through the whole server: open a
// page that only has sections (in-memory conversion, nothing written), preview
// an unsaved doc in the canvas frame, refuse a stale save, save, and see it on
// the public page with the page_sections mirror left frozen.
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run PageEditor
func TestPageEditorIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "editor-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Editor IT")
	t.Setenv("PUBLIC_BASE_URL", "https://editor-it.example.test")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENVIRONMENT", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var logOut io.Writer = io.Discard
	if os.Getenv("DUMP") != "" {
		logOut = os.Stderr
	}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(logOut, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}

	sfx := uuid.NewString()[:6]
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "pe-" + sfx + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner)
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	schemaID, pageID := uuid.New(), uuid.New()
	schemaSlug, pageSlug := "pe-"+sfx, "landing-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`, schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes) VALUES ($1,$2,'Landing',$3,'published',now(),$4::jsonb)`,
		pageID, schemaID, pageSlug, `{"page_sections":[
		 {"id":"h1","type":"hero","sort_order":0,"content":{"headline":"Before the edit","legacy_key":"kept"}},
		 {"id":"b1","type":"html","sort_order":1,"content":{"body":"<h2>Story</h2><p>Original body</p>"}}]}`)
	// the public renderer needs the site config row the setup wizard normally creates
	hadSite := db.Pool().QueryRow(ctx, `SELECT 1 FROM hatamoto_site_config`).Scan(new(int)) == nil
	if !hadSite {
		exec(`INSERT INTO hatamoto_site_config (company_name) VALUES ('Editor IT')`)
	}
	t.Cleanup(func() {
		c := context.Background()
		if !hadSite {
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_site_config`)
		}
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})

	csrf := adminToken(t, srv.router, token)
	do := func(method, path string, form url.Values, signedIn bool, hdr map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if signedIn {
			adminReq(req, token, csrf)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	edit := "/admin/entities/" + pageID.String() + "/edit"

	postJSON := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		adminReq(req, token, csrf)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	rawRow := func() (json.RawMessage, int) {
		t.Helper()
		var raw json.RawMessage
		var version int
		if err := db.Pool().QueryRow(ctx, `SELECT attributes, version FROM hatamoto_entities WHERE id=$1`, pageID).Scan(&raw, &version); err != nil {
			t.Fatal(err)
		}
		return raw, version
	}
	rawBefore, versionBefore := rawRow()

	// a page that only has sections opens the builder on its in-memory
	// conversion; opening it writes nothing (no version, no attribute change)
	rec := do("GET", edit, nil, true, nil)
	if rec.Code != 200 {
		t.Fatalf("open editor: %d\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`data-hm-builder`, `hm-builder-boot`, "Before the edit"} {
		if !strings.Contains(body, want) {
			t.Errorf("builder page lacks %q", want)
		}
	}
	if raw, v := rawRow(); v != versionBefore || !bytes.Equal(raw, rawBefore) {
		t.Fatalf("opening the page wrote: version %d -> %d", versionBefore, v)
	}
	m := regexp.MustCompile(`(?s)<script type="application/json" id="hm-builder-boot">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no builder boot data")
	}
	var boot struct {
		Doc     json.RawMessage `json:"doc"`
		Version int             `json:"version"`
	}
	if err := json.Unmarshal([]byte(m[1]), &boot); err != nil {
		t.Fatal(err)
	}

	// edit in the doc: new headline, hostile html body
	edited := strings.Replace(string(boot.Doc), "Before the edit", "After the edit", 1)
	edited = strings.Replace(edited, "Original body", "Edited <strong>body</strong><script>alert(1)</script>", 1)
	if edited == string(boot.Doc) {
		t.Fatalf("the doc carries neither section text: %s", boot.Doc)
	}

	// a staged preview of the unsaved doc renders through the real theme with the bridge
	prev := postJSON(strings.TrimSuffix(edit, "/edit")+"/builder/stage", `{"doc":`+edited+`}`)
	var staged struct{ URL string }
	if prev.Code != 200 || json.Unmarshal(prev.Body.Bytes(), &staged) != nil || staged.URL == "" {
		t.Fatalf("stage preview: %d %s", prev.Code, prev.Body.String())
	}
	// the same-site parent navigates the frame with the session cookie; the ticket in the URL is bound to that session
	frame := do("GET", staged.URL, nil, true, map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"})
	if frame.Code != 200 || !strings.Contains(frame.Body.String(), "After the edit") || !strings.Contains(frame.Body.String(), `id="hm-canvas-bridge"`) {
		t.Fatalf("preview frame: %d\n%.600s", frame.Code, frame.Body.String())
	}
	if raw, v := rawRow(); v != versionBefore || !bytes.Equal(raw, rawBefore) {
		t.Fatalf("a preview must not save: version %d -> %d", versionBefore, v)
	}

	// a stale save is refused and writes nothing
	stale := postJSON(strings.TrimSuffix(edit, "/edit")+"/builder/save", `{"version":`+strconv.Itoa(boot.Version+5)+`,"doc":`+edited+`,"op":"save"}`)
	if stale.Code != http.StatusConflict {
		t.Errorf("stale save: %d", stale.Code)
	}

	// save: page_doc stored, the page_sections mirror frozen as it was
	save := postJSON(strings.TrimSuffix(edit, "/edit")+"/builder/save", `{"version":`+strconv.Itoa(boot.Version)+`,"doc":`+edited+`,"op":"save"}`)
	if save.Code != 200 {
		t.Fatalf("save: %d\n%s", save.Code, save.Body.String())
	}
	rawAfter, version := rawRow()
	if version != versionBefore+1 {
		t.Errorf("version %d, want %d", version, versionBefore+1)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(rawAfter, &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored["page_doc"]), "After the edit") {
		t.Errorf("page_doc not stored: %s", stored["page_doc"])
	}
	var frozenBefore map[string]json.RawMessage
	_ = json.Unmarshal(rawBefore, &frozenBefore)
	if !bytes.Equal(stored["page_sections"], frozenBefore["page_sections"]) || strings.Contains(string(stored["page_sections"]), "After the edit") {
		t.Errorf("the page_sections mirror must stay frozen: %s", stored["page_sections"])
	}
	// the derived view (public JSON, plugins) shows the edit
	if !strings.Contains(string(entity.DerivedAttributes(rawAfter)), "After the edit") {
		t.Error("derived page_sections lacks the edit")
	}

	// the public page renders what was saved, sanitised
	pub := do("GET", "/"+pageSlug, nil, false, nil)
	out := pub.Body.String()
	if pub.Code != 200 || !strings.Contains(out, "After the edit") || !strings.Contains(out, "Edited <strong>body</strong>") {
		t.Fatalf("public page: %d\n%.800s", pub.Code, out)
	}
	if strings.Contains(out, "alert(1)") {
		t.Error("an html section script reached the public page")
	}

	// The no-JavaScript "Open preview in a new tab" is a top-level document in the
	// admin origin: through the real handler it must carry the sandbox CSP and no
	// plugin script, even for a granted plugin.
	render.SetPluginAssets("atk", "/p/atk/assets/plugin.css", "/p/atk/assets/plugin.js")
	render.SetPluginScriptGate(func(string) bool { return true })
	t.Cleanup(func() {
		render.SetPluginAssets("atk", "", "")
		render.SetPluginScriptGate(nil)
	})
	blogSchemaID, blogID := uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`, blogSchemaID, "pb-"+sfx, `{"url_pattern":"/{slug}","editor":"post"}`)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, blogSchemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, blogSchemaID)
	})
	exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,'Draft post',$3,'draft',$4::jsonb)`,
		blogID, blogSchemaID, "post-"+sfx, `{"content":"<p>Post body</p>"}`)
	noJS := do("POST", "/admin/entities/"+blogID.String()+"/edit/preview", url.Values{"name": {"Draft post"}, "content": {"<p>Post body</p>"}}, true, nil)
	if noJS.Code != 200 || !strings.Contains(noJS.Body.String(), "Post body") {
		t.Fatalf("no-JS preview: %d\n%.600s", noJS.Code, noJS.Body.String())
	}
	var sandboxed bool
	for _, v := range noJS.Header().Values("Content-Security-Policy") {
		sandboxed = sandboxed || v == "sandbox allow-scripts; script-src 'none'"
	}
	if !sandboxed {
		t.Errorf("no-JS preview CSP headers %q lack the sandbox", noJS.Header().Values("Content-Security-Policy"))
	}
	if strings.Contains(noJS.Body.String(), `<script src="/p/atk/assets/plugin.js"`) {
		t.Error("the no-JS preview carries the plugin script in the admin origin")
	}
}
