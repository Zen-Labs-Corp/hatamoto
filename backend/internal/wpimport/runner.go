package wpimport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
)

// Options configures a run.
type Options struct {
	// SourceRef identifies the source site; it scopes the id map. Set from the Dump when empty.
	SourceRef string
	// Actor is recorded as the creator of imported rows (nil: none).
	Actor *uuid.UUID
	// AllowPrivate lets media be fetched from a private-network site. It turns
	// the SSRF guard off for everything the source names: set it only from an
	// operator's own command line. Never bind it from request data and never
	// restore it from a stored run's options.
	AllowPrivate bool
	// Client downloads media; built from the site URL when nil (WXR).
	Client *Client
	// MediaConcurrency bounds parallel downloads; 0 means 4.
	MediaConcurrency int
	// KeepPermalinks sets the post and page URL patterns to the WordPress
	// structure. Default true (set NoPermalinks to disable).
	NoPermalinks bool
	// WorkDir holds temporary downloads; empty uses the system temp dir.
	WorkDir string
	// Progress receives one line per step.
	Progress func(phase, msg string)
	// Purge clears rendered-page and sitemap caches after the import. The CLI is a
	// separate process and cannot reach the server's caches: it leaves this nil.
	Purge func(ctx context.Context)
	// Audit checks every old URL against the new site; nil skips it.
	Audit Prober
	// KeepEdited leaves entities alone that were edited in Hatamoto after the
	// last import. By default a re-run overwrites them and lists them in the
	// result.
	KeepEdited bool
	// SkipMedia imports no files: the library is skipped and images and
	// attachments inside bodies are removed or unlinked, as for a file that
	// cannot be fetched (no wp-content URL is left behind).
	SkipMedia bool
	// Options are written to the run (no secrets) so --resume can reuse them.
	Stored map[string]any
}

// ItemError is one thing that failed, with the reason.
type ItemError struct {
	Kind  string `json:"kind"`
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	Err   string `json:"error"`
}

// Result is what a run did.
type Result struct {
	RunID uuid.UUID `json:"run_id"`

	Created int `json:"created"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`

	Terms         int `json:"terms"`
	Authors       int `json:"authors"`
	MediaImported int `json:"media_imported"`
	MediaReused   int `json:"media_reused"`
	MediaFailed   int `json:"media_failed"`
	Menus         int `json:"menus"`
	Redirects     int `json:"redirects"`

	MediaRewritten int      `json:"media_rewritten"`
	LinksRewritten int      `json:"links_rewritten"`
	Unresolved     []string `json:"unresolved,omitempty"`
	// WPContentLeft lists wp-content URLs still present in imported content;
	// the importer's promise is that it is empty.
	WPContentLeft []string `json:"wp_content_left,omitempty"`

	// SiteMode is how the target was treated: "empty" or "existing".
	SiteMode SiteMode `json:"site_mode"`
	// URLConflicts are old URLs that could not be redirected because a live
	// page or a redirect of the owner already answers there.
	URLConflicts []URLConflict `json:"url_conflicts,omitempty"`
	// RedirectsRemoved counts redirects of an earlier run that are no longer needed.
	RedirectsRemoved int `json:"redirects_removed,omitempty"`
	// OverwrittenEdits and KeptEdits list entities edited in Hatamoto since the
	// last import, which a re-run overwrote or (with KeepEdited) left alone.
	OverwrittenEdits []string `json:"overwritten_edits,omitempty"`
	KeptEdits        []string `json:"kept_edits,omitempty"`

	Audit    *AuditResult `json:"audit,omitempty"`
	Errors   []ItemError  `json:"errors,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Report   *Report      `json:"report,omitempty"`
}

type entInfo struct {
	item    *Item
	ent     *entity.Entity
	newPath string
	body    entity.ImportItem
}

// URLConflict is an old URL the importer left alone, with the reason.
type URLConflict struct {
	URL    string `json:"url"`
	Target string `json:"target,omitempty"` // where the imported item lives
	Reason string `json:"reason"`
}

// Runner writes a Dump into Hatamoto.
type Runner struct {
	d    *Deps
	st   *Store
	opts Options
	dump *Dump
	// state is the target site before the import; mode follows from it.
	state *SiteState
	run   *Run
	res   *Result

	client *Client
	media  *mediaRegistry
	// mediaMu serialises the identical-bytes check and upload; hashes caches the result.
	mediaMu sync.Mutex
	hashes  map[string]*media.Media

	hosts         map[string]bool
	siteHost      string
	authorIDs     map[string]uuid.UUID
	termIDs       map[string]uuid.UUID
	schemaOf      map[string]string // post type -> schema slug
	infos         map[string]*entInfo
	byID          map[uuid.UUID]*entity.Entity
	oldPaths      map[string]*entInfo // normalised old path -> info
	termPaths     map[string]string   // old term/author path -> new path
	hiddenAuthors map[string]string   // old author path of a non-public person -> person name
	schemas       map[string]*entity.EntitySchema
}

// NewRunner prepares a run over dump.
func NewRunner(d *Deps, st *Store, dump *Dump, opts Options) (*Runner, error) {
	if opts.MediaConcurrency <= 0 {
		opts.MediaConcurrency = 4
	}
	if opts.Progress == nil {
		opts.Progress = func(string, string) {}
	}
	if opts.WorkDir == "" {
		opts.WorkDir = os.TempDir()
	}
	r := &Runner{d: d, st: st, opts: opts, dump: dump, media: newMediaRegistry(), hashes: map[string]*media.Media{}, res: &Result{},
		hosts: map[string]bool{}, authorIDs: map[string]uuid.UUID{}, termIDs: map[string]uuid.UUID{},
		schemaOf: map[string]string{}, infos: map[string]*entInfo{}, byID: map[uuid.UUID]*entity.Entity{},
		oldPaths: map[string]*entInfo{}, termPaths: map[string]string{}, hiddenAuthors: map[string]string{}, schemas: map[string]*entity.EntitySchema{}}
	u, err := url.Parse(dump.Site.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the source has no usable site URL (%q)", dump.Site.URL)
	}
	r.siteHost = strings.ToLower(u.Hostname())
	r.hosts[r.siteHost] = true
	r.hosts[strings.TrimPrefix(r.siteHost, "www.")] = true
	r.hosts["www."+strings.TrimPrefix(r.siteHost, "www.")] = true
	r.client = opts.Client
	if r.client == nil {
		r.client, err = NewClient(ClientOptions{URL: dump.Site.URL, AllowPrivate: opts.AllowPrivate})
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Runner) say(phase, format string, a ...any) {
	r.opts.Progress(phase, fmt.Sprintf(format, a...))
}

func (r *Runner) phase(ctx context.Context, name string) {
	_ = r.st.SetPhase(ctx, r.run.ID, name)
	r.say(name, "start")
}

// Run imports the dump. resume names an earlier run to continue (its map rows
// are reused, so finished work is not repeated); uuid.Nil starts a new one.
func (r *Runner) Run(ctx context.Context, resume uuid.UUID) (res *Result, err error) {
	if resume != uuid.Nil {
		if r.run, err = r.st.GetRun(ctx, resume); err != nil {
			return nil, err
		}
		if r.run.SourceRef != r.st.ref {
			return nil, fmt.Errorf("run %s belongs to %s, not %s", r.run.ID, r.run.SourceRef, r.st.ref)
		}
		if err = r.st.ResumeRun(ctx, resume); err != nil {
			return nil, err
		}
	} else if r.run, err = r.st.StartRun(ctx, RunRunning, r.opts.Stored); err != nil {
		return nil, err
	}
	r.res.RunID = r.run.ID
	r.res.Warnings = append(r.res.Warnings, r.dump.Warnings...)
	r.res.Report = Scan(r.dump, r.st.ref)
	defer func() {
		status := RunCompleted
		if err != nil {
			status = RunFailed
		}
		if ferr := r.st.FinishRun(context.WithoutCancel(ctx), r.run.ID, status, r.res, err); ferr != nil && err == nil {
			err = ferr
		}
		res = r.res
	}()

	if r.state, err = InspectSite(ctx, r.d.DB.Pool(), r.keyPrefix()); err != nil {
		return r.res, err
	}
	r.res.SiteMode = r.state.Mode
	r.res.Report.ApplySiteState(r.state)
	r.say("inspect", "target site is %s (%d existing items, %d starter)", r.state.Mode, r.state.Existing, r.state.Starter)

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"settings", r.importSettings},
		{"schemas", r.prepareSchemas},
		{"media", func(ctx context.Context) error { r.importMedia(ctx); return nil }},
		{"authors", r.importAuthors},
		{"terms", r.importTerms},
		{"content", r.importContent},
		{"links", r.rewriteLinks},
		{"menus", r.importMenus},
		{"redirects", r.importRedirects},
		{"finish", r.finish},
	}
	for _, s := range steps {
		if err = ctx.Err(); err != nil {
			return r.res, err
		}
		r.phase(ctx, s.name)
		if err = s.fn(ctx); err != nil {
			return r.res, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if r.opts.Audit != nil {
		r.phase(ctx, "audit")
		r.res.Audit = Audit(ctx, r.dump.Site.URL, r.dump.OldURLs, r.opts.Audit)
		r.res.Audit.hideAuthors(r.hiddenAuthors)
		for _, c := range r.res.URLConflicts {
			r.res.Audit.Unresolved = append(r.res.Audit.Unresolved, AuditFailure{URL: c.URL, Status: 200, Reason: c.Reason})
		}
		r.say("audit", "%d URLs checked: %d ok, %d redirected, %d failed, %d author pages hidden until their people are public",
			r.res.Audit.Checked, r.res.Audit.OK, r.res.Audit.Redirected, len(r.res.Audit.Failures), len(r.res.Audit.Hidden))
	}
	return r.res, nil
}

// schema returns a schema by slug, cached.
func (r *Runner) schema(ctx context.Context, slug string) (*entity.EntitySchema, error) {
	if s, ok := r.schemas[slug]; ok {
		return s, nil
	}
	s, err := r.d.Entities.GetSchemaBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	r.schemas[slug] = s
	return s, nil
}

func isBuiltinGone(err error) bool { return errors.Is(err, entity.ErrSchemaNotFound) }

// mediaURL returns the imported public URL for an old-site URL, "" when not importable.
func (r *Runner) mediaURL(ctx context.Context, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return ""
	}
	if got, ok := r.resolveMedia(ctx)(u); ok {
		return got
	}
	return ""
}
