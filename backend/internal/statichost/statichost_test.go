package statichost

import (
	"context"
	"testing"
)

func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"HTTPS://Example.NET:443/", "https://example.net", true},
		{"http://x.example.net:8080", "http://x.example.net:8080", true},
		{"http://example.net:80", "http://example.net", true},
		{"https://example.net", "https://example.net", true},
		{"ftp://a", "", false},
		{"null", "", false},
		{"https://a/b", "", false},
		{"https://u@a", "", false},
		{"https://a?x=1", "", false},
		{"https://a#f", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		got, ok := NormalizeOrigin(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeOrigin(%q) = %q,%v; want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestActive(t *testing.T) {
	t.Parallel()
	const l, s = "https://live.example.net", "https://static.example.net"
	tests := []struct {
		name string
		h    StaticHosting
		want bool
	}{
		{"happy", StaticHosting{true, l, s}, true},
		{"served", StaticHosting{false, l, s}, false},
		{"unset live", StaticHosting{true, "", s}, false},
		{"unset site", StaticHosting{true, l, ""}, false},
		{"same", StaticHosting{true, s, s}, false},
	}
	for _, tc := range tests {
		if got := tc.h.Active(); got != tc.want {
			t.Errorf("%s: Active() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllowsOriginAndLiveHost(t *testing.T) {
	t.Parallel()
	h := StaticHosting{true, "https://live.example.net:8443", "https://static.example.net"}
	if !h.AllowsOrigin("https://static.example.net") {
		t.Error("S must be allowed")
	}
	for _, o := range []string{"null", "", "https://evil.example", "https://live.example.net:8443"} {
		if h.AllowsOrigin(o) {
			t.Errorf("origin %q must not be allowed", o)
		}
	}
	if got := h.LiveHost(); got != "live.example.net:8443" {
		t.Errorf("LiveHost = %q", got)
	}
	off := StaticHosting{false, h.Live, h.Site}
	if off.AllowsOrigin(h.Site) || off.LiveHost() != "" {
		t.Error("inactive snapshot must allow nothing")
	}
}

func TestHolder(t *testing.T) {
	t.Parallel()
	var nilH *Holder
	if got := nilH.Load(); got != (StaticHosting{}) {
		t.Errorf("nil Load = %+v", got)
	}
	var h Holder
	if got := h.Load(); got != (StaticHosting{}) {
		t.Errorf("zero Load = %+v", got)
	}
	h.Store(StaticHosting{true, "HTTPS://Live.Example.NET:443/", "https://static.example.net/"})
	want := StaticHosting{true, "https://live.example.net", "https://static.example.net"}
	if got := h.Load(); got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
	h.Store(StaticHosting{true, "ftp://bad", "https://static.example.net"})
	if got := h.Load(); got.Live != "" || got.Active() {
		t.Errorf("invalid origin must store empty and be inactive: %+v", got)
	}
}

func TestExportFlag(t *testing.T) {
	t.Parallel()
	if IsExport(context.Background()) {
		t.Error("plain context must not be export")
	}
	if !IsExport(WithExport(context.Background())) {
		t.Error("WithExport must round trip")
	}
}
