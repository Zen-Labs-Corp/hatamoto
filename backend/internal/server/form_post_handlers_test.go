package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/form"
)

// fakeFormRepo implements only what public submissions touch; the embedded
// nil interface makes any other call panic loudly.
type fakeFormRepo struct {
	form.Repository
	forms map[string]*form.Form
	subs  []*form.CreateSubmissionInput
}

func (f *fakeFormRepo) GetFormBySlug(_ context.Context, slug string) (*form.Form, error) {
	if fm, ok := f.forms[slug]; ok {
		return fm, nil
	}
	return nil, form.ErrFormNotFound
}

func (f *fakeFormRepo) GetFormByID(_ context.Context, id uuid.UUID) (*form.Form, error) {
	for _, fm := range f.forms {
		if fm.ID == id {
			return fm, nil
		}
	}
	return nil, form.ErrFormNotFound
}

func (f *fakeFormRepo) CreateSubmission(_ context.Context, id uuid.UUID, in *form.CreateSubmissionInput) (*form.FormSubmission, error) {
	f.subs = append(f.subs, in)
	return &form.FormSubmission{ID: uuid.New(), FormID: id}, nil
}

func newFormPostServer(t *testing.T) (*Server, *fakeFormRepo) {
	t.Helper()
	repo := &fakeFormRepo{forms: map[string]*form.Form{
		"contact": {ID: uuid.New(), Slug: "contact", Status: form.StatusActive,
			Fields: []form.FormField{
				{Name: "name", Label: "Name", Type: form.FieldTypeText, Required: true},
				{Name: "email", Label: "Email", Type: form.FieldTypeEmail},
				{Name: "topic", Label: "Topic", Type: form.FieldTypeSelect, Options: []form.FieldOption{{Label: "Sales", Value: "sales"}, {Label: "Help", Value: "help"}}},
				{Name: "tags", Label: "Tags", Type: form.FieldTypeCheckbox, Options: []form.FieldOption{{Label: "A", Value: "a"}, {Label: "B", Value: "b"}}},
			},
			Settings: form.FormSettings{SuccessMessage: "Thanks", SuccessRedirectURL: "/thanks"}},
		"draft": {ID: uuid.New(), Slug: "draft", Status: form.StatusInactive},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{cfg: &config.Config{}, logger: logger, formService: form.NewService(repo, logger)}, repo
}

func postForm(srv *Server, slug string, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/forms/"+slug+"/submit", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://example.com/contact")
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	srv.handlePublicFormPost(rec, req)
	return rec
}

func TestFormPost_Success(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	rec := postForm(srv, "contact", url.Values{
		"name": {"Ada"}, "tags": {"a", "b"}, "_redirect": {"/contact?x=1"}, "page_url": {"/contact"}, "hm_website": {""},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/contact?submitted=1&x=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(repo.subs) != 1 {
		t.Fatalf("submissions = %d", len(repo.subs))
	}
	got := repo.subs[0]
	if got.Data["name"] != "Ada" || len(got.Data["tags"].([]interface{})) != 2 {
		t.Errorf("data = %v", got.Data)
	}
	for _, k := range []string{"_redirect", "hm_website", "page_url"} {
		if _, ok := got.Data[k]; ok {
			t.Errorf("%s leaked into submission data", k)
		}
	}
	if got.Metadata.PageURL != "/contact" || got.Metadata.Referrer != "https://example.com/contact" {
		t.Errorf("metadata = %+v", got.Metadata)
	}
}

func TestFormPost_HoneypotStoresNothing(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	rec := postForm(srv, "contact", url.Values{"name": {"bot"}, "hm_website": {"http://spam"}, "_redirect": {"/c"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/c?submitted=1" || len(repo.subs) != 0 {
		t.Fatalf("got %d %q subs=%d", rec.Code, rec.Header().Get("Location"), len(repo.subs))
	}
}

func TestFormPost_Errors(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	for _, slug := range []string{"missing", "draft"} {
		rec := postForm(srv, slug, url.Values{"_redirect": {"/c"}})
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/c?form_error=1" {
			t.Errorf("%s: got %d %q", slug, rec.Code, rec.Header().Get("Location"))
		}
	}
	if len(repo.subs) != 0 {
		t.Error("nothing should be stored")
	}
}

func TestSafeLocalPath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/contact":            "/contact",
		"/a/b?x=1":            "/a/b?x=1",
		"/a%20b":              "/a%20b",
		"":                    "/",
		"contact":             "/",
		"//evil.com":          "/",
		"/\\evil.com":         "/",
		"https://evil.com":    "/",
		"javascript:alert(1)": "/",
		"/a\r\nSet-Cookie: x": "/",
		"/#frag":              "/",
	} {
		if got := safeLocalPath(in); got != want {
			t.Errorf("safeLocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The JSON endpoint must keep its status codes and bodies after the
// submitPublicForm extraction.
func TestFormSubmit_JSONUnchanged(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	do := func(slug, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		srv.handlePublicFormSubmit(rec, req)
		return rec
	}
	rec := do("contact", `{"name":"Ada"}`)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusCreated || body["success"] != true || body["message"] != "Thanks" ||
		body["redirect_url"] != "/thanks" || body["submission_id"] == "" || len(repo.subs) != 1 {
		t.Fatalf("success: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("missing", `{}`); rec.Code != 404 || !strings.Contains(rec.Body.String(), "form not found") {
		t.Errorf("missing: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("draft", `{}`); rec.Code != 404 {
		t.Errorf("draft: %d", rec.Code)
	}
	// Unknown form wins over a bad body, as before the extraction.
	if rec := do("missing", `{bad`); rec.Code != 404 {
		t.Errorf("missing+bad body: %d", rec.Code)
	}
	if rec := do("contact", `{bad`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid request body") {
		t.Errorf("bad body: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFormSubmit_ValidationBothContentTypes(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	do := func(ct, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		req.SetPathValue("slug", "contact")
		rec := httptest.NewRecorder()
		srv.handlePublicFormSubmit(rec, req)
		return rec
	}
	const urlenc = "application/x-www-form-urlencoded"
	for name, c := range map[string]struct {
		ct, body string
		code     int
		field    string
	}{
		"json missing required":    {"application/json", `{"foo":"bar"}`, 400, "name"},
		"json bad enum":            {"application/json", `{"name":"A","topic":"Sales"}`, 400, "topic"},
		"json bad email":           {"application/json", `{"name":"A","email":"nope"}`, 400, "email"},
		"json bad checkbox":        {"application/json", `{"name":"A","tags":["a","z"]}`, 400, "tags"},
		"urlencoded missing":       {urlenc, "email=a%40b.co", 400, "name"},
		"urlencoded bad enum":      {urlenc, "name=A&topic=Sales", 400, "topic"},
		"json ok":                  {"application/json", `{"name":"A","email":"a@b.co","topic":"sales","tags":["a"],"foo":"bar"}`, 201, ""},
		"urlencoded ok":            {urlenc, "name=B&tags=a&tags=b&unknown=x", 201, ""},
		"json nested data wrapper": {"application/json", `{"data":{"name":"A"}}`, 400, "name"},
	} {
		rec := do(c.ct, c.body)
		if rec.Code != c.code {
			t.Errorf("%s: got %d %s", name, rec.Code, rec.Body.String())
			continue
		}
		if c.field != "" {
			var body struct {
				Fields map[string]string `json:"fields"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body.Fields[c.field] == "" {
				t.Errorf("%s: no error for %s in %s", name, c.field, rec.Body.String())
			}
		}
	}
	if len(repo.subs) != 2 {
		t.Fatalf("stored %d submissions, want 2", len(repo.subs))
	}
	for _, sub := range repo.subs {
		for _, k := range []string{"foo", "unknown"} {
			if _, ok := sub.Data[k]; ok {
				t.Errorf("unknown field %q was stored: %v", k, sub.Data)
			}
		}
	}
	for _, sub := range repo.subs {
		if sub.Data["name"] == "B" && len(sub.Data["tags"].([]any)) != 2 {
			t.Errorf("urlencoded multi-value lost: %v", sub.Data)
		}
	}
}

// The JSON endpoint honours the honeypot like the no-JS path: the answer looks
// like a success, nothing is stored (and so nothing is mailed or delivered).
func TestFormSubmit_HoneypotBothContentTypes(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	do := func(slug, ct, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		srv.handlePublicFormSubmit(rec, req)
		return rec
	}
	const urlenc = "application/x-www-form-urlencoded"
	for name, c := range map[string]struct{ ct, body string }{
		"json":                   {"application/json", `{"name":"bot","hm_website":"http://spam"}`},
		"json non-string":        {"application/json", `{"name":"bot","hm_website":true}`},
		"json invalid otherwise": {"application/json", `{"hm_website":"x"}`}, // required name missing: still a fake success
		"urlencoded":             {urlenc, "name=bot&hm_website=http%3A%2F%2Fspam"},
	} {
		rec := do("contact", c.ct, c.body)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusCreated || body["success"] != true || body["message"] != "Thanks" ||
			body["redirect_url"] != "/thanks" || body["submission_id"] == "" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if len(repo.subs) != 0 {
		t.Fatalf("honeypot submissions stored: %d", len(repo.subs))
	}
	// An empty or null honeypot is a human, and an unknown form is still a 404.
	for _, body := range []string{`{"name":"Ada","hm_website":""}`, `{"name":"Ada","hm_website":null}`} {
		if rec := do("contact", "application/json", body); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if len(repo.subs) != 2 {
		t.Fatalf("human submissions = %d, want 2", len(repo.subs))
	}
	if _, ok := repo.subs[0].Data["hm_website"]; ok {
		t.Error("hm_website leaked into submission data")
	}
	if rec := do("missing", "application/json", `{"hm_website":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing form: %d", rec.Code)
	}
}
