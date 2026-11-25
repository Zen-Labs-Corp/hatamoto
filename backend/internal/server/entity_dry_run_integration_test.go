//go:build integration

package server

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/jsonx"
)

// dryDo is doCT with a query string appended to the entity URL.
func dryDo(e *condEnv, method string, id uuid.UUID, query, ct, body, ifMatch string) (int, http.Header, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+"/api/v1/entities/"+id.String()+query, bytes.NewReader([]byte(body)))
	req.Header.Set("X-API-Key", e.key)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Accept-Encoding", "identity")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, resp.Header, buf.Bytes()
}

func dryChanges(t *testing.T, raw []byte) []struct {
	Path   string `json:"path"`
	Before any    `json:"before"`
	After  any    `json:"after"`
} {
	t.Helper()
	var a dryAnswer
	if err := jsonx.Unmarshal(raw, &a); err != nil || !a.DryRun {
		t.Fatalf("answer %s: %v", raw, err)
	}
	return a.Changes
}

func TestEntityDryRunIntegrationStoresNothing(t *testing.T) {
	e := newCondEnv(t)
	v := e.version()
	count := func(q string) int {
		var n int
		e.scan(&n, q, e.postID.String())
		return n
	}
	const versionsQ = `SELECT count(*) FROM hatamoto_entity_versions WHERE entity_id::text = $1`
	const auditQ = `SELECT count(*) FROM hatamoto_admin_audit WHERE target = $1 AND action = 'entity.update'`
	versions, audits := count(versionsQ), count(auditQ)

	code, h, raw := e.do("PATCH", "?dry_run=1", `{"attributes":{"content":"next"}}`, tag(v))
	if code != http.StatusOK || h.Get("ETag") != tag(v) {
		t.Fatalf("dry run = %d etag %q: %s", code, h.Get("ETag"), raw)
	}
	ch := dryChanges(t, raw)
	if len(ch) != 1 || ch[0].Path != "content" || ch[0].Before != "base" || ch[0].After != "next" {
		t.Fatalf("changes = %+v", ch)
	}
	if e.content() != "base" || e.version() != v {
		t.Fatalf("stored changed: %q v%d", e.content(), e.version())
	}
	if count(versionsQ) != versions || count(auditQ) != audits {
		t.Fatal("a dry run wrote a version or audit row")
	}

	if code, _, raw := e.do("PATCH", "", `{"attributes":{"content":"next"}}`, tag(v)); code != http.StatusOK {
		t.Fatalf("real write = %d: %s", code, raw)
	}
	if e.version() != v+1 {
		t.Fatalf("version = %d, want %d", e.version(), v+1)
	}
}

func TestEntityDryRunIntegrationMerge(t *testing.T) {
	e := newCondEnv(t)
	const seed = `{"content":"base","notes":"x","series_part":5}`
	e.exec(`UPDATE hatamoto_entities SET attributes=$2::jsonb WHERE id=$1`, e.postID, seed)
	v := e.version()
	const body = `{"attributes":{"content":"next","notes":null}}`

	code, h, raw := dryDo(e, http.MethodPatch, e.postID, "?dry_run=1", mergeCT, body, tag(v))
	if code != http.StatusOK {
		t.Fatalf("dry run = %d: %s", code, raw)
	}
	ch := dryChanges(t, raw)
	if len(ch) != 2 || ch[0].Path != "content" || ch[0].After != "next" || ch[1].Path != "notes" || ch[1].Before != "x" || ch[1].After != nil {
		t.Fatalf("changes = %+v", ch)
	}
	attrsEqual(t, e.attrsOf(e.postID), seed)
	if e.versionOf(e.postID) != v {
		t.Fatalf("version moved to %d", e.versionOf(e.postID))
	}

	if code, _, raw := doCT(e, http.MethodPatch, e.postID, mergeCT, body, h.Get("ETag")); code != http.StatusOK {
		t.Fatalf("real merge = %d: %s", code, raw)
	}
	attrsEqual(t, e.attrsOf(e.postID), `{"content":"next","series_part":5}`)
}
