package admin

import (
	"net/http/httptest"
	"testing"
)

func TestHTMXRequestClassification(t *testing.T) {
	t.Parallel()
	type hdr = map[string]string
	cases := []struct {
		name               string
		h                  hdr
		htmx, frag, drawer bool
	}{
		{"none", nil, false, false, false},
		{"v2 request only", hdr{"HX-Request": "true"}, true, false, false},
		{"v2 drawer", hdr{"HX-Request": "true", "HX-Target": "hm-ss-drawer"}, true, true, true},
		{"v2 other target", hdr{"HX-Request": "true", "HX-Target": "row-1"}, true, true, false},
		{"v4 partial drawer", hdr{"HX-Request": "true", "HX-Request-Type": "partial", "HX-Target": "div#hm-ss-drawer"}, true, true, true},
		{"v4 partial no target", hdr{"HX-Request": "true", "HX-Request-Type": "partial"}, true, true, false},
		{"v4 full with target", hdr{"HX-Request": "true", "HX-Request-Type": "full", "HX-Target": "body#x"}, true, false, false},
		{"v4 full no target", hdr{"HX-Request": "true", "HX-Request-Type": "full"}, true, false, false},
		{"v4 tag only", hdr{"HX-Request": "true", "HX-Request-Type": "partial", "HX-Target": "div"}, true, true, false},
		{"v4 empty id", hdr{"HX-Request": "true", "HX-Request-Type": "partial", "HX-Target": "div#"}, true, true, false},
		{"target without HX-Request", hdr{"HX-Target": "hm-ss-drawer"}, false, false, false},
		{"partial without HX-Request", hdr{"HX-Request-Type": "partial", "HX-Target": "div#hm-ss-drawer"}, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/x", nil)
			for k, v := range c.h {
				r.Header.Set(k, v)
			}
			if got := IsHTMX(r); got != c.htmx {
				t.Errorf("IsHTMX = %v, want %v", got, c.htmx)
			}
			if got := isFragment(r); got != c.frag {
				t.Errorf("isFragment = %v, want %v", got, c.frag)
			}
			if got := hxTargetIs(r, "hm-ss-drawer"); got != c.drawer {
				t.Errorf("hxTargetIs = %v, want %v", got, c.drawer)
			}
			if hxTargetIs(r, "") {
				t.Error("hxTargetIs with empty id must be false")
			}
		})
	}
}

func TestHXCurrentURL(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/x", nil)
	if got := hxCurrentURL(r); got != "" {
		t.Errorf("unset = %q", got)
	}
	r.Header.Set("HX-Current-URL", "https://example.test/admin/a")
	if got := hxCurrentURL(r); got != "https://example.test/admin/a" {
		t.Errorf("set = %q", got)
	}
}
