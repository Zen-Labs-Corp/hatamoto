package statichost

import (
	"context"
	"strings"
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
		{"https://a\"x", "", false},
		{"https://a'b", "", false},
		{"https://a<b>.com", "", false},
		{"https://a(b)", "", false},
		{"https://a=b", "", false},
		{"https://a_b.com", "", false},
		{"https://a..b", "", false},
		{"https://a'\"tabindex='0'autofocus='1'onfocus='alert(1)'x='", "", false},
		{"https://bücher.example.net", "https://xn--bcher-kva.example.net", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"https://[::1]:8443", "https://[::1]:8443", true},
		{"https://[::1%25eth0]", "", false},
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
		{"happy", StaticHosting{Static: true, Live: l, Site: s}, true},
		{"served", StaticHosting{Static: false, Live: l, Site: s}, false},
		{"unset live", StaticHosting{Static: true, Live: "", Site: s}, false},
		{"unset site", StaticHosting{Static: true, Live: l, Site: ""}, false},
		{"same", StaticHosting{Static: true, Live: s, Site: s}, false},
		{"scheme only", StaticHosting{Static: true, Live: "http://static.example.net", Site: s}, false},
		{"scheme only with port", StaticHosting{Static: true, Live: "http://a.example.net:8080", Site: "https://a.example.net:8080"}, false},
		{"other port", StaticHosting{Static: true, Live: "https://static.example.net:8443", Site: s}, true},
	}
	for _, tc := range tests {
		if got := tc.h.Active(); got != tc.want {
			t.Errorf("%s: Active() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllowsOriginAndLiveHost(t *testing.T) {
	t.Parallel()
	h := StaticHosting{Static: true, Live: "https://live.example.net:8443", Site: "https://static.example.net"}
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
	off := StaticHosting{Live: h.Live, Site: h.Site}
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
	h.Store(StaticHosting{Static: true, Live: "HTTPS://Live.Example.NET:443/", Site: "https://static.example.net/"})
	want := StaticHosting{Static: true, Live: "https://live.example.net", Site: "https://static.example.net"}
	if got := h.Load(); got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
	h.Store(StaticHosting{Static: true, Live: "ftp://bad", Site: "https://static.example.net"})
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

func TestSameHost(t *testing.T) {
	t.Parallel()
	const site = "https://www.example.com"
	h := StaticHosting{Static: true, SameHost: true, Site: site}
	if !h.IslandsOn() || h.Active() || h.LiveHost() != "" || h.AllowsOrigin(site) {
		t.Errorf("same host: islands=%v active=%v livehost=%q allows=%v", h.IslandsOn(), h.Active(), h.LiveHost(), h.AllowsOrigin(site))
	}
	if h.IslandBase() != site || h.InactiveReason() != "" {
		t.Errorf("same host: base=%q reason=%q", h.IslandBase(), h.InactiveReason())
	}
	noSite := StaticHosting{Static: true, SameHost: true}
	if noSite.IslandsOn() || noSite.IslandBase() != "" || !strings.Contains(noSite.InactiveReason(), "Site URL") {
		t.Errorf("same host without Site: %+v reason=%q", noSite, noSite.InactiveReason())
	}
	served := StaticHosting{SameHost: true, Site: site}
	if served.IslandsOn() || served.InactiveReason() != "" {
		t.Error("served mode must stay off")
	}
	split := StaticHosting{Static: true, Live: "https://live.example.net", Site: site}
	if !split.IslandsOn() || split.IslandBase() != split.Live {
		t.Errorf("split: base=%q", split.IslandBase())
	}
	if (StaticHosting{Static: true, Site: site}).IslandBase() != "" {
		t.Error("no live origin and not same host must have no island base")
	}
}

func TestHolderSameHostClearsLive(t *testing.T) {
	t.Parallel()
	var h Holder
	h.Store(StaticHosting{Static: true, SameHost: true, Live: "https://x.example.com", Site: "https://www.example.com"})
	if got := h.Load(); got.Live != "" || !got.IslandsOn() {
		t.Errorf("Load = %+v", got)
	}
}
