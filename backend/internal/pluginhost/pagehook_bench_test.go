package pluginhost

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/pluginhost/plugintest"
	"hatamoto.dev/sdk"
)

// pageOf is a page of n content blocks whose body is repeat sentences long.
func pageOf(n, repeat int) sdk.PageContext {
	pc := sdk.PageContext{
		Path:    "/services/seo",
		Cookies: map[string]string{},
		Entity:  &sdk.EntitySummary{ID: "0b1f6c3e-8d57-4b3a-9a43-6a2f3f2f9c11", SchemaSlug: "service", Slug: "seo", Name: "SEO Services"},
	}
	body := "<p>" + strings.Repeat("Hatamoto keeps content, structure and delivery in one small server. ", repeat) + "</p>"
	for i := range n {
		pc.Blocks = append(pc.Blocks, sdk.Block{
			ID: fmt.Sprintf("b%02d", i), Type: "content", Title: fmt.Sprintf("Section %d", i),
			Content: map[string]any{"headline": fmt.Sprintf("Headline %d", i), "body": body, "cta_label": "Read more", "cta_url": "/contact"},
		})
	}
	pc.Blocks = append(pc.Blocks, sdk.Block{ID: "secret", Type: "content", Title: "Members only"})
	return pc
}

// slimManifest builds capa for the current SDK, so it gets slim blocks; the
// default capa manifest (^0.6) gets the full payload.
func slimManifest(m *sdk.Manifest) { m.SDK = "^0.9" }

// pageFilterRig loads the capa plugin (its render.page filter hides "secret"
// and sets a cookie) behind a PageFilter.
func pageFilterRig(tb testing.TB, mutate func(*sdk.Manifest)) *PageFilter {
	tb.Helper()
	engine := hooks.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	rt, err := NewRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), engine, cacheDir)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { rt.Close(context.Background()) })
	st := rt.Load(context.Background(), capaManifest(mutate), plugintest.Build(tb, "capa"))
	if !st.Loaded {
		tb.Fatalf("capa not loaded: %s", st.Error)
	}
	pf := NewPageFilter(engine)
	if !pf.Active() {
		tb.Fatal("render.page not registered")
	}
	return pf
}

func applyPage(tb testing.TB, pf *PageFilter, in sdk.PageContext) {
	tb.Helper()
	out := pf.Apply(context.Background(), in)
	if len(out.Blocks) != len(in.Blocks)-1 || len(out.SetCookies) != 1 {
		tb.Fatalf("render.page did not run: %d blocks, %d cookies", len(out.Blocks), len(out.SetCookies))
	}
}

// BenchmarkRenderPage is the cost of one render.page round trip (host JSON
// marshal, the guest's decode and encode, host merge) for pages of growing
// size, on the slim payload (the default) and the full one
// (render_page.full_blocks). The v1.0 target is a p50 under 1 ms: the slim
// payload meets it for a typical page, the full one does not because its cost
// is the guest's JSON decode and encode (about 0.5 ms fixed plus 0.5 ms per KB),
// see docs/PLUGIN_API.md.
func BenchmarkRenderPage(b *testing.B) {
	for _, mode := range []struct {
		name   string
		mutate func(*sdk.Manifest)
	}{{"slim", slimManifest}, {"full", nil}} {
		pf := pageFilterRig(b, mode.mutate)
		for _, c := range []struct{ blocks, repeat int }{{4, 1}, {8, 2}, {12, 6}} {
			in := pageOf(c.blocks, c.repeat)
			applyPage(b, pf, in)
			b.Run(fmt.Sprintf("%s/blocks=%d", mode.name, c.blocks), func(b *testing.B) {
				for b.Loop() {
					pf.Apply(context.Background(), in)
				}
			})
		}
	}
}

// p50Of measures the round trip of in through pf.
func p50Of(t *testing.T, pf *PageFilter, in sdk.PageContext) (p50, p99 time.Duration) {
	t.Helper()
	applyPage(t, pf, in)
	for range 50 { // warm the instance pool
		pf.Apply(context.Background(), in)
	}
	const n = 500
	d := make([]time.Duration, n)
	for i := range d {
		start := time.Now()
		pf.Apply(context.Background(), in)
		d[i] = time.Since(start)
	}
	slices.Sort(d)
	return d[n/2], d[n*99/100]
}

// TestRenderPageP50 asserts the roadmap target (p50 under 1 ms) for typical
// pages of 4 to 12 blocks on the slim payload, and a bound with headroom for
// shared CI runners (25 ms on CI, 10 ms otherwise; a regression guard only) on
// the full payload. Under -race timings are not comparable, so it is skipped. With
// HATAMOTO_PERF_STRICT=0 (a shared or busy host, the pre-push default) it is
// skipped too: even the 10 ms bound failed there under load while passing on
// an idle machine, so it only measured the neighbours. HATAMOTO_PERF_STRICT=1
// keeps the 10 ms full bound on CI.
func TestRenderPageP50(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is not meaningful under -race")
	}
	if os.Getenv("HATAMOTO_PERF_STRICT") == "0" {
		t.Skip("HATAMOTO_PERF_STRICT=0: timing bounds are not enforced on a shared or busy host")
	}
	slim, full := pageFilterRig(t, slimManifest), pageFilterRig(t, nil)
	// The 1 ms target is the goal; shared CI runners are too noisy to enforce it,
	// so there only the 10 ms regression bound applies. HATAMOTO_PERF_STRICT=1
	// enforces 1 ms anywhere; HATAMOTO_PERF_STRICT=0 marks any other shared or
	// busy host (e.g. a LAN build server) as a runner: only the 10 ms bound.
	slimBound := 10 * time.Millisecond
	if perfStrict() {
		slimBound = time.Millisecond
	}
	// The full payload is a regression guard: 10 ms off CI or when strict, 25 ms
	// on CI, where hosted runners are noisy and the two-leg PostgreSQL matrix
	// doubles the chances of a slow run.
	fullBound := 10 * time.Millisecond
	if os.Getenv("CI") != "" && os.Getenv("HATAMOTO_PERF_STRICT") != "1" {
		fullBound = 25 * time.Millisecond
	}
	for _, c := range []struct{ blocks, repeat int }{{4, 1}, {8, 2}, {12, 6}} {
		in := pageOf(c.blocks, c.repeat)
		p50, p99 := p50Of(t, slim, in)
		for try := 0; try < 2 && p50 >= slimBound; try++ { // a noisy runner gets two more samples
			p50, p99 = p50Of(t, slim, in)
		}
		t.Logf("render.page slim, %d blocks: p50 %s, p99 %s (target p50 < 1ms)", c.blocks, p50, p99)
		if p50 >= slimBound {
			t.Errorf("slim render.page p50 %s with %d blocks misses the %s bound", p50, c.blocks, slimBound)
		}
		p50, p99 = p50Of(t, full, in)
		t.Logf("render.page full, %d blocks: p50 %s, p99 %s", c.blocks, p50, p99)
		if p50 > fullBound {
			t.Errorf("full render.page p50 %s with %d blocks exceeds the %s bound", p50, c.blocks, fullBound)
		}
	}
}

// perfStrict reports whether timing targets are enforced: always with
// HATAMOTO_PERF_STRICT=1, never with HATAMOTO_PERF_STRICT=0, else only off CI.
func perfStrict() bool {
	switch os.Getenv("HATAMOTO_PERF_STRICT") {
	case "1":
		return true
	case "0":
		return false
	}
	return os.Getenv("CI") == ""
}
