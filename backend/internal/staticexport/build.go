package staticexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/redirect"
)

const (
	defaultConcurrency = 4
	stagingTmp         = ".staging-tmp"
	maxErrors          = 50
	maxSkipWarnings    = 20
	maxSitemapTries    = 20
	defaultRetryWait   = 600 * time.Millisecond
)

// BuildOptions tunes Build. The zero value is usable.
type BuildOptions struct {
	Concurrency int
	Progress    func(format string, a ...any)
}

// Report counts what Build wrote.
type Report struct {
	Pages, Assets, Media, Redirects, Stubs, Skipped int
	Bytes                                           int64
	Warnings, Errors                                []string
}

// retrySleep waits between sitemap retries; tests replace it.
var retrySleep = func(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Build crawls the site through src into dir (an empty staging directory):
// every seed and every same-origin link found in a fetched body, redirect
// stubs, _redirects and 404.html. It returns the manifest of what was
// written. Errors do not stop the crawl; they are collected and the first is
// returned at the end.
func Build(ctx context.Context, src Source, site Site, dir string, o BuildOptions) (Manifest, Report, error) {
	b := &builder{
		src: src, site: site, dir: dir, o: o,
		tmp:       filepath.Join(dir, stagingTmp),
		seen:      map[string]bool{},
		seeds:     map[string]bool{},
		ruleSrc:   map[string]bool{},
		manifest:  Manifest{Version: manifestVersion, Files: map[string]Entry{}},
		skipTypes: map[string]int{},
	}
	if err := os.MkdirAll(b.tmp, 0o755); err != nil {
		return Manifest{}, Report{}, err
	}
	defer os.RemoveAll(b.tmp)

	if site.LegacyWPRedirects {
		b.warn("computed WordPress redirects are not exported; only redirects found by crawling are written (see docs)")
	}
	seeds, err := src.Seeds(ctx)
	if err != nil {
		return Manifest{}, Report{}, fmt.Errorf("list seeds: %w", err)
	}
	rules, err := src.Redirects(ctx)
	if err != nil {
		return Manifest{}, Report{}, fmt.Errorf("list redirects: %w", err)
	}

	var queue []string
	for _, s := range append(seeds, notFoundPath) {
		b.seeds[s] = true
		queue = append(queue, s)
	}
	var patterns []*redirect.Redirect
	for _, r := range rules {
		if redirect.IsPatternSource(r.SourcePath) {
			patterns = append(patterns, r)
			continue
		}
		b.ruleSrc[r.SourcePath] = true
		queue = append(queue, r.SourcePath)
	}

	b.crawl(ctx, queue)
	if ctx.Err() != nil {
		return Manifest{}, Report{}, ctx.Err()
	}

	b.writeRedirects(patterns)
	return b.finish()
}

type builder struct {
	src  Source
	site Site
	dir  string
	tmp  string
	o    BuildOptions

	mu        sync.Mutex
	seen      map[string]bool
	seeds     map[string]bool
	ruleSrc   map[string]bool
	manifest  Manifest
	report    Report
	lines     []Line
	next      []string
	errs      []string
	skipTypes map[string]int
	skipLog   int
	tmpN      int
	fetched   int
}

func (b *builder) warn(format string, a ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.report.Warnings = append(b.report.Warnings, fmt.Sprintf(format, a...))
}

func (b *builder) fail(format string, a ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errs = append(b.errs, fmt.Sprintf(format, a...))
}

// enqueue records p for the next round unless it was seen before.
func (b *builder) enqueue(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.seen[p] {
		b.next = append(b.next, p)
	}
}

func (b *builder) crawl(ctx context.Context, queue []string) {
	workers := b.o.Concurrency
	if workers <= 0 {
		workers = defaultConcurrency
	}
	for len(queue) > 0 && ctx.Err() == nil {
		var todo []string
		b.mu.Lock()
		for _, p := range queue {
			if !b.seen[p] {
				b.seen[p] = true
				todo = append(todo, p)
			}
		}
		b.next = nil
		b.mu.Unlock()

		ch := make(chan string)
		var wg sync.WaitGroup
		for range min(workers, max(len(todo), 1)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range ch {
					b.process(ctx, p)
				}
			}()
		}
		for _, p := range todo {
			if ctx.Err() != nil {
				break
			}
			ch <- p
		}
		close(ch)
		wg.Wait()

		b.mu.Lock()
		queue = b.next
		b.mu.Unlock()
	}
}

// process fetches one URL path (with optional raw query) and records the outcome.
func (b *builder) process(ctx context.Context, p string) {
	urlPath, rawQuery, _ := strings.Cut(p, "?")
	if Excluded(urlPath, b.site.AdminPrefix) {
		return
	}
	if isAssetPath(urlPath) {
		// A cache-busting query (?v=hash) names the file at the path; static
		// hosts ignore it, so the file is exported without it.
		rawQuery = ""
	}
	if skip, why := QueryKind(rawQuery); skip {
		b.skip(p, why)
		return
	}

	file, size, sum, resp, err := b.fetch(ctx, urlPath)
	if err != nil {
		if ctx.Err() == nil {
			b.fail("%s: %v", p, err)
		}
		return
	}
	if file != "" {
		defer os.Remove(file)
	}
	b.countFetched()

	ct := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	status := resp.Status

	if urlPath == notFoundPath {
		if status != http.StatusNotFound || mt != "text/html" {
			b.fail("%s: want a 404 HTML page, got status %d type %q", p, status, ct)
			return
		}
		b.store(urlPath, file, size, sum, ct, mt, false)
		return
	}

	switch {
	case status >= 200 && status < 300:
		if b.ruleSrc[urlPath] {
			b.warn("redirect rule %s is shadowed by a page and is not exported", urlPath)
		}
		b.store(urlPath, file, size, sum, ct, mt, true)
	case status >= 300 && status < 400:
		b.redirected(urlPath, resp)
	case status == http.StatusNotFound || status == http.StatusGone:
		switch {
		case optionalSeeds[urlPath]:
		case b.seeds[urlPath]:
			b.fail("%s: render %d", p, status)
		default:
			b.warn("%s answers %d (broken link on the live site)", p, status)
		}
	default:
		b.fail("%s: render %d", p, status)
	}
}

func (b *builder) skip(p, why string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.report.Skipped++
	b.skipTypes[why]++
	if b.skipLog < maxSkipWarnings {
		b.skipLog++
		b.report.Warnings = append(b.report.Warnings, fmt.Sprintf("skipped %s (%s is not exported, see docs)", p, why))
	}
}

func (b *builder) countFetched() {
	b.mu.Lock()
	b.fetched++
	n := b.fetched
	b.mu.Unlock()
	if b.o.Progress != nil && n%200 == 0 {
		b.o.Progress("static: fetched %d URLs", n)
	}
}

// fetch streams one GET into a temp file, retrying sitemap pages that answer
// 429. The returned file is removed by the caller.
func (b *builder) fetch(ctx context.Context, urlPath string) (file string, size int64, sum string, resp Response, err error) {
	retries := 0
	isSitemap := strings.HasPrefix(urlPath, "/sitemap-") && strings.HasSuffix(urlPath, ".xml")
	for {
		if file != "" {
			os.Remove(file)
			file = ""
		}
		b.mu.Lock()
		b.tmpN++
		name := filepath.Join(b.tmp, strconv.Itoa(b.tmpN))
		b.mu.Unlock()
		f, cerr := os.Create(name)
		if cerr != nil {
			return "", 0, "", Response{}, cerr
		}
		h := sha256.New()
		cw := &countingWriter{w: io.MultiWriter(f, h)}
		resp, err = b.src.Fetch(ctx, urlPath, cw)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(name)
			return "", 0, "", Response{}, err
		}
		if resp.Header == nil {
			resp.Header = http.Header{}
		}
		if resp.Status == http.StatusTooManyRequests && isSitemap && retries < maxSitemapTries {
			retries++
			wait := defaultRetryWait
			if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && s > 0 {
				wait = time.Duration(s) * time.Second
			}
			os.Remove(name)
			retrySleep(ctx, wait)
			if ctx.Err() != nil {
				return "", 0, "", Response{}, ctx.Err()
			}
			continue
		}
		return name, cw.n, hex.EncodeToString(h.Sum(nil)), resp, nil
	}
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func isTextType(mt string) bool {
	return strings.HasPrefix(mt, "text/") || strings.Contains(mt, "xml") || strings.Contains(mt, "json")
}

// store moves a fetched body into place, records it in the manifest and
// enqueues the links it contains. count says whether it counts as a page,
// asset or media file.
func (b *builder) store(urlPath, tmpFile string, size int64, sum, ct, mt string, count bool) {
	out, err := FilePath(urlPath, mt == "text/html")
	if err != nil {
		b.fail("%s: %v", urlPath, err)
		return
	}
	if err := b.checkOutput(out); err != nil {
		b.fail("%s: %v", urlPath, err)
		return
	}
	var text []byte
	if isTextType(mt) {
		if text, err = os.ReadFile(tmpFile); err != nil {
			b.fail("%s: %v", urlPath, err)
			return
		}
		if p := b.site.AdminPrefix; p != "" && p != "/admin" && strings.Contains(string(text), p) {
			b.fail("%s: the body contains the admin address", urlPath)
			return
		}
	}
	if ct == "" {
		ct = ContentType(out)
	}
	entry := Entry{SHA256: sum, Size: size, Type: ct}

	b.mu.Lock()
	if old, ok := b.manifest.Files[out]; ok {
		b.mu.Unlock()
		if old.SHA256 != sum {
			b.fail("%s: two URLs map to %s with different content", urlPath, out)
		}
		return
	}
	b.manifest.Files[out] = entry
	b.mu.Unlock()

	if err := place(tmpFile, filepath.Join(b.dir, filepath.FromSlash(out))); err != nil {
		b.fail("%s: %v", urlPath, err)
		return
	}

	b.mu.Lock()
	if count {
		switch {
		case strings.HasPrefix(out, "media/"):
			b.report.Media++
		case path.Ext(out) == ".html":
			b.report.Pages++
		default:
			b.report.Assets++
		}
	}
	b.report.Bytes += size
	b.mu.Unlock()

	if text != nil {
		base, err := b.site.Base.Parse(urlPath)
		if err != nil {
			return
		}
		for _, l := range Links(base, ct, text) {
			b.enqueue(l)
		}
	}
}

// checkOutput refuses output paths that would escape or hide in the staging
// area or sit under the admin address.
func (b *builder) checkOutput(out string) error {
	if out == stagingTmp || strings.HasPrefix(out, stagingTmp+"/") {
		return errors.New("path collides with the staging directory")
	}
	if p := b.site.AdminPrefix; p != "" && p != "/" && underPrefix("/"+out, p) {
		return errors.New("output path is under the admin address")
	}
	return nil
}

func place(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

// redirected records a 3xx answer: a stub file, a _redirects line, and the
// target joins the crawl when it is on the same origin.
func (b *builder) redirected(urlPath string, resp Response) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		b.fail("%s: redirect %d without Location", urlPath, resp.Status)
		return
	}
	target, err := b.site.Base.Parse(loc)
	if err != nil {
		b.fail("%s: bad Location %q", urlPath, loc)
		return
	}
	to := target.String()
	if sameOrigin(b.site.Base, target) {
		to = target.EscapedPath()
		if to == "" {
			to = "/"
		}
		if target.RawQuery != "" {
			to += "?" + target.RawQuery
		}
		b.enqueue(to)
	}

	out, err := FilePath(urlPath, true)
	if err == nil {
		err = b.checkOutput(out)
	}
	if err != nil {
		b.fail("%s: %v", urlPath, err)
		return
	}
	body := Stub(target.String())
	sum := sha256.Sum256(body)
	b.mu.Lock()
	if _, ok := b.manifest.Files[out]; ok {
		b.mu.Unlock()
		return
	}
	b.manifest.Files[out] = Entry{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Type: ContentType(out)}
	b.lines = append(b.lines, Line{From: urlPath, To: to, Status: resp.Status})
	b.report.Stubs++
	b.mu.Unlock()

	full := filepath.Join(b.dir, filepath.FromSlash(out))
	err = os.MkdirAll(filepath.Dir(full), 0o755)
	if err == nil {
		err = os.WriteFile(full, body, 0o644)
	}
	if err != nil {
		b.fail("%s: %v", urlPath, err)
	}
}

func sameOrigin(a, c *url.URL) bool {
	return a.Scheme == c.Scheme && strings.EqualFold(a.Host, c.Host)
}

// writeRedirects renders _redirects from the stub lines and the pattern rules.
func (b *builder) writeRedirects(patterns []*redirect.Redirect) {
	body, warns := WriteRedirects(b.lines, patterns)
	for _, w := range warns {
		b.warn("%s", w)
	}
	if len(body) == 0 {
		return
	}
	sum := sha256.Sum256(body)
	b.manifest.Files[RedirectsFile] = Entry{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Type: "text/plain; charset=utf-8"}
	b.report.Redirects = strings.Count(string(body), "\n")
	b.report.Bytes += int64(len(body))
	if err := os.WriteFile(filepath.Join(b.dir, RedirectsFile), body, 0o644); err != nil {
		b.fail("%s: %v", RedirectsFile, err)
	}
}

func (b *builder) finish() (Manifest, Report, error) {
	r := b.report
	why := make([]string, 0, len(b.skipTypes))
	for k := range b.skipTypes {
		why = append(why, k)
	}
	sort.Strings(why)
	if r.Skipped > maxSkipWarnings {
		r.Warnings = append(r.Warnings, fmt.Sprintf("... and %d more skipped URLs", r.Skipped-maxSkipWarnings))
	}
	if r.Skipped > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("skipped %d query URLs (%s), see docs", r.Skipped, strings.Join(why, ", ")))
	}
	sort.Strings(r.Warnings)

	sort.Strings(b.errs)
	if len(b.errs) > maxErrors {
		b.errs = b.errs[:maxErrors]
	}
	r.Errors = b.errs
	if len(b.errs) == 0 {
		return b.manifest, r, nil
	}
	err := errors.New(b.errs[0])
	if len(b.errs) > 1 {
		err = fmt.Errorf("%s (and %d more errors)", b.errs[0], len(b.errs)-1)
	}
	return b.manifest, r, err
}
