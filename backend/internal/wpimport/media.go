package wpimport

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/media"
)

var sizeSuffixRe = regexp.MustCompile(`(?:-scaled)?(?:-\d{2,5}x\d{2,5})?(\.[A-Za-z0-9]+)$`)

// mediaKey is the host-independent identity of a WordPress file: the path
// below wp-content/uploads without the generated size suffix ("-300x200") or
// "-scaled", so every size of an image maps to the one imported original.
func mediaKey(u *url.URL) string {
	p := u.EscapedPath()
	if i := strings.Index(p, "/wp-content/uploads/"); i >= 0 {
		p = p[i+len("/wp-content/uploads/"):]
	} else if i := strings.Index(p, "/wp-content/"); i >= 0 {
		p = p[i+1:]
	}
	if un, err := url.PathUnescape(p); err == nil {
		p = un
	}
	return sizeSuffixRe.ReplaceAllString(p, "$1")
}

// mediaRegistry maps the files of the old site to imported media.
type mediaRegistry struct {
	mu    sync.Mutex
	byKey map[string]*media.Media
	byID  map[string]*media.Media
	fail  map[string]error
}

func newMediaRegistry() *mediaRegistry {
	return &mediaRegistry{byKey: map[string]*media.Media{}, byID: map[string]*media.Media{}, fail: map[string]error{}}
}

func (g *mediaRegistry) get(key string) (*media.Media, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.byKey[key]
	return m, ok
}

func (g *mediaRegistry) add(src *Media, m *media.Media) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if src == nil {
		return
	}
	g.byID[src.ID] = m
	for _, raw := range append([]string{src.URL}, src.Sizes...) {
		if u, err := url.Parse(raw); err == nil {
			g.byKey[mediaKey(u)] = m
		}
	}
}

func (g *mediaRegistry) addKey(key string, m *media.Media) {
	g.mu.Lock()
	g.byKey[key] = m
	g.mu.Unlock()
}

func (g *mediaRegistry) failure(key string, err error) {
	g.mu.Lock()
	g.fail[key] = err
	g.mu.Unlock()
}

func (g *mediaRegistry) failed(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.fail[key]
	return ok
}

// importMedia imports the media library with bounded concurrency.
func (r *Runner) importMedia(ctx context.Context) {
	if r.opts.SkipMedia {
		r.say("media", "skipped by option")
		return
	}
	items := r.dump.Media
	sem := make(chan struct{}, r.opts.MediaConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range items {
		m := &items[i]
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			_, reused, err := r.fetchMedia(ctx, m)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				r.res.MediaFailed++
				r.res.Errors = append(r.res.Errors, ItemError{Kind: "media", ID: m.ID, Title: m.URL, Err: err.Error()})
				if u, perr := url.Parse(m.URL); perr == nil {
					r.media.failure(mediaKey(u), err)
				}
			case reused:
				r.res.MediaReused++
			default:
				r.res.MediaImported++
			}
		}()
	}
	wg.Wait()
}

// fetchMedia imports one library file: reuse the earlier import of the same
// source id, or of byte-identical content; else download and upload it.
func (r *Runner) fetchMedia(ctx context.Context, src *Media) (*media.Media, bool, error) {
	if id, ok, err := r.st.Lookup(ctx, KindMedia, src.ID); err != nil {
		return nil, false, err
	} else if ok {
		if m, err := r.d.Media.Get(ctx, id); err == nil {
			r.media.add(src, m)
			return m, true, nil
		}
	}
	m, reused, sum, err := r.download(ctx, src.URL, src.Mime, src.Title)
	if err != nil {
		return nil, false, err
	}
	r.media.add(src, m)
	if !reused {
		alt, capt := src.Alt, src.Caption
		if alt != "" || capt != "" {
			if upd, err := r.d.Media.Update(ctx, m.ID, &media.UpdateMediaInput{AltText: &alt, Caption: &capt}); err == nil {
				m = upd
				r.media.add(src, m)
			}
		}
	}
	if err := r.st.Put(ctx, r.run.ID, KindMedia, src.ID, m.ID, sum); err != nil {
		return nil, false, err
	}
	return m, reused, nil
}

// download fetches rawURL and stores it unless the same bytes were imported before.
func (r *Runner) download(ctx context.Context, rawURL, mime, title string) (*media.Media, bool, string, error) {
	maxBytes := int64(r.d.Media.Config().MaxSizeMB) << 20
	tmp, size, sum, err := r.client.Download(ctx, rawURL, r.opts.WorkDir, maxBytes)
	if err != nil {
		return nil, false, "", err
	}
	defer os.Remove(tmp)
	// Downloads run in parallel; the check for identical bytes and the upload
	// do not, or two copies of one file would both miss.
	r.mediaMu.Lock()
	defer r.mediaMu.Unlock()
	if m, ok := r.hashes[sum]; ok {
		return m, true, sum, nil
	}
	if id, ok, err := r.st.LookupHash(ctx, KindMedia, sum); err != nil {
		return nil, false, "", err
	} else if ok {
		if m, err := r.d.Media.Get(ctx, id); err == nil {
			r.hashes[sum] = m
			return m, true, sum, nil
		}
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, false, "", err
	}
	defer f.Close()
	u, _ := url.Parse(rawURL)
	name := path.Base(u.Path)
	m, err := r.d.Media.Upload(ctx, r.opts.Actor, f, name, mime, size)
	if err != nil {
		return nil, false, "", fmt.Errorf("%s: %w", name, err)
	}
	r.hashes[sum] = m
	return m, false, sum, nil
}

// resolveMedia maps a URL of the old site to the new media URL, importing the
// file on demand when the library listing did not have it (theme assets, files
// of a WXR without attachments).
func (r *Runner) resolveMedia(ctx context.Context) func(u *url.URL) (string, bool) {
	return func(u *url.URL) (string, bool) {
		if r.opts.SkipMedia {
			return "", false
		}
		key := mediaKey(u)
		if m, ok := r.media.get(key); ok && m.PublicURL != nil {
			return *m.PublicURL, true
		}
		if r.media.failed(key) {
			return "", false
		}
		abs := *u
		if abs.Host == "" {
			base, _ := url.Parse(r.dump.Site.URL)
			abs.Scheme, abs.Host = base.Scheme, base.Host
		}
		// The original, not a "-300x200" size: sizes exist only on the old server.
		m, _, _, err := r.download(ctx, abs.String(), "", "")
		if err != nil {
			if orig := originalURL(&abs); orig != abs.String() {
				m, _, _, err = r.download(ctx, orig, "", "")
			}
		}
		if err != nil {
			r.media.failure(key, err)
			r.res.Errors = append(r.res.Errors, ItemError{Kind: "media", Title: abs.String(), Err: err.Error()})
			return "", false
		}
		r.media.addKey(key, m)
		r.res.MediaImported++
		if m.PublicURL == nil {
			return "", false
		}
		return *m.PublicURL, true
	}
}

// originalURL strips the size suffix from a media URL.
func originalURL(u *url.URL) string {
	c := *u
	c.Path = sizeSuffixRe.ReplaceAllString(u.Path, "$1")
	c.RawPath = ""
	return c.String()
}
