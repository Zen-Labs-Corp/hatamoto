package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

const performancePath = "/settings/performance"

// performanceData is the Performance settings page: the saved (or, after a
// failed save, submitted) form state next to the live cache numbers.
type performanceData struct {
	Summary []ui.FieldError
	Errs    map[string]string

	Enabled, PreloadEnabled     bool
	AfterChanges                bool
	TTLOptions, IntervalOptions []ui.Option
	MaxMB                       string
	TTLSource                   string
	LastPreload, StoppedLine    string
	Running                     string
	Stats                       []ui.Stat
	CacheUse, PreloadProgress   *ui.Progress // nil without a size limit / while no preload runs
	FailedLine                  string
	ModeOptions                 []ui.Option
	NoBaseURL, NoScheduler      bool
}

func (a *Admin) mountPerformanceSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/performance", a.handlePerformance)
	owner.HandleFunc("POST /settings/performance", a.handlePerformanceSave)
	owner.HandleFunc("POST /settings/performance/clear", a.handlePerformanceClear)
	owner.HandleFunc("POST /settings/performance/preload", a.handlePerformancePreload)
}

func (a *Admin) handlePerformance(w http.ResponseWriter, r *http.Request) {
	if a.deps.PageCache == nil {
		a.unavailable(w, r, "Performance settings")
		return
	}
	stored, err := a.deps.PageCache.Settings(r.Context())
	if err != nil {
		a.logger().Error("page cache settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The performance settings could not be loaded.")
		return
	}
	a.renderPerformance(w, r, http.StatusOK, stored, strconv.Itoa(stored.MaxMB), nil)
}

func (a *Admin) handlePerformanceSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.PageCache
	if svc == nil {
		a.unavailable(w, r, "Performance settings")
		return
	}
	ctx := r.Context()
	stored, err := svc.Settings(ctx)
	if err != nil {
		a.logger().Error("page cache settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The performance settings could not be loaded.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails validation
	next, errs := validatePerformance(r.PostForm, stored)
	// Posting the lifetime the page pre-selected (the server default) must not
	// turn "server default" into an admin override.
	if stored.TTLSeconds == 0 && next.TTLSeconds == int(svc.Stats(ctx).ServerDefaultTTL/time.Second) {
		next.TTLSeconds = 0
	}
	if len(errs) > 0 {
		maxMB := strconv.Itoa(next.MaxMB)
		if next.Enabled && r.PostForm.Has("max_mb") {
			maxMB = strings.TrimSpace(r.PostFormValue("max_mb"))
		}
		a.renderPerformance(w, r, http.StatusUnprocessableEntity, next, maxMB, errs)
		return
	}
	if err := svc.Save(ctx, next); err != nil {
		a.logger().Error("page cache save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The performance settings could not be saved.")
		return
	}
	a.audit(r, "cache.settings", "page-cache", map[string]any{"old": settingsDetail(stored), "new": settingsDetail(next)})
	a.SetFlash(w, r, "success", "Performance settings saved.")
	http.Redirect(w, r, a.adminPath+performancePath, http.StatusSeeOther)
}

func (a *Admin) handlePerformanceClear(w http.ResponseWriter, r *http.Request) {
	if a.deps.PageCache == nil {
		a.unavailable(w, r, "Performance settings")
		return
	}
	pages, err := a.deps.PageCache.Clear(r.Context())
	if err != nil {
		a.logger().Error("page cache clear", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The cache could not be cleared.")
		return
	}
	a.audit(r, "cache.clear", "page-cache", map[string]any{"pages": pages})
	a.SetFlash(w, r, "success", "Cleared "+countNoun(pages, "page")+".")
	http.Redirect(w, r, a.adminPath+performancePath, http.StatusSeeOther)
}

func (a *Admin) handlePerformancePreload(w http.ResponseWriter, r *http.Request) {
	if a.deps.PageCache == nil {
		a.unavailable(w, r, "Performance settings")
		return
	}
	started, err := a.deps.PageCache.PreloadNow(r.Context())
	switch {
	case err != nil:
		a.SetFlash(w, r, "error", "Preload did not start: "+fullStop(err.Error()))
	case started:
		a.audit(r, "cache.preload", "page-cache", nil)
		a.SetFlash(w, r, "success", "Preload started.")
	default:
		a.SetFlash(w, r, "info", "A preload is already running.")
	}
	http.Redirect(w, r, a.adminPath+performancePath, http.StatusSeeOther)
}

func (a *Admin) renderPerformance(w http.ResponseWriter, r *http.Request, status int, s PageCacheSettings, maxMB string, errs []ui.FieldError) {
	ctx := r.Context()
	stats := a.deps.PageCache.Stats(ctx)
	pre := a.deps.PageCache.PreloadStatus()
	loc := a.siteLocation(ctx)

	ttl := time.Duration(s.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = stats.EffectiveTTL
	}
	interval := time.Duration(s.PreloadIntervalSeconds) * time.Second
	if interval == 0 {
		interval = defaultInterval(ttl)
	}

	d := performanceData{
		Summary: errs, Errs: map[string]string{},
		Enabled: s.Enabled, PreloadEnabled: s.PreloadEnabled,
		ModeOptions: []ui.Option{
			{Value: siteconfig.PreloadModeMissing, Label: "Only missing", Selected: s.PreloadMode != siteconfig.PreloadModeAll},
			{Value: siteconfig.PreloadModeAll, Label: "Refresh all", Selected: s.PreloadMode == siteconfig.PreloadModeAll},
		},
		AfterChanges:    s.PreloadAfterChanges,
		TTLOptions:      durationOptions(ttlPresets, ttl),
		IntervalOptions: durationOptions(intervalPresets, interval),
		MaxMB:           maxMBText(maxMB),
		TTLSource:       ttlSourceLine(stats),
		NoBaseURL:       !stats.BaseURLPinned,
		NoScheduler:     !stats.SchedulerAvailable,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	switch {
	case pre.Running:
		d.Running = fmt.Sprintf("Preloading: %d of %d pages", pre.Done, pre.Total)
		d.PreloadProgress = &ui.Progress{Label: "Preload progress", Value: fmt.Sprintf("%d of %d pages", pre.Done, pre.Total)}
		if pre.Total > 0 {
			d.PreloadProgress.Pct = float64(pre.Done) / float64(pre.Total) * 100
		}
	case pre.HasResult:
		parts := []string{countNoun(pre.Pages, "page")}
		if pre.Skipped > 0 {
			parts = append(parts, thousands(pre.Skipped)+" already cached")
		}
		parts = append(parts, countNoun(pre.Errors, "error"), shortDuration(pre.Duration))
		d.LastPreload = strings.Join(parts, " · ")
		d.FailedLine = failedLine(pre)
		if pre.StoppedEarly {
			d.StoppedLine = "(stopped early: " + pre.Reason + ")"
		}
	}

	d.CacheUse = cacheUse(stats)
	d.Stats = performanceStats(stats, pre, loc)

	v := a.NewView(w, r, "Performance")
	v.Section, v.Tab = "performance", "performance"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/performance", v)
}

func settingsDetail(s PageCacheSettings) map[string]any {
	return map[string]any{
		"enabled": s.Enabled, "ttl_seconds": s.TTLSeconds, "max_mb": s.MaxMB,
		"preload_enabled": s.PreloadEnabled, "preload_interval_seconds": s.PreloadIntervalSeconds,
		"preload_mode": s.PreloadMode, "preload_after_changes": s.PreloadAfterChanges,
	}
}

// maxMBText shows 0 (no limit) as an empty field.
func maxMBText(v string) string {
	if v == "0" {
		return ""
	}
	return v
}

// durationOptions lists the presets with the one equal to current selected;
// a current value that is not a preset is added so saving never changes it.
func durationOptions(presets []time.Duration, current time.Duration) []ui.Option {
	var opts []ui.Option
	found := false
	for _, p := range presets {
		if p == current {
			found = true
		}
	}
	if !found && current > 0 {
		opts = append(opts, ui.Option{Value: strconv.Itoa(int(current / time.Second)), Label: cacheDuration(current), Selected: true})
	}
	for _, p := range presets {
		opts = append(opts, ui.Option{Value: strconv.Itoa(int(p / time.Second)), Label: cacheDuration(p), Selected: p == current})
	}
	return opts
}

// defaultInterval is the preload interval shown while none is stored: the
// smallest preset that is not shorter than the page lifetime.
func defaultInterval(ttl time.Duration) time.Duration {
	for _, p := range intervalPresets {
		if p >= ttl {
			return p
		}
	}
	return intervalPresets[len(intervalPresets)-1]
}

// maxFailedShown is how many failed pages the page names before "and N more".
const maxFailedShown = 10

// failedLine names the pages the last preload could not build, or "".
func failedLine(pre PreloadStatus) string {
	if len(pre.Failures) == 0 {
		return ""
	}
	shown := pre.Failures[:min(len(pre.Failures), maxFailedShown)]
	items := make([]string, len(shown))
	for i, f := range shown {
		items[i] = f.Path + " (" + f.Reason + ")"
	}
	line := "Failed: " + strings.Join(items, " · ")
	if more := pre.Errors - len(shown); more > 0 {
		line += " and " + thousands(more) + " more"
	}
	return line
}

func cacheDuration(d time.Duration) string {
	switch {
	case d >= 5*cacheDay && d%cacheDay == 0:
		return countNoun(int(d/cacheDay), "day")
	case d%time.Hour == 0:
		return countNoun(int(d/time.Hour), "hour")
	case d%time.Minute == 0:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	default:
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
}

func shortDuration(d time.Duration) string {
	if d < 10*time.Minute {
		return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
	}
	return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return thousands(n) + " " + noun + "s"
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func megabytes(b int64) string { return fmt.Sprintf("%d MB", (b+(1<<19))>>20) }

// cacheSize is the size of the cached pages as a short phrase.
func cacheSize(s PageCacheStats) string {
	if s.Bytes > 0 && s.Bytes < 1<<20 {
		return "under 1 MB"
	}
	return "~" + megabytes(s.Bytes)
}

// cacheUse is the gauge of the cache against its size limit; nil without a
// limit, so nothing divides by zero.
func cacheUse(s PageCacheStats) *ui.Progress {
	if s.CapBytes <= 0 {
		return nil
	}
	p := &ui.Progress{Label: "Cache use", Value: cacheSize(s) + " of " + megabytes(s.CapBytes), Pct: float64(s.Bytes) / float64(s.CapBytes) * 100, Kind: "ink", Thin: true}
	switch {
	case p.Pct >= 100:
		p.Kind = "danger"
	case p.Pct >= 85:
		p.Kind = "warning"
	}
	return p
}

// performanceStats is the strip above the form: pages, size and last preload.
func performanceStats(s PageCacheStats, pre PreloadStatus, loc *time.Location) []ui.Stat {
	last := "Never"
	switch {
	case pre.Running:
		last = "Running now"
	case pre.HasResult:
		last = fmtDateTime(pre.Finished.In(loc))
	}
	return []ui.Stat{
		{Label: "Pages cached", Value: thousands(s.Pages)},
		{Label: "Cache size", Value: cacheSize(s)},
		{Label: "Last preload", Value: last, Small: true},
	}
}

func ttlSourceLine(s PageCacheStats) string {
	def := cacheDuration(s.ServerDefaultTTL)
	if s.TTLSource == "admin" {
		return "set here (server default: " + def + ")"
	}
	origin := "built-in"
	if s.ServerDefaultFromEnv {
		origin = "environment"
	}
	return "server default: " + def + " (" + origin + ")"
}
