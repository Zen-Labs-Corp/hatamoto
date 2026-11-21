package render

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHmLiveScriptServed(t *testing.T) {
	t.Parallel()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	get := func(name string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+name, nil))
		return rec
	}
	live, track := get(LiveScriptName), get(TrackScriptName)
	if live.Code != http.StatusOK {
		t.Fatalf("status = %d", live.Code)
	}
	if ct := live.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content type = %q", ct)
	}
	if got, want := live.Header().Get("Cache-Control"), track.Header().Get("Cache-Control"); got == "" || got != want {
		t.Errorf("cache-control = %q, want %q (as hm-track.js)", got, want)
	}
}

func TestHmLiveScriptShape(t *testing.T) {
	t.Parallel()
	js := string(hmLiveSrc)
	for _, want := range []string{"credentials: 'omit'", "u.origin !== live"} {
		if !strings.Contains(js, want) {
			t.Errorf("hm-live.js lacks %q", want)
		}
	}
	if re := regexp.MustCompile(`https?://|//`); re.Match(hmLiveSrc) {
		t.Error("hm-live.js must not carry an absolute URL literal")
	}
	if got := AssetPrefix + LiveScriptName; got != "/theme/assets/hm-live.js" {
		t.Errorf("asset path = %q", got)
	}
}
