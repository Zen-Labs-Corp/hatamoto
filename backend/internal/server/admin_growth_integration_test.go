//go:build integration

package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/user"
)

// The site and growth screens of the Go admin against a migrated Postgres, through
// the whole server (session, CSRF, CSP and every route):
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run AdminGrowth
func TestAdminGrowthIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set: skipping (needs a migrated Postgres)")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "growth-it")
	t.Setenv("HATAMOTO_SITE_NAME", "Growth IT")
	t.Setenv("PUBLIC_BASE_URL", "https://growth-it.example.test")
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
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if srv.renderErr != nil {
		t.Fatal(srv.renderErr)
	}

	sfx := uuid.NewString()[:6]
	// ---- an owner with a session
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := srv.userRepo.Create(ctx, &user.CreateUserInput{Email: "owner-" + sfx + "@example.test", Role: user.RoleOwner, Name: "Owner"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	completeProfile(t, srv, owner)
	token, err := srv.authService.IssueSession(ctx, owner.ID, "it", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_users WHERE id=$1`, owner.ID)
	})

	var csrf string // fetched on the first signed-in request: an admin page read warms route caches the fixtures below must not meet
	do := func(method, path string, form url.Values, signedIn bool) *httptest.ResponseRecorder {
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
			if csrf == "" {
				csrf = adminToken(t, srv.router, token)
			}
			adminReq(req, token, csrf)
		} else if method == http.MethodGet {
			navigation(req) // signed out, a browser still navigates
		}
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		return rec
	}
	admin := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		return do(method, path, form, true)
	}

	// ---- fixtures: a routed schema with two pages, so redirects and menus have targets
	schemaID, aboutID, otherID := uuid.New(), uuid.New(), uuid.New()
	schemaSlug, aboutSlug, otherSlug := "git-"+sfx, "about-"+sfx, "other-"+sfx
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}',$3)`, schemaID, schemaSlug, `{"url_pattern":"/{slug}"}`)
	for id, slug := range map[uuid.UUID]string{aboutID: aboutSlug, otherID: otherSlug} {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes) VALUES ($1,$2,$3,$4,'published',now(),'{}'::jsonb)`, id, schemaID, "Page "+slug, slug)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_menu_items WHERE entity_id = ANY($1)`, []uuid.UUID{aboutID, otherID})
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})

	if rec := do("GET", "/admin/theme/menus", nil, false); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Fatalf("signed out: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// ============ menu save is one transaction ============
	t.Run("menu save", func(t *testing.T) {
		name := "IT menu " + sfx
		rec := admin("POST", "/admin/theme/menus", url.Values{"name": {name}, "location": {"footer"}})
		loc := rec.Header().Get("Location")
		// Create lands on the list with ?created=<id> (the admin "created" toast pattern).
		if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/theme/menus?created=") {
			t.Fatalf("create: %d %s", rec.Code, loc)
		}
		menuID := strings.TrimPrefix(loc, "/admin/theme/menus?created=")
		t.Cleanup(func() { _, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_menus WHERE id=$1`, menuID) })

		f := url.Values{"name": {name}, "location": {"footer"}, "_op": {"save"},
			"i.0.label": {"Home"}, "i.0.type": {"custom"}, "i.0.depth": {"0"}, "i.0.url": {"/"},
			"i.1.label": {"About"}, "i.1.type": {"entity"}, "i.1.depth": {"1"}, "i.1.entity": {aboutID.String()}, "i.1.entity_prev": {"Page " + aboutSlug},
			"i.2.label": {"Other (by name)"}, "i.2.type": {"entity"}, "i.2.depth": {"1"}, "i.2.entity_q": {"Page " + otherSlug},
			"i.3.label": {"Elsewhere"}, "i.3.type": {"custom"}, "i.3.depth": {"0"}, "i.3.url": {"https://example.test/x"}, "i.3.newtab": {"1"},
		}
		if rec := admin("POST", "/admin/theme/menus/"+menuID, f); rec.Code != 303 {
			t.Fatalf("save: %d\n%s", rec.Code, rec.Body)
		}
		type item struct {
			Label, LinkType string
			Parent          *uuid.UUID
			ID              uuid.UUID
			Entity          *uuid.UUID
			Pos             int
			NewTab          bool
		}
		load := func() []item {
			rows, err := db.Pool().Query(ctx, `SELECT id,label,link_type,parent_id,entity_id,position,open_in_new_tab FROM hatamoto_menu_items WHERE menu_id=$1 ORDER BY parent_id NULLS FIRST, position`, menuID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []item
			for rows.Next() {
				var it item
				if err := rows.Scan(&it.ID, &it.Label, &it.LinkType, &it.Parent, &it.Entity, &it.Pos, &it.NewTab); err != nil {
					t.Fatal(err)
				}
				out = append(out, it)
			}
			return out
		}
		items := load()
		if len(items) != 4 {
			t.Fatalf("items = %d, want 4: %+v", len(items), items)
		}
		by := map[string]item{}
		for _, it := range items {
			by[it.Label] = it
		}
		home := by["Home"]
		if by["About"].Parent == nil || *by["About"].Parent != home.ID || by["Other (by name)"].Parent == nil || *by["Other (by name)"].Parent != home.ID {
			t.Errorf("nesting lost: %+v", items)
		}
		if by["About"].Pos != 0 || by["Other (by name)"].Pos != 1 || by["Home"].Pos != 0 || by["Elsewhere"].Pos != 1 || !by["Elsewhere"].NewTab || by["Elsewhere"].LinkType != "external" {
			t.Errorf("order or flags wrong: %+v", items)
		}
		if by["Other (by name)"].Entity == nil || *by["Other (by name)"].Entity != otherID {
			t.Errorf("no-JS typed name was not resolved to the page: %+v", by["Other (by name)"])
		}
		// the editor shows the saved tree
		page := admin("GET", "/admin/theme/menus/"+menuID, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `data-depth="1"`) || !strings.Contains(page.Body.String(), "Page "+aboutSlug) {
			t.Errorf("edit page: %d", page.Code)
		}
		// a save that cannot complete (a page that no longer exists) changes nothing
		bad := url.Values{}
		for k, v := range f {
			bad[k] = v
		}
		bad.Set("i.1.entity", uuid.NewString())
		bad.Set("i.1.entity_prev", "Page "+aboutSlug)
		bad.Set("i.0.label", "Changed")
		if rec := admin("POST", "/admin/theme/menus/"+menuID, bad); rec.Code != 422 {
			t.Fatalf("bad save: %d", rec.Code)
		}
		after := load()
		if len(after) != 4 || after[0].Label == "Changed" {
			t.Errorf("a failed save must change nothing: %+v", after)
		}
		for _, it := range after {
			if it.Label == "Changed" {
				t.Error("partial write")
			}
		}
	})

	// ============ form submit + export ============
	t.Run("form submit and export", func(t *testing.T) {
		name := "IT contact " + sfx
		rec := admin("POST", "/admin/settings/forms", url.Values{"name": {name}})
		loc := rec.Header().Get("Location")
		if rec.Code != 303 || !strings.HasSuffix(loc, "/edit") {
			t.Fatalf("create form: %d %s", rec.Code, loc)
		}
		formID := strings.TrimSuffix(strings.TrimPrefix(loc, "/admin/settings/forms/"), "/edit")
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_form_submissions WHERE form_id=$1`, formID)
			_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_forms WHERE id=$1`, formID)
		})
		var slug string
		if err := db.Pool().QueryRow(ctx, `SELECT slug FROM hatamoto_forms WHERE id=$1`, formID).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		save := admin("POST", "/admin/settings/forms/"+formID+"/edit", url.Values{
			"name": {name}, "status": {"active"}, "_op": {"save"}, "store": {"1"}, "success_message": {"Thanks!"},
			"f.0.label": {"Name"}, "f.0.type": {"text"}, "f.0.name": {"name"}, "f.0.required": {"1"},
			"f.1.label": {"Email"}, "f.1.type": {"email"}, "f.1.name": {"email"},
			"f.2.label": {"Message"}, "f.2.type": {"textarea"}, "f.2.name": {"message"},
		})
		if save.Code != 303 {
			t.Fatalf("save form: %d\n%s", save.Code, save.Body)
		}
		submit := func(name, email, msg string) {
			t.Helper()
			r := do("POST", "/forms/"+slug+"/submit", url.Values{"name": {name}, "email": {email}, "message": {msg}, "_redirect": {"/"}}, false)
			if r.Code != http.StatusSeeOther {
				t.Fatalf("public submit: %d %s", r.Code, r.Body)
			}
		}
		submit("Ada", "ada@example.test", "Hello,\nworld \"quoted\"")
		submit("=HYPERLINK(\"http://evil.test\")", "evil@example.test", "x")
		submit("Bob", "bob@example.test", "y")
		// the builder's required/type checks are enforced on the public path: nothing is stored
		bad := do("POST", "/forms/"+slug+"/submit", url.Values{"email": {"not-an-email"}, "_redirect": {"/"}}, false)
		if loc := bad.Header().Get("Location"); bad.Code != http.StatusSeeOther || !strings.Contains(loc, "form_error=1") {
			t.Fatalf("an invalid public submit must fail: %d %q", bad.Code, loc)
		}
		if n := count(`SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1`, formID); n != 3 {
			t.Fatalf("stored %d submissions, want 3", n)
		}
		list := admin("GET", "/admin/settings/submissions?form="+formID, nil)
		if list.Code != 200 || !strings.Contains(list.Body.String(), "Name: Ada") {
			t.Errorf("submissions list: %d", list.Code)
		}
		exp := admin("GET", "/admin/settings/submissions/export.csv?form="+formID, nil)
		if exp.Code != 200 || !strings.HasPrefix(exp.Header().Get("Content-Type"), "text/csv") || !strings.Contains(exp.Header().Get("Content-Disposition"), slug) {
			t.Fatalf("export: %d %v", exp.Code, exp.Header())
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(exp.Body.String(), "\xef\xbb\xbf"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 4 || rows[0][3] != "Name" || rows[0][4] != "Email" {
			t.Fatalf("csv rows %d header %v", len(rows), rows[0])
		}
		var sawMsg, sawFormula bool
		var firstID string
		for _, r := range rows[1:] {
			sawMsg = sawMsg || r[5] == "Hello,\nworld \"quoted\""
			sawFormula = sawFormula || strings.HasPrefix(r[3], "'=HYPERLINK")
			if strings.HasPrefix(r[3], "=") {
				t.Errorf("formula not neutralised: %q", r[3])
			}
			if r[3] == "Ada" {
				firstID = r[2]
			}
		}
		if !sawMsg || !sawFormula || firstID == "" {
			t.Errorf("csv content: msg=%v formula=%v rows=%v", sawMsg, sawFormula, rows)
		}
		// mark one as spam through the bulk action, then export by status
		if rec := admin("POST", "/admin/settings/submissions/bulk?form="+formID, url.Values{"action": {"spam"}, "id": {firstID}}); rec.Code != 303 {
			t.Fatalf("bulk spam: %d", rec.Code)
		}
		spam := admin("GET", "/admin/settings/submissions/export.csv?form="+formID+"&status=spam", nil)
		if r, _ := csv.NewReader(strings.NewReader(strings.TrimPrefix(spam.Body.String(), "\xef\xbb\xbf"))).ReadAll(); len(r) != 2 {
			t.Errorf("spam export has %d rows, want header + 1", len(r))
		}
		// delete asks first, then deletes
		if rec := admin("POST", "/admin/settings/submissions/bulk?form="+formID, url.Values{"action": {"delete"}, "id": {firstID}}); rec.Code != 200 || count(`SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1`, formID) != 3 {
			t.Fatalf("delete must ask first: %d", rec.Code)
		}
		if rec := admin("POST", "/admin/settings/submissions/bulk?form="+formID, url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {firstID}}); rec.Code != 303 || count(`SELECT count(*) FROM hatamoto_form_submissions WHERE form_id=$1`, formID) != 2 {
			t.Fatalf("confirmed delete: %d", rec.Code)
		}
	})

	// ============ redirects: 404 log -> redirect, explain, wildcard ============
	t.Run("redirects explain and 404 to redirect", func(t *testing.T) {
		gone := "/gone-" + sfx
		if r := do("GET", gone, nil, false); r.Code != 404 {
			t.Fatalf("public 404: %d", r.Code)
		}
		var logID string
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_404_log WHERE path=$1`, gone).Scan(&logID); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if logID == "" {
			t.Fatal("the 404 was not logged")
		}
		t.Cleanup(func() {
			_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_404_log WHERE path=$1`, gone)
			_, _ = db.Pool().Exec(context.Background(), `DELETE FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+sfx+"%")
		})
		if page := admin("GET", "/admin/settings/redirects/404?q="+sfx, nil); page.Code != 200 || !strings.Contains(page.Body.String(), gone) || !strings.Contains(page.Body.String(), "Create redirect") {
			t.Fatalf("404 log page: %d", page.Code)
		}
		// explain before: nothing answers it
		if ex := admin("GET", "/admin/settings/redirects/explain?path="+url.QueryEscape(gone), nil); ex.Code != 200 || !strings.Contains(ex.Body.String(), "visitors get a 404") {
			t.Fatalf("explain before: %d\n%s", ex.Code, ex.Body)
		}
		// create the redirect from the log entry
		rec := admin("POST", "/admin/settings/redirects", url.Values{"source": {gone}, "target": {"/" + aboutSlug}, "code": {"301"}, "log": {logID}})
		if rec.Code != 303 {
			t.Fatalf("create redirect: %d\n%s", rec.Code, rec.Body)
		}
		if n := count(`SELECT count(*) FROM hatamoto_404_log WHERE path=$1 AND redirect_id IS NOT NULL`, gone); n != 1 {
			t.Error("the 404 entry should be linked to the new redirect")
		}
		if r := do("GET", gone, nil, false); r.Code != 301 || r.Header().Get("Location") != "/"+aboutSlug {
			t.Errorf("public after redirect: %d %q", r.Code, r.Header().Get("Location"))
		}
		if ex := admin("GET", "/admin/settings/redirects/explain?path="+url.QueryEscape(gone), nil); !strings.Contains(ex.Body.String(), "redirected to /"+aboutSlug+" with status 301") {
			t.Errorf("explain after: %s", ex.Body)
		}
		// a page answers its own address
		if ex := admin("GET", "/admin/settings/redirects/explain?path=/"+aboutSlug, nil); !strings.Contains(ex.Body.String(), "shows the page Page "+aboutSlug) {
			t.Errorf("explain a page: %s", ex.Body)
		}
		// wildcard rules go through the v1-09 validation and expand captures
		if rec := admin("POST", "/admin/settings/redirects", url.Values{"source": {"/**"}, "target": {"/x"}, "code": {"301"}}); rec.Code != 422 {
			t.Errorf("catch-all wildcard: %d", rec.Code)
		}
		if rec := admin("POST", "/admin/settings/redirects", url.Values{"source": {"/legacy-" + sfx + "/**"}, "target": {"/" + aboutSlug + "/$1"}, "code": {"302"}}); rec.Code != 303 {
			t.Fatalf("wildcard: %d %s", rec.Code, rec.Body)
		}
		ex := admin("GET", "/admin/settings/redirects/explain?path="+url.QueryEscape("/legacy-"+sfx+"/a/b"), nil)
		if !strings.Contains(ex.Body.String(), "redirected to /"+aboutSlug+"/a/b with status 302") {
			t.Errorf("explain wildcard: %s", ex.Body)
		}
		// bulk delete asks first, then removes
		var ids []string
		rows, _ := db.Pool().Query(ctx, `SELECT id FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+sfx+"%")
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) != 2 {
			t.Fatalf("redirects stored: %d", len(ids))
		}
		if rec := admin("POST", "/admin/settings/redirects/bulk", url.Values{"action": {"delete"}, "id": ids}); rec.Code != 200 || count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+sfx+"%") != 2 {
			t.Fatalf("bulk delete must ask first: %d", rec.Code)
		}
		if rec := admin("POST", "/admin/settings/redirects/bulk", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": ids}); rec.Code != 303 || count(`SELECT count(*) FROM hatamoto_redirects WHERE source_path LIKE $1`, "%"+sfx+"%") != 0 {
			t.Fatalf("confirmed bulk delete: %d", rec.Code)
		}
		if r := do("GET", gone, nil, false); r.Code != 404 {
			t.Errorf("after delete the address is a 404 again: %d", r.Code)
		}
	})

	// ============ dashboard JSON ============
	t.Run("dashboard JSON", func(t *testing.T) {
		today := time.Now().UTC().Truncate(24 * time.Hour)
		yesterday := today.AddDate(0, 0, -1)
		agent, op := "it-agent-"+sfx, "ITOp-"+sfx
		for day, hits := range map[time.Time]int{today: 7, yesterday: 3} {
			exec(`INSERT INTO hatamoto_agent_daily (day,agent_id,path,status,operator,purpose,entity_id,hits,verified_hits)
				VALUES ($1,$2,$3,200,$4,'training',$5,$6,$6)`, day, agent, "/"+aboutSlug, op, aboutID, hits)
		}
		exec(`INSERT INTO hatamoto_agent_daily (day,agent_id,path,status,operator,purpose,hits) VALUES ($1,$2,'/missing-'||$3,404,$4,'training',2)`, today, agent, sfx, op)
		// Add to whatever the shared database already counted for these days.
		exec(`INSERT INTO hatamoto_analytics_daily (day,kind,key,count) VALUES ($1,'pageview','',40),($2,'pageview','',60),($1,'ai_referral','it-assistant-`+sfx+`',5)
			ON CONFLICT (day,kind,key) DO UPDATE SET count = hatamoto_analytics_daily.count + EXCLUDED.count`, today, yesterday)
		t.Cleanup(func() {
			c := context.Background()
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_agent_daily WHERE agent_id=$1`, agent)
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_analytics_daily WHERE kind='ai_referral' AND key = $1`, "it-assistant-"+sfx)
			_, _ = db.Pool().Exec(c, `UPDATE hatamoto_analytics_daily SET count = count - CASE WHEN day = $1 THEN 40 ELSE 60 END WHERE kind='pageview' AND key='' AND day IN ($1,$2)`, today, yesterday)
			_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_analytics_daily WHERE kind='pageview' AND count <= 0`)
		})
		from, to := yesterday.Format("2006-01-02"), today.Format("2006-01-02")
		getJSON := func(path string) (int, map[string]any) {
			t.Helper()
			rec := admin("GET", path, nil)
			var m map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
				t.Fatalf("%s: not JSON: %v\n%s", path, err, rec.Body)
			}
			return rec.Code, m
		}
		code, m := getJSON("/admin/analytics/data/overview.json?from=" + from + "&to=" + to)
		if code != 200 {
			t.Fatalf("overview: %d %v", code, m)
		}
		fetches := m["charts"].(map[string]any)["fetches"].(map[string]any)
		labels := fetches["labels"].([]any)
		if len(labels) != 2 || labels[0] != from || labels[1] != to {
			t.Errorf("labels: %v", labels)
		}
		var series []float64
		for _, ds := range fetches["datasets"].([]any) {
			d := ds.(map[string]any)
			if d["label"] == op {
				for _, v := range d["data"].([]any) {
					series = append(series, v.(float64))
				}
			}
		}
		if len(series) != 2 || series[0] != 3 || series[1] != 9 {
			t.Errorf("series for %s = %v, want [3 9] (7 ok + 2 not-found today)", op, series)
		}
		totals := m["totals"].(map[string]any)
		if totals["ai_fetches"].(float64) < 12 || totals["pageviews"].(float64) < 100 || totals["ai_referred_visits"].(float64) < 5 {
			t.Errorf("totals: %v", totals)
		}
		vis := m["charts"].(map[string]any)["visits"].(map[string]any)
		if len(vis["datasets"].([]any)) != 2 {
			t.Errorf("visits chart: %v", vis)
		}
		if code, m := getJSON("/admin/analytics/data/agent.json?id=" + agent + "&from=" + from + "&to=" + to); code != 200 || m["hits"].(float64) != 12 || len(m["by_status"].([]any)) != 2 {
			t.Errorf("agent: %d %v", code, m)
		}
		if code, m := getJSON("/admin/analytics/data/pages.json?from=" + from + "&to=" + to); code != 200 {
			t.Errorf("pages: %d", code)
		} else if len(m["top"].([]any)) == 0 {
			t.Error("pages: nothing fetched?")
		}
		if code, m := getJSON("/admin/analytics/data/referrals.json?from=" + from + "&to=" + to); code != 200 || m["pageviews"].(float64) < 100 {
			t.Errorf("referrals: %d %v", code, m)
		}
		if code, _ := getJSON("/admin/analytics/data/overview.json?from=nonsense"); code != 400 {
			t.Errorf("bad range: %d", code)
		}
		// the Crawlers page lists the purposes, and Health lists the failing 404 with a redirect link
		if page := admin("GET", "/admin/analytics/crawlers?from="+from+"&to="+to, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "Agents by purpose") {
			t.Errorf("crawlers page: %d", page.Code)
		}
		if errs := admin("GET", "/admin/analytics/health?from="+from+"&to="+to, nil); !strings.Contains(errs.Body.String(), "source=%2Fmissing-"+sfx) {
			t.Errorf("health page: %d", errs.Code)
		}
		if rec := admin("GET", "/admin/analytics/referrals?from="+from+"&to="+to, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "It Assistant "+strings.ToUpper(sfx[:1])+sfx[1:]) {
			t.Errorf("referrals page: %d", rec.Code)
		}
	})
}
