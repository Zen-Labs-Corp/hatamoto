package httpx

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func resetRefuser() { refuser.Store(nil) }

func TestRefuseFallbackIs404(t *testing.T) {
	resetRefuser()
	rec := httptest.NewRecorder()
	Refuse(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRefuseUsesRegistered(t *testing.T) {
	resetRefuser()
	t.Cleanup(resetRefuser)
	RegisterRefuser(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	Refuse(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d", rec.Code)
	}
	RegisterRefuser(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) })
	rec = httptest.NewRecorder()
	Refuse(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("last writer must win, got %d", rec.Code)
	}
}

func TestRegisterRace(t *testing.T) {
	resetRefuser()
	t.Cleanup(resetRefuser)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			RegisterRefuser(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		}()
		go func() {
			defer wg.Done()
			Refuse(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
		}()
	}
	wg.Wait()
}
