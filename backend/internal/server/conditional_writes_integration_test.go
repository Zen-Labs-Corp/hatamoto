//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// condEnv is a real server with one draft post and an owner key.
type condEnv struct {
	t      *testing.T
	ts     *httptest.Server
	key    string
	postID uuid.UUID
	exec   func(q string, args ...any)
	scan   func(dest any, q string, args ...any)
}

func newCondEnv(t *testing.T) *condEnv {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; needs a migrated Postgres")
	}
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
	t.Setenv("HATAMOTO_CANONICAL_SLUG", "ifmatch-it")
	t.Setenv("HATAMOTO_SITE_NAME", "If-Match IT")
	t.Setenv("SMTP_FROM", "it@example.test")
	t.Setenv("SMTP_FROM_NAME", "If-Match IT")
	t.Setenv("PUBLIC_BASE_URL", "https://ifmatch-it.example.test")
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
	e := &condEnv{t: t}
	e.exec = func(q string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	e.scan = func(dest any, q string, args ...any) {
		t.Helper()
		if err := db.Pool().QueryRow(ctx, q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sfx := uuid.NewString()[:6]
	userID := uuid.New()
	e.postID = uuid.New()
	e.exec(`INSERT INTO hatamoto_users (id,email,password_hash,role) VALUES ($1,$2,'x','owner')`, userID, "ifmatch-"+sfx+"@example.test")
	e.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes)
		SELECT $1, id, 'A post', $2::text, 'draft', '{"content":"base"}'::jsonb FROM hatamoto_entity_schemas WHERE slug = 'post'`, e.postID, "ifmatch-"+sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE id = $1`, e.postID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_mcp_audit WHERE api_key_name LIKE $1`, "%"+sfx)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_api_keys WHERE created_by=$1`, userID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_users WHERE id=$1`, userID)
	})
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.ts = httptest.NewServer(srv.router)
	t.Cleanup(e.ts.Close)
	key, err := srv.apiKeyService.Create(ctx, &userID, &auth.CreateAPIKeyInput{Name: "ifmatch" + sfx, Scopes: []auth.APIKeyScope{auth.ScopeWriteEntities, auth.ScopeReadEntities}})
	if err != nil {
		t.Fatal(err)
	}
	e.key = key.PlainKey
	return e
}

// do sends one request; ifMatch "" sends no header.
func (e *condEnv) do(method, path, body, ifMatch string) (int, http.Header, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+"/api/v1/entities/"+e.postID.String()+path, bytes.NewReader([]byte(body)))
	req.Header.Set("X-API-Key", e.key)
	req.Header.Set("Content-Type", "application/json")
	// the compression middleware turns a strong ETag into a weak one
	req.Header.Set("Accept-Encoding", "identity")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func (e *condEnv) etag() string {
	e.t.Helper()
	code, h, raw := e.do(http.MethodGet, "", "", "")
	if code != http.StatusOK || h.Get("ETag") == "" {
		e.t.Fatalf("GET = %d etag %q %s", code, h.Get("ETag"), raw)
	}
	return h.Get("ETag")
}

func (e *condEnv) version() int {
	e.t.Helper()
	var v int
	e.scan(&v, `SELECT version FROM hatamoto_entities WHERE id = $1`, e.postID)
	return v
}

func (e *condEnv) content() string {
	e.t.Helper()
	var c string
	e.scan(&c, `SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`, e.postID)
	return c
}

func tag(v int) string { return fmt.Sprintf(`"%d"`, v) }

// Two writers holding the same tag: one stores, the other gets 412 and its
// attributes never land.
func TestConditionalConcurrentPatchIntegration(t *testing.T) {
	e := newCondEnv(t)
	for round := 0; round < 10; round++ {
		n := e.etag()
		before := e.version()
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i := range codes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], _, _ = e.do(http.MethodPatch, "", fmt.Sprintf(`{"attributes":{"content":"w%d-%d"}}`, round, i), n)
			}()
		}
		wg.Wait()
		ok, stale := 0, 0
		winner := -1
		for i, c := range codes {
			switch c {
			case http.StatusOK:
				ok++
				winner = i
			case http.StatusPreconditionFailed:
				stale++
			}
		}
		if ok != 1 || stale != 1 {
			t.Fatalf("round %d: codes = %v, want one 200 and one 412", round, codes)
		}
		if got, want := e.content(), fmt.Sprintf("w%d-%d", round, winner); got != want {
			t.Fatalf("round %d: content = %q, want the winner's %q", round, got, want)
		}
		if got := e.version(); got != before+1 {
			t.Fatalf("round %d: version = %d, want %d", round, got, before+1)
		}
	}
}

// A restore naming a stale version is refused and touches nothing; with the
// current one it stores a new version carrying the old content.
func TestConditionalRestoreIntegration(t *testing.T) {
	e := newCondEnv(t)
	if code, _, raw := e.do(http.MethodPatch, "", `{"attributes":{"content":"first"}}`, ""); code != http.StatusOK {
		t.Fatalf("edit = %d %s", code, raw)
	}
	stale := e.version()
	if code, _, raw := e.do(http.MethodPatch, "", `{"attributes":{"content":"second"}}`, ""); code != http.StatusOK {
		t.Fatalf("edit = %d %s", code, raw)
	}
	var old int
	e.scan(&old, `SELECT version FROM hatamoto_entity_versions WHERE entity_id = $1 AND attributes->>'content' = 'first' LIMIT 1`, e.postID)
	cur := e.version()

	code, h, raw := e.do(http.MethodPost, fmt.Sprintf("/restore/%d", old), "", tag(stale))
	if code != http.StatusPreconditionFailed || h.Get("ETag") != tag(cur) {
		t.Fatalf("stale restore = %d etag %q %s, want 412 with %s", code, h.Get("ETag"), raw, tag(cur))
	}
	var body struct {
		Current int `json:"current_version"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Current != cur {
		t.Fatalf("body %s: current_version want %d (%v)", raw, cur, err)
	}
	if e.version() != cur || e.content() != "second" {
		t.Fatalf("a refused restore changed the entry: version %d content %q", e.version(), e.content())
	}

	code, h, raw = e.do(http.MethodPost, fmt.Sprintf("/restore/%d", old), "", tag(cur))
	if code != http.StatusOK || h.Get("ETag") != tag(cur+1) {
		t.Fatalf("restore = %d etag %q %s, want 200 with %s", code, h.Get("ETag"), raw, tag(cur+1))
	}
	if e.content() != "first" || e.version() != cur+1 {
		t.Fatalf("restored: version %d content %q", e.version(), e.content())
	}
}

// A status change compares the content version: an attribute edit in between
// makes the old tag stale and the status stays.
func TestConditionalStatusIntegration(t *testing.T) {
	e := newCondEnv(t)
	n := e.version()
	if code, _, raw := e.do(http.MethodPatch, "", `{"attributes":{"content":"edited"}}`, tag(n)); code != http.StatusOK {
		t.Fatalf("edit = %d %s", code, raw)
	}
	code, h, raw := e.do(http.MethodPatch, "/status", `{"status":"published"}`, tag(n))
	if code != http.StatusPreconditionFailed || h.Get("ETag") != tag(n+1) {
		t.Fatalf("stale status = %d etag %q %s, want 412 with %s", code, h.Get("ETag"), raw, tag(n+1))
	}
	var st string
	e.scan(&st, `SELECT status FROM hatamoto_entities WHERE id = $1`, e.postID)
	if st != "draft" {
		t.Fatalf("status = %q after a refused change, want draft", st)
	}
	code, h, raw = e.do(http.MethodPatch, "/status", `{"status":"published"}`, tag(n+1))
	if code != http.StatusOK || h.Get("ETag") != tag(n+1) {
		t.Fatalf("status = %d etag %q %s, want 200 with %s (a status change does not advance the version)", code, h.Get("ETag"), raw, tag(n+1))
	}
}

// A restore and an edit holding the same tag: one wins, the other gets 412.
func TestConditionalRestoreRacesPatchIntegration(t *testing.T) {
	e := newCondEnv(t)
	if code, _, raw := e.do(http.MethodPatch, "", `{"attributes":{"content":"first"}}`, ""); code != http.StatusOK {
		t.Fatalf("edit = %d %s", code, raw)
	}
	var old int
	e.scan(&old, `SELECT version FROM hatamoto_entity_versions WHERE entity_id = $1 LIMIT 1`, e.postID)
	for round := 0; round < 10; round++ {
		n := e.etag()
		before := e.version()
		var restoreCode, patchCode int
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			restoreCode, _, _ = e.do(http.MethodPost, fmt.Sprintf("/restore/%d", old), "", n)
		}()
		go func() {
			defer wg.Done()
			patchCode, _, _ = e.do(http.MethodPatch, "", fmt.Sprintf(`{"attributes":{"content":"p%d"}}`, round), n)
		}()
		wg.Wait()
		wins := 0
		for _, c := range []int{restoreCode, patchCode} {
			if c == http.StatusOK {
				wins++
			} else if c != http.StatusPreconditionFailed {
				t.Fatalf("round %d: restore = %d, patch = %d", round, restoreCode, patchCode)
			}
		}
		if wins != 1 || e.version() != before+1 {
			t.Fatalf("round %d: restore = %d, patch = %d, version %d -> %d, want one winner and one new version", round, restoreCode, patchCode, before, e.version())
		}
	}
}
