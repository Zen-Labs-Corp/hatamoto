package admin

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/seo"
)

func postIndexNow(s *seoRig, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/admin"+seoIndexNowPath, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.a.handleSEOIndexNowSave(rec, r)
	return rec
}

func TestIndexNowFromForm(t *testing.T) {
	stored := seo.IndexNow{Enabled: true, Key: "keyvalue-one", Endpoint: "www.bing.com"}
	if got := indexNowFromForm(url.Values{}, stored); got != stored {
		t.Errorf("empty post changed stored: %+v", got)
	}
	got := indexNowFromForm(url.Values{"enabled": {"false"}, "endpoint": {" yandex.com "}, "key": {"evil"}}, stored)
	want := seo.IndexNow{Key: "keyvalue-one", Endpoint: "yandex.com"}
	if got != want {
		t.Errorf("got %+v, want %+v (the key is never read from the form)", got, want)
	}
}

func TestSEOIndexNowDefaultsOff(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	rec := postIndexNow(s, url.Values{"enabled": {"false"}, "endpoint": {"api.indexnow.org"}})
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if got := s.svc.set.IndexNow; got.Enabled || got.Key != "" {
		t.Errorf("saved off but got %+v", got)
	}
}

func TestSEOIndexNowKeyOnFirstEnable(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	rec := postIndexNow(s, url.Values{"enabled": {"true"}})
	if rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	key := s.svc.set.IndexNow.Key
	if !s.svc.set.IndexNow.Enabled || len(key) != 32 {
		t.Fatalf("saved %+v, want enabled with a 32 character key", s.svc.set.IndexNow)
	}
	// A second save keeps the key.
	postIndexNow(s, url.Values{"enabled": {"true"}, "endpoint": {"www.bing.com"}})
	if got := s.svc.set.IndexNow; got.Key != key || got.Endpoint != "www.bing.com" {
		t.Errorf("second save = %+v, want key %q kept", got, key)
	}
	if len(s.events) != 2 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit %+v", s.events)
	}
	if strings.Contains(strings.ToLower(strings.Join(auditText(s.events), " ")), key) {
		t.Error("the key reached the audit entry")
	}
}

func TestSEOIndexNowRegenerate(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.IndexNow = seo.IndexNow{Enabled: true, Key: "previous-key-value"}
	if rec := postIndexNow(s, url.Values{"regenerate": {"true"}}); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	got := s.svc.set.IndexNow
	if got.Key == "previous-key-value" || len(got.Key) != 32 || !got.Enabled {
		t.Errorf("after regenerate %+v", got)
	}
}

func TestSEOIndexNowRejectsUnknownEndpoint(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	rec := postIndexNow(s, url.Values{"enabled": {"true"}, "endpoint": {"169.254.169.254"}})
	if rec.Code == 303 || !s.svc.set.IndexNow.IsZero() || s.svc.merges != 0 {
		t.Errorf("unknown endpoint saved: %d %+v", rec.Code, s.svc.set.IndexNow)
	}
}

func auditText(events []AuditEvent) []string {
	var out []string
	for _, e := range events {
		out = append(out, fmt.Sprint(e))
	}
	return out
}
