//go:build integration

package server

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/jsonx"
)

// doCT is condEnv.do with a Content-Type and an explicit entity id.
func doCT(e *condEnv, method string, id uuid.UUID, ct, body, ifMatch string) (int, http.Header, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+"/api/v1/entities/"+id.String(), bytes.NewReader([]byte(body)))
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
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func (e *condEnv) attrsOf(id uuid.UUID) map[string]any {
	e.t.Helper()
	var s string
	e.scan(&s, `SELECT attributes::text FROM hatamoto_entities WHERE id = $1`, id)
	m := map[string]any{}
	if err := jsonx.Unmarshal([]byte(s), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *condEnv) versionOf(id uuid.UUID) int {
	e.t.Helper()
	var v int
	e.scan(&v, `SELECT version FROM hatamoto_entities WHERE id = $1`, id)
	return v
}

func attrsEqual(t *testing.T, got map[string]any, want string) {
	t.Helper()
	w := map[string]any{}
	if err := jsonx.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	gb, _ := jsonx.Marshal(got)
	wb, _ := jsonx.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("attributes = %s, want %s", gb, wb)
	}
}

const mergeCT = "application/merge-patch+json"

func TestMergePatchREST(t *testing.T) {
	e := newCondEnv(t)
	const base = `{"content":"base","notes":"x","series_part":5}`
	reset := func() {
		e.exec(`UPDATE hatamoto_entities SET attributes=$2::jsonb WHERE id=$1`, e.postID, base)
	}

	t.Run("merge applies with a fresh tag", func(t *testing.T) {
		reset()
		v := e.version()
		code, h, raw := doCT(e, http.MethodPatch, e.postID, mergeCT, `{"attributes":{"content":"B","notes":null}}`, tag(v))
		if code != http.StatusOK {
			t.Fatalf("PATCH = %d %s", code, raw)
		}
		if got := h.Get("ETag"); got != tag(v+1) {
			t.Fatalf("ETag = %q, want %q", got, tag(v+1))
		}
		attrsEqual(t, e.attrsOf(e.postID), `{"content":"B","series_part":5}`)
		if e.version() != v+1 {
			t.Fatalf("version = %d, want %d", e.version(), v+1)
		}
	})

	t.Run("stale tag is refused", func(t *testing.T) {
		reset()
		v := e.version()
		code, _, raw := doCT(e, http.MethodPatch, e.postID, mergeCT, `{"attributes":{"content":"B"}}`, tag(v-1))
		if code != http.StatusPreconditionFailed {
			t.Fatalf("PATCH = %d %s", code, raw)
		}
		attrsEqual(t, e.attrsOf(e.postID), base)
		if e.version() != v {
			t.Fatalf("version = %d, want %d", e.version(), v)
		}
	})

	t.Run("plain PATCH and PUT replace", func(t *testing.T) {
		reset()
		code, _, raw := doCT(e, http.MethodPatch, e.postID, "application/json", `{"attributes":{"content":"C"}}`, "")
		if code != http.StatusOK {
			t.Fatalf("PATCH = %d %s", code, raw)
		}
		attrsEqual(t, e.attrsOf(e.postID), `{"content":"C"}`)
		reset()
		code, _, raw = doCT(e, http.MethodPut, e.postID, mergeCT, `{"attributes":{"content":"D"}}`, "")
		if code != http.StatusOK {
			t.Fatalf("PUT = %d %s", code, raw)
		}
		attrsEqual(t, e.attrsOf(e.postID), `{"content":"D"}`)
	})

	t.Run("null attributes", func(t *testing.T) {
		reset()
		v := e.version()
		code, _, raw := doCT(e, http.MethodPatch, e.postID, mergeCT, `{"attributes":null}`, "")
		if code != http.StatusBadRequest || !strings.Contains(string(raw), "validation failed") {
			t.Fatalf("merge null = %d %s", code, raw)
		}
		attrsEqual(t, e.attrsOf(e.postID), base)
		if e.version() != v {
			t.Fatalf("version changed to %d", e.version())
		}
		code, _, raw = doCT(e, http.MethodPatch, e.postID, "application/json", `{"attributes":null,"name":"N"}`, "")
		if code != http.StatusOK {
			t.Fatalf("replace null = %d %s", code, raw)
		}
		attrsEqual(t, e.attrsOf(e.postID), base)
	})
}

func TestMergePatchRESTRevalidates(t *testing.T) {
	e := newCondEnv(t)
	sfx := uuid.NewString()[:6]
	slug := "merge-it-" + sfx
	schemaID, entID := uuid.New(), uuid.New()
	e.exec(`INSERT INTO hatamoto_entity_schemas (id, name, slug, schema) VALUES ($1, $2, $2, $3::jsonb)`, schemaID, slug,
		`{"type":"object","required":["title"],"properties":{"title":{"type":"string"},"room":{"type":"string"}}}`)
	e.exec(`INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, attributes) VALUES ($1, $2, 'Merge IT', $3, 'draft', $4::jsonb)`,
		entID, schemaID, slug, `{"title":"T","room":"A"}`)
	t.Cleanup(func() {
		e.exec(`DELETE FROM hatamoto_entities WHERE id = $1`, entID)
		e.exec(`DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	v := e.versionOf(entID)
	code, _, raw := doCT(e, http.MethodPatch, entID, mergeCT, `{"attributes":{"title":null}}`, "")
	if code != http.StatusBadRequest || !strings.Contains(string(raw), "title") {
		t.Fatalf("PATCH = %d %s", code, raw)
	}
	attrsEqual(t, e.attrsOf(entID), `{"title":"T","room":"A"}`)
	if e.versionOf(entID) != v {
		t.Fatalf("version changed to %d", e.versionOf(entID))
	}
}
