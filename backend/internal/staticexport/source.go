package staticexport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"hatamoto.dev/core/internal/redirect"
)

// Site is what the exporter needs to know about the site being rendered.
type Site struct {
	// Base is the public origin; its scheme and host define "same origin".
	Base *url.URL
	// AdminPrefix is the live admin address ("/admin" for the default).
	AdminPrefix string
	// LegacyWPRedirects is on when computed WordPress redirects are active.
	LegacyWPRedirects bool
	// Warnings are notes the source wants in the run report; they never fail it.
	Warnings []string
}

// Response is the status and headers of one fetched page.
type Response struct {
	Status int
	Header http.Header
}

// Source renders the public site. The server's implementation fetches every
// path through its real router, as an anonymous visitor.
type Source interface {
	// Site refuses with ErrSiteAccessOn or an error wrapping ErrRefused.
	Site(ctx context.Context) (Site, error)
	// Seeds lists the sitemap paths and the fixed root files.
	Seeds(ctx context.Context) ([]string, error)
	// Fetch does a GET and streams the body into body.
	Fetch(ctx context.Context, path string, body io.Writer) (Response, error)
	// Redirects returns the active redirect rules from the database.
	Redirects(ctx context.Context) ([]*redirect.Redirect, error)
}

// ErrSiteAccessOn is returned when the site access gate is on.
var ErrSiteAccessOn = errors.New("static export refused: the site access gate is on (Settings > Site access)")

// ErrRefused is wrapped by every other refusal to export.
var ErrRefused = errors.New("static export refused")

// optionalSeeds may answer 404 without failing the export: the site simply
// has no such file.
var optionalSeeds = map[string]bool{
	"/ads.txt": true, "/app-ads.txt": true, "/feed.xml": true, "/atom.xml": true,
	"/favicon.ico": true, "/favicon-16.png": true, "/favicon-32.png": true,
	"/apple-icon.png": true, "/icon-192.png": true, "/icon-512.png": true,
}

// notFoundPath is fetched once: it must answer 404 with an HTML page, which
// becomes 404.html.
const notFoundPath = "/404.html"
