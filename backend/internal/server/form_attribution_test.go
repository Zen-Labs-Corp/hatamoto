package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testAttr = `{"f":{"h":"www.perplexity.ai","p":"/pricing"},"l":{"s":"newsletter","m":"email","p":"/blog"}}`

func postJSONSubmit(srv *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/contact/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "example.com"
	req.SetPathValue("slug", "contact")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	return rec
}

func TestFormAttribution_HTMLPostStoresAndStrips(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	rec := postForm(srv, "contact", url.Values{"name": {"Ada"}, "hm_attr": {testAttr}, "_redirect": {"/c"}})
	if rec.Header().Get("Location") != "/c?submitted=1" || len(repo.subs) != 1 {
		t.Fatalf("got %q subs=%d", rec.Header().Get("Location"), len(repo.subs))
	}
	got := repo.subs[0]
	if _, ok := got.Data["hm_attr"]; ok {
		t.Error("hm_attr leaked into data")
	}
	a := got.Attribution
	if a == nil || a.First == nil || a.Last == nil {
		t.Fatalf("attribution = %+v", a)
	}
	if a.First.Channel != "ai_assistants" || a.First.Source != "perplexity" || a.First.Landing != "/pricing" {
		t.Errorf("first = %+v", a.First)
	}
	if a.Last.Channel != "email" || a.Last.Source != "newsletter" {
		t.Errorf("last = %+v", a.Last)
	}
}

func TestFormAttribution_JSONStoresAndStrips(t *testing.T) {
	t.Parallel()
	srv, repo := newFormPostServer(t)
	body := `{"name":"Ada","hm_attr":` + jsonString(testAttr) + `}`
	if rec := postJSONSubmit(srv, body); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := repo.subs[0]
	if _, ok := got.Data["hm_attr"]; ok {
		t.Error("hm_attr leaked into data")
	}
	if got.Attribution == nil || got.Attribution.First.Source != "perplexity" {
		t.Errorf("attribution = %+v", got.Attribution)
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func TestFormAttribution_MissingOrGarbledNeverRejects(t *testing.T) {
	t.Parallel()
	for _, attr := range []string{"", "not json", `{"f":`, strings.Repeat("x", 5000), `{"f":{"h":123}}`} {
		srv, repo := newFormPostServer(t)
		v := url.Values{"name": {"Ada"}, "_redirect": {"/c"}}
		if attr != "" {
			v.Set("hm_attr", attr)
		}
		if rec := postForm(srv, "contact", v); rec.Header().Get("Location") != "/c?submitted=1" {
			t.Errorf("html %.20q: location %q", attr, rec.Header().Get("Location"))
		}
		body := `{"name":"Ada"}`
		if attr != "" {
			body = `{"name":"Ada","hm_attr":` + jsonString(attr) + `}`
		}
		if rec := postJSONSubmit(srv, body); rec.Code != http.StatusCreated {
			t.Errorf("json %.20q: status %d", attr, rec.Code)
		}
		if len(repo.subs) != 2 {
			t.Fatalf("%.20q: stored %d", attr, len(repo.subs))
		}
		for _, in := range repo.subs {
			if _, ok := in.Data["hm_attr"]; ok {
				t.Errorf("%.20q: hm_attr in data", attr)
			}
		}
	}
}
