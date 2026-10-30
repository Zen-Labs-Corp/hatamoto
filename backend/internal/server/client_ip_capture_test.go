package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/middleware"
)

const (
	captureEdgeIP    = "172.64.10.20"  // inside the built-in Cloudflare list
	captureVisitorIP = "20.171.207.99" // the crawler as the CDN saw it
	captureForgedIP  = "198.51.100.77" // not published by any agent
)

func withTrustedCDN(t *testing.T, preset string) {
	t.Helper()
	middleware.SetTrustedCDN(nil) // the preset being off leaves the set alone, so start clean
	applyTrustedCDN(&config.Config{Admin: config.AdminConfig{TrustedCDN: preset}}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		middleware.SetTrustedCDN(nil)
		agents.SetLogEdgeCheck(nil)
	})
}

// captureThrough sends one GPTBot request through the render capture path and
// the public-file path and returns the IPs stored for both.
func captureThrough(t *testing.T, remote, xff, cfIP string) (render, public string) {
	t.Helper()
	s, st := newCaptureServer()
	send := func(h http.Handler, path string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", captureGPTUA)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		if cfIP != "" {
			req.Header.Set("CF-Connecting-IP", cfIP)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	send(s.agentCaptureMiddleware(ok), "/about")
	send(s.publicTenantMiddleware(ok), "/api/v1/public/llms.txt")
	s.agentWriter.Close()
	for _, h := range st.got() {
		if h.IP == nil {
			t.Fatalf("hit without IP: %+v", h)
		}
		switch h.Path {
		case "/about":
			render = h.IP.String()
		case "/llms.txt":
			public = h.IP.String()
		}
	}
	return render, public
}

func TestCaptureStoresVisitorIPBehindCDNPreset(t *testing.T) {
	withTrustedCDN(t, "cloudflare")
	render, public := captureThrough(t, "127.0.0.1:4000", captureEdgeIP, captureVisitorIP)
	if render != captureVisitorIP || public != captureVisitorIP {
		t.Errorf("stored IPs render=%q public=%q, want %q", render, public, captureVisitorIP)
	}
}

func TestCaptureStoresEdgeIPWithoutPreset(t *testing.T) {
	withTrustedCDN(t, "")
	render, public := captureThrough(t, "127.0.0.1:4000", captureEdgeIP, captureVisitorIP)
	if render != captureEdgeIP || public != captureEdgeIP {
		t.Errorf("stored IPs render=%q public=%q, want the edge %q (header must not be read)", render, public, captureEdgeIP)
	}
}

func TestCaptureIgnoresForgedHeaderFromUntrustedPeer(t *testing.T) {
	withTrustedCDN(t, "cloudflare")
	render, public := captureThrough(t, captureForgedIP+":4000", "", captureVisitorIP)
	if render != captureForgedIP || public != captureForgedIP {
		t.Errorf("stored IPs render=%q public=%q, want the peer %q", render, public, captureForgedIP)
	}
}

// A crawler UA from a non-published address stays spoofed end to end; the
// published address behind the same chain is verified.
func TestCapturedIPDrivesVerification(t *testing.T) {
	withTrustedCDN(t, "cloudflare")
	ranges := agents.NewRangeStore()
	ranges.Set("openai-gptbot", []netip.Prefix{netip.MustParsePrefix("20.171.207.0/24")})
	v := agents.NewVerifier(ranges, nil)
	gpt := agents.Default().Match(captureGPTUA)
	if gpt == nil {
		t.Fatal("GPTBot UA not recognised")
	}
	for _, c := range []struct {
		name, cfIP string
		want       agents.State
	}{
		{"in range", captureVisitorIP, agents.StateVerified},
		{"forged UA from a non-range address", captureForgedIP, agents.StateSpoofed},
	} {
		render, _ := captureThrough(t, "127.0.0.1:4000", captureEdgeIP, c.cfIP)
		got := v.Verify(context.Background(), gpt, netip.MustParseAddr(render))
		if got != c.want {
			t.Errorf("%s: stored %s verified as %s, want %s", c.name, render, got, c.want)
		}
	}
	// Without the preset the stored edge address would be judged (and spoofed).
	withTrustedCDN(t, "")
	render, _ := captureThrough(t, "127.0.0.1:4000", captureEdgeIP, captureVisitorIP)
	if got := v.Verify(context.Background(), gpt, netip.MustParseAddr(render)); got != agents.StateSpoofed {
		t.Errorf("edge address verified as %s, want spoofed", got)
	}
}
