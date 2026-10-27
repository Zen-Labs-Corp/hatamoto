package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const testKey = "hmk_secret_test_key"

type recorded struct {
	Method, Path, Key string
	Body              map[string]any
}

// fakeAPI is an in-memory entities API. PATCH replaces attributes, as the real
// handler does (COALESCE($4, attributes) on the whole object); create stores a
// draft; slugs are ignored.
type fakeAPI struct {
	mu       sync.Mutex
	reqs     []recorded
	entities map[string]*Entity
	next     int
	pageSize int    // overrides the requested limit when > 0
	failAt   string // "METHOD path-prefix" answered with 500
}

func newFakeAPI(t *testing.T) (*fakeAPI, *Client) {
	f := &fakeAPI{entities: map[string]*Entity{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Client{Base: srv.URL, Key: testKey, SchemaID: "schema-1"}
}

func (f *fakeAPI) seed(version string, attrs map[string]any) *Entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	e := &Entity{ID: "e" + string(rune('0'+f.next)), Name: version, Status: "published", Attributes: attrs}
	f.entities[e.ID] = e
	return e
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := recorded{Method: r.Method, Path: r.URL.RequestURI(), Key: r.Header.Get("X-API-Key")}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	rec.Body = body
	f.reqs = append(f.reqs, rec)
	if f.failAt != "" && f.failAt == r.Method+" "+r.URL.Path {
		http.Error(w, "boom", 500)
		return
	}
	out := func(v any) { json.NewEncoder(w).Encode(v) }
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/api/v1/entities":
		q := r.URL.Query()
		search := q.Get("search")
		matched := []*Entity{}
		for _, e := range f.entities {
			if strings.Contains(e.Name, search) {
				matched = append(matched, e)
			}
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
		page, _ := strconv.Atoi(q.Get("page"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		if f.pageSize > 0 {
			limit = f.pageSize
		}
		items := []*Entity{}
		if lo := (page - 1) * limit; lo < len(matched) {
			items = matched[lo:min(lo+limit, len(matched))]
		}
		out(map[string]any{"items": items, "total": len(matched), "page": page, "total_pages": (len(matched) + limit - 1) / limit})
	case r.Method == "POST" && p == "/api/v1/entities":
		f.next++
		attrs, _ := body["attributes"].(map[string]any)
		e := &Entity{ID: "e" + string(rune('0'+f.next)), Name: body["name"].(string), Status: "draft", Attributes: attrs}
		f.entities[e.ID] = e
		w.WriteHeader(201)
		out(e)
	case strings.HasPrefix(p, "/api/v1/entities/"):
		rest := strings.TrimPrefix(p, "/api/v1/entities/")
		id, sub, _ := strings.Cut(rest, "/")
		e := f.entities[id]
		if e == nil {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.Method == "GET" && sub == "":
			out(e)
		case r.Method == "PATCH" && sub == "status":
			e.Status = body["status"].(string)
			out(e)
		case r.Method == "PATCH" && sub == "":
			if a, ok := body["attributes"].(map[string]any); ok {
				e.Attributes = a // replace
			}
			out(e)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) writes() []recorded {
	var w []recorded
	for _, r := range f.reqs {
		if r.Method != "GET" {
			w = append(w, r)
		}
	}
	return w
}

func TestFindExactVersionOnly(t *testing.T) {
	f, c := newFakeAPI(t)
	f.seed("v0.2.10", map[string]any{"version": "v0.2.10"})
	want := f.seed("v0.2.1", map[string]any{"version": "v0.2.1"})
	got, err := c.Find("v0.2.1")
	if err != nil || got == nil || got.ID != want.ID {
		t.Fatalf("got %+v, %v", got, err)
	}
	if none, _ := c.Find("v9.9.9"); none != nil {
		t.Error("found a version that does not exist")
	}
	r := f.reqs[0]
	if r.Key != testKey || !strings.Contains(r.Path, "schema_id=schema-1") {
		t.Errorf("request = %+v", r)
	}
}

func TestCreateIsPostThenPublish(t *testing.T) { // ISC-18
	f, c := newFakeAPI(t)
	e, err := c.Create("v1.0.0", map[string]any{"version": "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	w := f.writes()
	if len(w) != 3 || w[0].Method != "POST" || w[0].Path != "/api/v1/entities" ||
		w[1].Method != "PATCH" || w[1].Path != "/api/v1/entities/"+e.ID+"/status" || w[1].Body["status"] != "published" ||
		w[2].Method != "PATCH" || w[2].Path != "/api/v1/entities/"+e.ID {
		t.Fatalf("writes = %+v", w)
	}
	if a, _ := w[0].Body["attributes"].(map[string]any); a[pendingPublish] != true {
		t.Errorf("the draft must carry %s before the status PATCH: %v", pendingPublish, a)
	}
	if got := f.entities[e.ID].Attributes; got[pendingPublish] != false || got["version"] != "v1.0.0" {
		t.Errorf("after publish attrs = %v", got)
	}
	if w[0].Body["schema_id"] != "schema-1" || w[0].Body["name"] != "v1.0.0" || w[0].Body["slug"] != nil || w[0].Body["content"] != nil {
		t.Errorf("create body = %+v", w[0].Body)
	}
	if f.entities[e.ID].Status != "published" {
		t.Error("not published")
	}
}

func TestUpdateAttrsMergesBecausePatchReplaces(t *testing.T) {
	f, c := newFakeAPI(t)
	e := f.seed("v1.0.0", map[string]any{"version": "v1.0.0", "channel": "edge", "hand_added": "keep me"})
	if err := c.UpdateAttrs(e.ID, map[string]any{"channel": "stable"}); err != nil {
		t.Fatal(err)
	}
	a := f.entities[e.ID].Attributes
	if a["channel"] != "stable" || a["hand_added"] != "keep me" || a["version"] != "v1.0.0" {
		t.Errorf("attrs = %v", a)
	}
	patch := f.writes()[0]
	if got := patch.Body["attributes"].(map[string]any); len(got) != 3 {
		t.Errorf("PATCH must send the full object, got %v", got)
	}
}

func TestErrorsNeverContainTheKey(t *testing.T) {
	f, c := newFakeAPI(t)
	f.failAt = "GET /api/v1/entities"
	_, err := c.Find("v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
	// An error that echoes the key in a server body is scrubbed too.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key "+r.Header.Get("X-API-Key"), http.StatusUnauthorized)
	}))
	defer srv.Close()
	c2 := &Client{Base: srv.URL, Key: testKey, SchemaID: "s"}
	_, err = c2.Find("v1.0.0")
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
	// A transport error (bad port) carries the URL, never the header.
	c3 := &Client{Base: "http://127.0.0.1:1", Key: testKey, SchemaID: "s"}
	if _, err := c3.Find("v1.0.0"); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
}

func TestRedirectIsAnErrorAndKeyIsNotForwarded(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = leaked || r.Header.Get("X-API-Key") != ""
		w.Write([]byte(`{}`))
	}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer redir.Close()
	c := &Client{Base: redir.URL, Key: testKey, SchemaID: "s"}
	err := c.UpdateAttrs("e1", map[string]any{"channel": "edge"})
	if err == nil || !strings.Contains(err.Error(), "301") || !strings.Contains(err.Error(), target.URL) || strings.Contains(err.Error(), testKey) {
		t.Errorf("err = %v", err)
	}
	if leaked {
		t.Error("the key followed the redirect")
	}
	// An injected client gets the same guard.
	c.HTTP = &http.Client{}
	if err := c.UpdateAttrs("e1", nil); err == nil {
		t.Error("injected client followed the redirect")
	}
}

func TestCreateRejectsEmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Key: testKey, SchemaID: "s"}
	if _, err := c.Create("v1.0.0", nil); err == nil || !strings.Contains(err.Error(), "no entity id") {
		t.Errorf("err = %v", err)
	}
}

func TestFindDuplicateVersionIsAnError(t *testing.T) {
	f, c := newFakeAPI(t)
	f.seed("v1.0.0", map[string]any{"version": "v1.0.0"})
	f.seed("Light mode release", map[string]any{"version": "v1.0.0"})
	if e, err := c.Find("v1.0.0"); err == nil || e != nil || !strings.Contains(err.Error(), "2 entries") {
		t.Errorf("got %+v, %v", e, err)
	}
}

func TestFindRenamedEntryByListing(t *testing.T) {
	f, c := newFakeAPI(t)
	want := f.seed("Light mode release", map[string]any{"version": "v1.0.0"})
	f.seed("v1.0.1", map[string]any{"version": "v1.0.1"})
	got, err := c.Find("v1.0.0")
	if err != nil || got == nil || got.ID != want.ID {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestFindPaginates(t *testing.T) {
	f, c := newFakeAPI(t)
	f.pageSize = 2
	for _, v := range []string{"v1.0.1", "v1.0.10", "v1.0.11", "v1.0.12", "v1.0.13"} {
		f.seed(v, map[string]any{"version": v})
	}
	f.seed("v1.0.1 -> renamed", map[string]any{"version": "v1.0.1"})
	if got, err := c.Find("v1.0.12"); err != nil || got == nil || got.Attributes["version"] != "v1.0.12" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// The duplicate sits on the last page.
	if _, err := c.Find("v1.0.1"); err == nil {
		t.Error("the duplicate on a later page was missed")
	}
}

func TestListStopsAtPageCap(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // ignores page, no total_pages
		hits++
		items := make([]map[string]any, 100)
		for i := range items {
			items[i] = map[string]any{"id": "x", "attributes": map[string]any{}}
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Key: testKey, SchemaID: "s"}
	if _, err := c.Find("v1.0.0"); err == nil || !strings.Contains(err.Error(), "more than 100 pages") || hits != 100 {
		t.Errorf("err = %v after %d requests", err, hits)
	}
}
