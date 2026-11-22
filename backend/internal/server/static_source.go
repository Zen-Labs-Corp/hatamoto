package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/middleware"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/staticexport"
	"hatamoto.dev/core/internal/statichost"
)

// staticSource lets the static exporter render this server's public site. It
// fetches every path through the real router as an anonymous visitor.
// Site must be called before Fetch (Export does).
type staticSource struct {
	s    *Server
	base *url.URL
}

// siteSettings reads the site config; a missing row is not an error.
func (src *staticSource) siteSettings(ctx context.Context) (*siteconfig.Config, error) {
	if src.s.siteConfig == nil {
		return nil, nil
	}
	c, err := src.s.siteConfig.Get(ctx)
	if errors.Is(err, siteconfig.ErrNotConfigured) {
		return nil, nil
	}
	return c, err
}

// Site refuses demo installs, a site behind the access gate and a site with
// no address; otherwise it returns what the crawl needs to know.
func (src *staticSource) Site(ctx context.Context) (staticexport.Site, error) {
	s := src.s
	if s.demoOn() {
		return staticexport.Site{}, fmt.Errorf("%w: demo mode is on", staticexport.ErrRefused)
	}
	if s.siteAccessSettings(ctx).On() {
		return staticexport.Site{}, staticexport.ErrSiteAccessOn
	}
	conf, err := src.siteSettings(ctx)
	if err != nil {
		return staticexport.Site{}, fmt.Errorf("%w: cannot read the site settings", staticexport.ErrRefused)
	}
	base := ""
	if s.cfg != nil {
		base = strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/")
	}
	if base == "" && conf != nil {
		base = strings.TrimRight(strings.TrimSpace(conf.PublicBaseURL), "/")
	}
	if base == "" {
		return staticexport.Site{}, fmt.Errorf("%w: set the site URL in Site settings", staticexport.ErrRefused)
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
		return staticexport.Site{}, fmt.Errorf("%w: the site URL %q is not an origin", staticexport.ErrRefused, base)
	}
	src.base = u
	site := staticexport.Site{
		Base:              u,
		AdminPrefix:       s.adminPathValue(),
		LegacyWPRedirects: conf != nil && conf.LegacyWPRedirects,
	}
	if why := statichost.HostingFrom(ctx, s.hosting.Load()).InactiveReason(); why != "" {
		site.Warnings = []string{"static mode is on but islands stay on this site's own paths: " + why}
	}
	return site, nil
}

// Seeds lists the sitemap set (as the preloader does, but with the resolved
// site URL) and the fixed root files.
func (src *staticSource) Seeds(ctx context.Context) ([]string, error) {
	s := src.s
	if src.base == nil {
		return nil, errors.New("static export: Site was not called")
	}
	base := src.base.String()
	in, err := s.loadSitemapInputFor(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("load sitemap: %w", err)
	}
	plan := s.planSitemapLogged(in)
	var locs []string
	for _, g := range plan.groups {
		for _, u := range s.sitemapFileURLs(ctx, plan, g.key) {
			locs = append(locs, u.Loc)
		}
	}
	paths := preloadPathsFromLocs(base, locs)
	paths = append(paths, "/", "/robots.txt", "/sitemap.xml", "/sitemap.xsl", "/llms.txt",
		"/feed.xml", "/atom.xml", "/ads.txt", "/app-ads.txt")
	for _, icon := range render.RootIcons {
		paths = append(paths, "/"+icon)
	}
	if set := s.seoSettings(ctx); set != nil && set.IndexNow.Enabled && set.IndexNow.Key != "" {
		paths = append(paths, "/"+set.IndexNow.Key+".txt")
	}
	return paths, nil
}

// Fetch does a GET through the router and streams the body into body.
func (src *staticSource) Fetch(ctx context.Context, path string, body io.Writer) (resp staticexport.Response, err error) {
	if src.base == nil {
		return staticexport.Response{}, errors.New("static export: Site was not called")
	}
	// Export requests keep S-relative links and never get the live 301.
	ctx = statichost.WithExport(middleware.WithoutRequestLog(redirect.WithoutCounting(ctx)))
	w := &streamWriter{h: http.Header{}, body: body, status: http.StatusOK}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("render panic: %v", r)
		}
	}()
	// httptest sets TLS for an https URL. No Accept, Accept-Encoding or
	// Cookie: the visitor is anonymous and the body is identity-encoded.
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, src.base.Scheme+"://"+src.base.Host+path, nil)
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("User-Agent", "hatamoto-static-export")
	req.Header.Set("Sec-Purpose", "prefetch")
	src.s.router.ServeHTTP(w, req)
	if w.err != nil {
		return staticexport.Response{}, w.err
	}
	return staticexport.Response{Status: w.status, Header: w.h}, nil
}

// Redirects returns the active redirect rules.
func (src *staticSource) Redirects(ctx context.Context) ([]*redirect.Redirect, error) {
	return redirect.NewRepository(src.s.db).GetActiveRedirects(ctx)
}

// streamWriter is an http.ResponseWriter that streams the body to a writer, so
// a large media file is never held in memory.
type streamWriter struct {
	h      http.Header
	body   io.Writer
	status int
	wrote  bool
	err    error
}

func (w *streamWriter) Header() http.Header { return w.h }

func (w *streamWriter) WriteHeader(code int) {
	if w.wrote || (code >= 100 && code < 200) {
		return
	}
	w.wrote = true
	w.status = code
}

func (w *streamWriter) Write(b []byte) (int, error) {
	w.wrote = true
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.body.Write(b)
	if err != nil {
		w.err = err
	}
	return n, err
}

func (w *streamWriter) Flush() {}
