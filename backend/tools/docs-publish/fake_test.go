package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fakeKey    = "test-key-value"
	fakeSchema = "11111111-1111-1111-1111-111111111111"
)

// fakeSite is an in-memory stand-in for the entity REST API. It enforces the
// X-API-Key header, per-parent slug uniqueness and offset paging like the
// real handlers do.
type fakeSite struct {
	mu       sync.Mutex
	ents     []*Entity
	calls    []string
	throttle int // answer this many requests with 429 first
	pageSize int // forces more than one page when small
	seq      int
	srv      *httptest.Server
}

func newFakeSite(t *testing.T) *fakeSite {
	t.Helper()
	f := &fakeSite{pageSize: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) client() *Client {
	c := NewClient(Config{URL: f.srv.URL, APIKey: fakeKey})
	c.Delay = 0
	c.Sleep = func(time.Duration) {}
	return c
}

// writes lists the recorded mutating calls.
func (f *fakeSite) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var w []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			w = append(w, c)
		}
	}
	return w
}

func (f *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-Key") != fakeKey {
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
		return
	}
	if f.throttle > 0 {
		f.throttle--
		w.Header().Set("Retry-After", "2")
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	find := func(id string) *Entity {
		for _, e := range f.ents {
			if e.ID == id {
				return e
			}
		}
		return nil
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/entities")
	switch {
	case r.Method == http.MethodGet && path == "":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		var match []Entity
		for _, e := range f.ents {
			if s := r.URL.Query().Get("schema_id"); s != "" && e.SchemaID != s {
				continue
			}
			if q := r.URL.Query().Get("search"); q != "" && !strings.Contains(e.Slug, q) && !strings.Contains(e.Name, q) {
				continue
			}
			match = append(match, *e)
		}
		lo, hi := (page-1)*f.pageSize, page*f.pageSize
		if lo > len(match) {
			lo = len(match)
		}
		if hi > len(match) {
			hi = len(match)
		}
		pages := (len(match) + f.pageSize - 1) / f.pageSize
		reply(200, map[string]any{"items": match[lo:hi], "total": len(match), "page": page, "total_pages": pages})
	case r.Method == http.MethodPost && path == "":
		var in struct {
			SchemaID, Name, Slug, ParentID string
			Attributes                     json.RawMessage
		}
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		for k, dst := range map[string]*string{"schema_id": &in.SchemaID, "name": &in.Name, "slug": &in.Slug, "parent_id": &in.ParentID} {
			_ = json.Unmarshal(raw[k], dst)
		}
		in.Attributes = raw["attributes"]
		if in.ParentID != "" && find(in.ParentID) == nil {
			reply(422, map[string]string{"error": "parent not found", "code": "parent_not_found"})
			return
		}
		for _, e := range f.ents {
			if e.SchemaID == in.SchemaID && e.ParentID == in.ParentID && e.Slug == in.Slug {
				reply(409, map[string]string{"error": "slug already exists", "code": "slug_exists"})
				return
			}
		}
		f.seq++
		e := &Entity{ID: fmt.Sprintf("e%03d", f.seq), SchemaID: in.SchemaID, Name: in.Name, Slug: in.Slug, Status: "draft", ParentID: in.ParentID, Attributes: in.Attributes}
		f.ents = append(f.ents, e)
		reply(201, e)
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/status"):
		e := find(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/status"))
		if e == nil {
			reply(404, map[string]string{"error": "entity not found"})
			return
		}
		var in struct{ Status string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		e.Status = in.Status
		reply(200, e)
	case r.Method == http.MethodPatch:
		e := find(strings.TrimPrefix(path, "/"))
		if e == nil {
			reply(404, map[string]string{"error": "entity not found"})
			return
		}
		var in struct {
			Name       string
			Attributes json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		e.Name, e.Attributes = in.Name, in.Attributes
		reply(200, e)
	default:
		http.NotFound(w, r)
	}
}
