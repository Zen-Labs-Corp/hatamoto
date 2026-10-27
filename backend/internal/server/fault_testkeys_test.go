//go:build hatamoto_testkeys

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWrapFaultRoot500(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	t.Setenv("HATAMOTO_TEST_FAULT", "root-500")
	h := wrapFault(inner)
	for path, want := range map[string]int{"/": 500, "/about": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
	t.Setenv("HATAMOTO_TEST_FAULT", "root-500@v9.9.9") // not this binary's directory
	rec := httptest.NewRecorder()
	wrapFault(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Errorf("fault for another directory fired: %d", rec.Code)
	}
	t.Setenv("HATAMOTO_TEST_FAULT", "")
	rec = httptest.NewRecorder()
	wrapFault(inner).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Errorf("no fault set: %d", rec.Code)
	}
}
