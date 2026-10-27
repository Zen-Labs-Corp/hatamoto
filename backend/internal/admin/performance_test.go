package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakePageCache is a PageCacheService with fixed numbers and call records.
type fakePageCache struct {
	settings  PageCacheSettings
	stats     PageCacheStats
	preload   PreloadStatus
	saved     []PageCacheSettings
	clears    int
	clearN    int
	started   bool
	startErr  error
	preloads  int
	saveError error
}

func (f *fakePageCache) Stats(context.Context) PageCacheStats { return f.stats }
func (f *fakePageCache) Settings(context.Context) (PageCacheSettings, error) {
	return f.settings, nil
}
func (f *fakePageCache) Save(_ context.Context, s PageCacheSettings) error {
	if f.saveError != nil {
		return f.saveError
	}
	f.saved = append(f.saved, s)
	f.settings = s
	return nil
}
func (f *fakePageCache) Clear(context.Context) (int, error) { f.clears++; return f.clearN, nil }
func (f *fakePageCache) PreloadNow(context.Context) (bool, error) {
	f.preloads++
	return f.started, f.startErr
}
func (f *fakePageCache) PreloadStatus() PreloadStatus { return f.preload }

func newPerfFake() *fakePageCache {
	return &fakePageCache{
		settings: PageCacheSettings{Enabled: true, TTLSeconds: 3600, MaxMB: 300, PreloadEnabled: true, PreloadIntervalSeconds: 3600, PreloadMode: "missing", PreloadAfterChanges: true},
		stats: PageCacheStats{
			Pages: 1144, Bytes: 180 << 20, CapBytes: 300 << 20,
			EffectiveTTL: time.Hour, ServerDefaultTTL: 5 * time.Minute, TTLSource: "admin",
			BaseURLPinned: true, SchedulerAvailable: true,
		},
		preload: PreloadStatus{
			HasResult: true, Finished: time.Date(2026, 10, 16, 17, 38, 0, 0, time.UTC),
			Pages: 1144, Errors: 0, Duration: 64 * time.Second, StoppedEarly: true, Reason: "size limit reached",
		},
		clearN: 1144, started: true,
	}
}

type perfRig struct {
	*ownerRig
	svc    *fakePageCache
	events []AuditEvent
}

func newPerfRig(t *testing.T, svc *fakePageCache) *perfRig {
	t.Helper()
	g := newOwnerRig(t, true)
	g.a.deps.Site.(*fakeSite).cfg.Timezone = "Europe/Paris"
	p := &perfRig{ownerRig: g, svc: svc}
	if svc != nil {
		g.a.deps.PageCache = svc
	}
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { p.events = append(p.events, e) }
	return p
}

// followFlash loads the settings page with the flash cookie a redirect set.
func (p *perfRig) followFlash(cookies []*http.Cookie) string {
	req := httptestReq("GET", "/admin/settings/performance", "owner-token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return serve(p.h, req, nil).Body.String()
}

func (p *perfRig) get(t *testing.T) string {
	t.Helper()
	rec := p.do("GET", "/admin/settings/performance", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestPerformancePage(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	body := p.get(t)
	for _, want := range []string{"~180 MB of 300 MB", "Pages cached", "1,144", "Cache use", "set here (server default: 5 min)", "1,144 pages · 0 errors · 64 s", "(stopped early: size limit reached)", "Edits made here clear the cache at once; changes made elsewhere", "Pages to build", "After content changes", "Only missing skips pages that are already cached, so it does not refresh them."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, bad := range []string{"PUBLIC_BASE_URL", "Background jobs are off", " disabled"} {
		if strings.Contains(body, bad) {
			t.Errorf("page has %q", bad)
		}
	}
	if !strings.Contains(body, `id="max-mb-unit">MB<`) || !regexp.MustCompile(`id="max-mb"[^>]*aria-describedby="[^"]*\bmax-mb-unit\b`).MatchString(body) {
		t.Error("max-mb input is not described by the MB unit element")
	}
	compareGolden(t, "pages/settings-performance.html", normalizePage(body))
}

func TestPerformancePageVariants(t *testing.T) {
	t.Parallel()

	t.Run("no cap and server default from environment", func(t *testing.T) {
		f := newPerfFake()
		f.settings.MaxMB, f.settings.TTLSeconds = 0, 0
		f.stats.CapBytes, f.stats.TTLSource, f.stats.ServerDefaultFromEnv = 0, "server", true
		f.stats.EffectiveTTL = 10 * time.Minute
		f.stats.ServerDefaultTTL = 10 * time.Minute
		f.preload.StoppedEarly = false
		body := newPerfRig(t, f).get(t)
		for _, want := range []string{"~180 MB<", "server default: 10 min (environment)"} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %q", want)
			}
		}
		if strings.Contains(body, " of 300 MB") || strings.Contains(body, "stopped early") {
			t.Error("cap or stopped-early line shown without a cap")
		}
		if strings.Contains(body, "Cache use") || strings.Contains(body, "hm-meter") {
			t.Error("progress bar shown without a size limit")
		}
	})
	t.Run("off greys the size and preload controls", func(t *testing.T) {
		f := newPerfFake()
		f.settings.Enabled = false
		f.stats.Pages, f.stats.Bytes = 0, 0
		body := newPerfRig(t, f).get(t)
		if n := strings.Count(body, " disabled"); n < 5 {
			t.Errorf("disabled controls = %d, want size, preload switch and its off sentinel, interval and preload now", n)
		}
		if !strings.Contains(body, ">Pages cached<") || !strings.Contains(body, `Pages cached</dt><dd class="hm-statcard__value">0</dd>`) {
			t.Error("Off should read 0 pages cached")
		}
	})
	t.Run("unpinned base URL and no scheduler notes", func(t *testing.T) {
		f := newPerfFake()
		f.stats.BaseURLPinned, f.stats.SchedulerAvailable = false, false
		body := newPerfRig(t, f).get(t)
		for _, want := range []string{"PUBLIC_BASE_URL", "Background jobs are off"} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %q", want)
			}
		}
	})
	t.Run("cancelled preload", func(t *testing.T) {
		f := newPerfFake()
		f.preload.Reason = "cancelled"
		if body := newPerfRig(t, f).get(t); !strings.Contains(body, "(stopped early: cancelled)") {
			t.Error("cancelled reason missing")
		}
	})
	t.Run("running preload", func(t *testing.T) {
		f := newPerfFake()
		f.preload = PreloadStatus{Running: true, Done: 12, Total: 40}
		if body := newPerfRig(t, f).get(t); !strings.Contains(body, "Preloading: 12 of 40 pages") {
			t.Error("progress missing")
		} else if !strings.Contains(body, "12 of 40 pages</span>") || !strings.Contains(body, "Running now") {
			t.Error("preload gauge or stat missing")
		}
	})
	t.Run("non preset lifetime stays selected", func(t *testing.T) {
		f := newPerfFake()
		f.settings.TTLSeconds = 90
		if body := newPerfRig(t, f).get(t); !strings.Contains(body, `<option value="90" selected>90 s</option>`) {
			t.Error("extra option missing")
		}
	})
}

func TestPerformanceOwnerOnlyAndCSRF(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	if rec := p.do("GET", "/admin/settings/performance", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	for _, path := range []string{"", "/clear", "/preload"} {
		target := "/admin/settings/performance" + path
		if rec := p.do("POST", target, "editor-token", url.Values{}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d", target, rec.Code)
		}
		if rec := p.do("POST", target, "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != 403 {
			t.Errorf("no CSRF POST %s = %d", target, rec.Code)
		}
	}
	if p.svc.clears != 0 || p.svc.preloads != 0 || len(p.svc.saved) != 0 || len(p.events) != 0 {
		t.Error("a refused request reached the service")
	}
}

func TestPerformanceSave(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	good := url.Values{"enabled": {"on"}, "ttl": {"7200"}, "max_mb": {"500"}, "preload": {"off"}, "interval": {"3600"}}
	rec := p.do("POST", "/admin/settings/performance", "owner-token", good, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/performance" {
		t.Fatalf("save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(p.svc.saved) != 1 || p.svc.saved[0] != (PageCacheSettings{Enabled: true, TTLSeconds: 7200, MaxMB: 500, PreloadEnabled: false, PreloadIntervalSeconds: 3600, PreloadMode: "missing", PreloadAfterChanges: true}) {
		t.Fatalf("saved = %+v", p.svc.saved)
	}
	if len(p.events) != 1 || p.events[0].Action != "cache.settings" || p.events[0].Actor == "" || p.events[0].Detail["new"] == nil {
		t.Fatalf("audit = %+v", p.events)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Error("no flash cookie")
	}
}

func TestPerformanceSaveOffKeepsStored(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	rec := p.do("POST", "/admin/settings/performance", "owner-token", url.Values{"enabled": {"off"}, "ttl": {"3600"}}, nil)
	if rec.Code != 303 || len(p.svc.saved) != 1 {
		t.Fatalf("save = %d %+v", rec.Code, p.svc.saved)
	}
	if got := p.svc.saved[0]; got.Enabled || got.MaxMB != 300 || !got.PreloadEnabled || got.PreloadIntervalSeconds != 3600 {
		t.Fatalf("saved = %+v", got)
	}
}

func TestPerformanceSaveOffToOnKeepsStored(t *testing.T) {
	t.Parallel()

	f := newPerfFake()
	f.settings = PageCacheSettings{Enabled: false, TTLSeconds: 3600, MaxMB: 500, PreloadEnabled: true, PreloadIntervalSeconds: 3600, PreloadMode: "all", PreloadAfterChanges: true}
	p := newPerfRig(t, f)
	rec := p.do("POST", "/admin/settings/performance", "owner-token", url.Values{"enabled": {"on"}, "ttl": {"3600"}}, nil)
	if rec.Code != 303 || len(p.svc.saved) != 1 {
		t.Fatalf("save = %d %+v", rec.Code, p.svc.saved)
	}
	want := PageCacheSettings{Enabled: true, TTLSeconds: 3600, MaxMB: 500, PreloadEnabled: true, PreloadIntervalSeconds: 3600, PreloadMode: "all", PreloadAfterChanges: true}
	if got := p.svc.saved[0]; got != want {
		t.Fatalf("saved = %+v, want %+v", got, want)
	}
}

func TestPerformanceSaveInvalid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		form url.Values
		want string
	}{
		{"short lifetime", url.Values{"enabled": {"on"}, "ttl": {"10"}, "preload": {"off"}}, "Choose a time from 30 seconds to 30 days."},
		{"long lifetime", url.Values{"enabled": {"on"}, "ttl": {"2678400"}, "preload": {"off"}}, "Choose a time from 30 seconds to 30 days."},
		{"bad size", url.Values{"enabled": {"on"}, "ttl": {"3600"}, "max_mb": {"lots"}, "preload": {"off"}}, "Enter a whole number of MB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPerfRig(t, newPerfFake())
			rec := p.do("POST", "/admin/settings/performance", "owner-token", tc.form, nil)
			body := rec.Body.String()
			if rec.Code != 422 || !strings.Contains(body, tc.want) || !strings.Contains(body, "There is a problem with this form") {
				t.Fatalf("code %d, body lacks %q", rec.Code, tc.want)
			}
			if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
				t.Errorf("a11y: %v", issues)
			}
			if len(p.svc.saved) != 0 || len(p.events) != 0 {
				t.Error("an invalid save reached the service")
			}
		})
	}
}

func TestPerformanceSaveStoreError(t *testing.T) {
	t.Parallel()

	f := newPerfFake()
	f.saveError = errors.New("db down")
	p := newPerfRig(t, f)
	form := url.Values{"enabled": {"on"}, "ttl": {"3600"}, "preload": {"off"}}
	if rec := p.do("POST", "/admin/settings/performance", "owner-token", form, nil); rec.Code != 500 || len(p.events) != 0 {
		t.Errorf("code %d events %d", rec.Code, len(p.events))
	}
}

func TestPerformanceClear(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	rec := p.do("POST", "/admin/settings/performance/clear", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || p.svc.clears != 1 {
		t.Fatalf("clear = %d clears %d", rec.Code, p.svc.clears)
	}
	if len(p.events) != 1 || p.events[0].Action != "cache.clear" || p.events[0].Detail["pages"] != 1144 {
		t.Fatalf("audit = %+v", p.events)
	}
}

func TestPerformancePreload(t *testing.T) {
	t.Parallel()

	f := newPerfFake()
	p := newPerfRig(t, f)
	rec := p.do("POST", "/admin/settings/performance/preload", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || f.preloads != 1 || len(p.events) != 1 || p.events[0].Action != "cache.preload" {
		t.Fatalf("started: %d %d %+v", rec.Code, f.preloads, p.events)
	}
	if got := p.followFlash(rec.Result().Cookies()); !strings.Contains(got, "Preload started.") {
		t.Errorf("flash = %q", got)
	}

	f.started = false
	rec = p.do("POST", "/admin/settings/performance/preload", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || len(p.events) != 1 {
		t.Fatalf("running: %d events %d", rec.Code, len(p.events))
	}
	if got := p.followFlash(rec.Result().Cookies()); !strings.Contains(got, "A preload is already running.") || !strings.Contains(got, "hm-flash--info") {
		t.Errorf("flash = %q", got)
	}

	f.startErr = errors.New("PUBLIC_BASE_URL is not set")
	rec = p.do("POST", "/admin/settings/performance/preload", "owner-token", url.Values{}, nil)
	if got := p.followFlash(rec.Result().Cookies()); !strings.Contains(got, "PUBLIC_BASE_URL is not set") || !strings.Contains(got, "hm-flash--error") {
		t.Errorf("flash = %q", got)
	}
}

func TestPerformanceUnavailable(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, nil)
	if rec := p.do("GET", "/admin/settings/performance", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("GET = %d", rec.Code)
	}
	for _, path := range []string{"", "/clear", "/preload"} {
		if rec := p.do("POST", "/admin/settings/performance"+path, "owner-token", url.Values{}, nil); rec.Code != 503 {
			t.Errorf("POST %q = %d", path, rec.Code)
		}
	}
}

func TestPerformanceSaveKeepsServerDefaultLifetime(t *testing.T) {
	t.Parallel()

	f := newPerfFake()
	f.settings = PageCacheSettings{Enabled: true}
	f.stats.TTLSource = "default"
	p := newPerfRig(t, f)
	rec := p.do("POST", "/admin/settings/performance", "owner-token",
		url.Values{"enabled": {"on"}, "ttl": {"300"}, "preload": {"on"}, "interval": {"900"}}, nil)
	if rec.Code != 303 || len(f.saved) != 1 {
		t.Fatalf("save = %d %+v", rec.Code, f.saved)
	}
	if got := f.saved[0]; got.TTLSeconds != 0 || !got.PreloadEnabled || got.PreloadIntervalSeconds != 900 {
		t.Fatalf("saved = %+v, want ttl 0 kept", got)
	}

	rec = p.do("POST", "/admin/settings/performance", "owner-token",
		url.Values{"enabled": {"on"}, "ttl": {"3600"}, "preload": {"on"}, "interval": {"900"}}, nil)
	if rec.Code != 303 || len(f.saved) != 2 || f.saved[1].TTLSeconds != 3600 {
		t.Fatalf("different lifetime: %d %+v", rec.Code, f.saved)
	}
}

func TestPerformanceErrorLinksTargetRadioGroups(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	rec := p.do("POST", "/admin/settings/performance", "owner-token",
		url.Values{"enabled": {"maybe"}, "ttl": {"3600"}, "preload": {"maybe"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 {
		t.Fatalf("save = %d", rec.Code)
	}
	for _, id := range []string{"page-cache", "preload"} {
		if !strings.Contains(body, `href="#`+id+`"`) || !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("error link #%s has no target element", id)
		}
	}
}

func TestPerformanceSaveModeAndAfterChanges(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	form := url.Values{"enabled": {"on"}, "ttl": {"2592000"}, "preload": {"on"}, "interval": {"2592000"}, "mode": {"all"}, "after_changes": {"off"}}
	if rec := p.do("POST", "/admin/settings/performance", "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	got := p.svc.saved[0]
	if got.TTLSeconds != 2592000 || got.PreloadIntervalSeconds != 2592000 || got.PreloadMode != "all" || got.PreloadAfterChanges {
		t.Fatalf("saved = %+v", got)
	}
}

func TestPerformanceSaveRejectsBadModeAndTooLongLifetime(t *testing.T) {
	t.Parallel()

	p := newPerfRig(t, newPerfFake())
	for name, form := range map[string]url.Values{
		"mode":          {"enabled": {"on"}, "ttl": {"3600"}, "mode": {"some"}},
		"after changes": {"enabled": {"on"}, "ttl": {"3600"}, "after_changes": {"maybe"}},
		"31 days":       {"enabled": {"on"}, "ttl": {"2678400"}},
	} {
		if rec := p.do("POST", "/admin/settings/performance", "owner-token", form, nil); rec.Code != 422 {
			t.Errorf("%s: save = %d, want 422", name, rec.Code)
		}
	}
	if len(p.svc.saved) != 0 {
		t.Fatalf("invalid input was saved: %+v", p.svc.saved)
	}
}

func TestPerformancePageNamesFailuresAndSkipped(t *testing.T) {
	t.Parallel()

	f := newPerfFake()
	f.preload = PreloadStatus{
		HasResult: true, Finished: time.Date(2026, 10, 16, 17, 38, 0, 0, time.UTC),
		Pages: 1140, Skipped: 4, Errors: 2, Duration: 64 * time.Second,
		Failures: []PreloadFailure{{"/news/x", "500"}, {"/blog/y", "panic"}},
	}
	body := newPerfRig(t, f).get(t)
	if !strings.Contains(body, "hm-callout--warning") {
		t.Error("failed pages are not a warning callout")
	}
	for _, want := range []string{"1,140 pages · 4 already cached · 2 errors · 64 s", "Failed: /news/x (500) · /blog/y (panic)"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	f.preload.Skipped, f.preload.Errors = 0, 25
	f.preload.Failures = nil
	for i := range 20 {
		f.preload.Failures = append(f.preload.Failures, PreloadFailure{fmt.Sprintf("/p%d", i), "500"})
	}
	body = newPerfRig(t, f).get(t)
	if !strings.Contains(body, "/p9 (500) and 15 more") || strings.Contains(body, "/p10 (500)") || strings.Contains(body, "already cached ·") {
		t.Errorf("failure list not capped at 10 / skipped shown at 0: %s", body)
	}
}

func TestPerformanceStatsAndGauge(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	st := PageCacheStats{Pages: 3, Bytes: 100}
	if cacheUse(st) != nil {
		t.Error("no limit must give no gauge")
	}
	if got := performanceStats(st, PreloadStatus{}, loc); got[1].Value != "under 1 MB" || got[2].Value != "Never" {
		t.Errorf("empty stats %+v", got)
	}
	done := PreloadStatus{HasResult: true, Finished: time.Date(2026, 10, 12, 19, 38, 0, 0, time.UTC)}
	if got := performanceStats(st, done, loc); got[2].Value != "12 Oct 2026 19:38" {
		t.Errorf("an old preload must show its date, got %q", got[2].Value)
	}
	st.Bytes, st.CapBytes = 90<<20, 100<<20
	if p := cacheUse(st); p == nil || p.Kind != "warning" || p.Pct != 90 {
		t.Errorf("gauge %+v", p)
	}
	st.Bytes = 100 << 20
	if p := cacheUse(st); p.Kind != "danger" {
		t.Errorf("full gauge %+v", p)
	}
}
