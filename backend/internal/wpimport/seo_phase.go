package wpimport

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/core/internal/seo"
)

const (
	seoPhaseTimeout = 60 * time.Second
	seoLiveTimeout  = 10 * time.Second
	// seoLiveWarnings caps how many failed live reads are listed one by one.
	seoLiveWarnings = 3
)

// importSEO maps the source site's global SEO settings onto Hatamoto's. On a
// site with content nothing is written: the rows go to SEOResult.Would. The
// phase never fails the import: every problem is a warning.
func (r *Runner) importSEO(ctx context.Context) error {
	if r.opts.SEOFile == nil && r.opts.NoSEOLive {
		return nil
	}
	res := &SEOResult{}
	r.res.SEO = res

	var live *seoLive
	if !r.opts.NoSEOLive {
		ctx, cancel := context.WithTimeout(ctx, seoPhaseTimeout)
		defer cancel()
		live = r.readSEOLive(ctx)
	}
	if live != nil {
		res.Sources = append(res.Sources, "live")
	}
	if f := r.opts.SEOFile; f != nil {
		res.Sources = append(res.Sources, "file:"+f.Plugin)
	}

	plan := buildSEOPlan(r.opts.SEOFile, live, r.slugOf, r.dump.Detect.SEOPlugin)
	res.Plugin = plan.Plugin
	res.NotMapped = append(res.NotMapped, plan.NotMapped...)

	if len(plan.Fields) > 0 {
		r.applySEOPlan(ctx, res, plan)
	}
	r.seoExtras(ctx, r.opts.SEOFile, live, res)
	if len(res.Applied)+len(res.Would)+len(res.NotMapped) == 0 {
		r.res.SEO = nil
	}
	return nil
}

// applySEOPlan validates the plan against the current settings and writes it
// (empty site) or lists it (existing site).
func (r *Runner) applySEOPlan(ctx context.Context, res *SEOResult, plan seoPlan) {
	cur, err := r.d.SEO.Get(ctx)
	if err != nil {
		r.warn("SEO settings not imported: " + err.Error())
		return
	}
	kept, dropped := pruneSEOPlan(cur, plan)
	res.NotMapped = append(res.NotMapped, dropped...)
	if r.state.Mode == ModeExisting {
		_, res.Would = seoRows(cur, kept)
		return
	}
	if err := r.d.SEO.Merge(ctx, func(s *seo.Settings) {
		for _, f := range kept {
			f.Apply(s)
		}
	}); err != nil {
		r.warn("SEO settings not imported: " + err.Error())
		return
	}
	res.Applied, _ = seoRows(cur, kept)
}

// seoRows describes what kept would do to cur: applied has every field, would
// only those whose value differs from the current one.
func seoRows(cur seo.Settings, kept []seoField) (applied, would []SEORow) {
	next := cur.Clone()
	for _, f := range kept {
		f.Apply(&next)
	}
	for _, f := range kept {
		was, now := f.Show(cur), f.Show(next)
		applied = append(applied, SEORow{Key: f.Key, Label: f.Label, Value: now})
		if was != now {
			would = append(would, SEORow{Key: f.Key, Label: f.Label, Current: was, Value: now})
		}
	}
	return applied, would
}

// slugOf resolves a WordPress post type to its Hatamoto schema slug.
func (r *Runner) slugOf(wpType string) (string, bool) {
	slug, ok := r.schemaOf[wpType]
	return slug, ok
}

func (r *Runner) warn(msg string) { r.res.Warnings = append(r.res.Warnings, msg) }

// readSEOLive reads the source site's public pages with a dedicated client.
// AllowPrivate comes from the run's options only; the URLs are the site's own.
func (r *Runner) readSEOLive(ctx context.Context) *seoLive {
	c, err := NewClient(ClientOptions{URL: r.client.Base.String(), AllowPrivate: r.opts.AllowPrivate,
		MaxRetries: 1, Timeout: seoLiveTimeout, MaxTotalBytes: 8 << 20})
	if err != nil {
		r.warn("SEO live read skipped: " + err.Error())
		return nil
	}
	postURL, archiveURL := r.sampleURLs()
	live := readSEOLive(ctx, c, c.Base.String()+"/", postURL, archiveURL)
	for i, w := range live.Warnings {
		if i == seoLiveWarnings {
			r.warn(fmt.Sprintf("SEO live read: %d more reads failed", len(live.Warnings)-i))
			break
		}
		r.warn(w)
	}
	return live
}

// sampleURLs picks one published post and one term archive of the source site.
func (r *Runner) sampleURLs() (post, archive string) {
	onSite := func(link string) bool {
		u, err := url.Parse(link)
		return err == nil && r.hosts[strings.ToLower(u.Hostname())]
	}
	for _, it := range r.dump.Items {
		if it.Type == "post" && it.Status == wpPublish && !it.Protected && onSite(it.Link) {
			post = it.Link
			break
		}
	}
	for _, t := range r.dump.Terms {
		if t.Link != "" && onSite(t.Link) {
			archive = t.Link
			break
		}
	}
	return post, archive
}
