package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/safefetch"
	"hatamoto.dev/core/internal/wpimport"
)

const importWordPressUsage = `usage: hatamoto import wordpress (--url URL [--user U --app-password P] | --wxr FILE) [--scan-only] [--resume RUN]
                                [--audit-url URL] [--allow-private-hosts] [--no-permalinks] [--keep-edited] [--json]`

type wpFlags struct {
	url, user, pass, wxr, resume, auditURL  string
	scanOnly, allowPrivate, noPerma, asJSON bool
	keepEdited                              bool
	mediaConc, maxItems                     int
}

func parseWPFlags(args []string) (*wpFlags, error) {
	var f wpFlags
	if _, err := flagSet("import wordpress", args, func(fs *flag.FlagSet) {
		fs.StringVar(&f.url, "url", "", "WordPress site URL (REST API source)")
		fs.StringVar(&f.user, "user", "", "WordPress user for the application password")
		fs.StringVar(&f.pass, "app-password", "", "application password (or set WP_APP_PASSWORD)")
		fs.StringVar(&f.wxr, "wxr", "", "WordPress export file (WXR source)")
		fs.BoolVar(&f.scanOnly, "scan-only", false, "print the scan report and import nothing")
		fs.StringVar(&f.resume, "resume", "", "continue an earlier run (id); finished work is not repeated")
		fs.StringVar(&f.auditURL, "audit-url", "", "check every old URL against this running Hatamoto site (301/200)")
		fs.BoolVar(&f.allowPrivate, "allow-private-hosts", false, "allow a source on a private or loopback address")
		fs.BoolVar(&f.noPerma, "no-permalinks", false, "keep the current URL patterns instead of the WordPress permalink structure")
		fs.BoolVar(&f.keepEdited, "keep-edited", false, "on a re-run, leave items alone that were edited in Hatamoto since the last import (default: overwrite and list them)")
		fs.BoolVar(&f.asJSON, "json", false, "print the final report as JSON")
		fs.IntVar(&f.mediaConc, "media-concurrency", 4, "parallel media downloads")
		fs.IntVar(&f.maxItems, "max-items", 0, "refuse sources with more items than this (default 50000)")
	}); err != nil {
		return nil, err
	}
	if f.pass == "" {
		f.pass = os.Getenv("WP_APP_PASSWORD")
	}
	if (f.url == "") == (f.wxr == "") && f.resume == "" {
		return nil, errors.New(importWordPressUsage)
	}
	if f.url != "" && f.wxr != "" {
		return nil, errors.New("use --url or --wxr, not both")
	}
	return &f, nil
}

// runImportWordPress is `hatamoto import wordpress`.
func runImportWordPress(ctx context.Context, db *database.DB, cfg *config.Config, args []string, logger *slog.Logger, out io.Writer) error {
	f, err := parseWPFlags(args)
	if err != nil {
		return err
	}
	if err := migrateUp(ctx, db.Pool(), logger); err != nil {
		return err
	}
	deps := wpimport.NewDeps(db, wpimport.DepsConfig{MediaDir: mediaDirOr(cfg.Server.MediaUploadDir), MediaBaseURL: cfg.Server.MediaBaseURL, MediaMaxMB: cfg.Server.MediaMaxSizeMB}, logger)
	defer deps.Close()
	return importWordPress(ctx, deps, f, out)
}

func importWordPress(ctx context.Context, deps *wpimport.Deps, f *wpFlags, out io.Writer) error {
	say := func(phase, msg string) { fmt.Fprintf(out, "[%s] %s\n", phase, msg) }
	// One import at a time, shared with the admin wizard's jobs.
	release, err := wpimport.AcquireImportLock(ctx, deps.DB.Pool())
	if err != nil {
		if errors.Is(err, wpimport.ErrImportRunning) {
			return fmt.Errorf("%w (the admin wizard or another `hatamoto import wordpress`): wait for it to finish", err)
		}
		return err
	}
	defer release()
	var resume uuid.UUID
	if f.resume != "" {
		id, err := uuid.Parse(f.resume)
		if err != nil {
			return fmt.Errorf("--resume: %q is not a run id", f.resume)
		}
		resume = id
		var stored struct{ URL, User string }
		var raw json.RawMessage
		if err := deps.DB.Pool().QueryRow(ctx, `SELECT options FROM hatamoto_import_runs WHERE id = $1`, id).Scan(&raw); err != nil {
			return fmt.Errorf("--resume: run %s not found", id)
		}
		_ = json.Unmarshal(raw, &stored)
		if f.url == "" && f.wxr == "" {
			f.url = stored.URL
		}
		if f.user == "" {
			f.user = stored.User
		}
		if f.url == "" && f.wxr == "" {
			return errors.New("--resume: the run was a file import; pass --wxr again")
		}
	}

	var dump *wpimport.Dump
	var client *wpimport.Client
	switch {
	case f.url != "":
		src, err := wpimport.NewRESTSource(wpimport.RESTOptions{
			ClientOptions: wpimport.ClientOptions{URL: f.url, User: f.user, AppPassword: f.pass, AllowPrivate: f.allowPrivate},
			MaxItems:      f.maxItems, Progress: func(m string) { say("read", m) },
		})
		if err != nil {
			return err
		}
		if dump, err = src.Load(ctx); err != nil {
			return err
		}
		client = src.Client()
	default:
		file, err := os.Open(f.wxr)
		if err != nil {
			return err
		}
		defer file.Close()
		say("read", "parsing "+filepath.Base(f.wxr))
		if dump, err = wpimport.ParseWXR(file, wpimport.WXROptions{MaxItems: f.maxItems}); err != nil {
			return err
		}
	}
	st := wpimport.NewStore(deps.DB.Pool(), strings.TrimRight(dump.Site.URL, "/"))

	report := wpimport.Scan(dump, dump.Site.URL)
	state, err := wpimport.InspectSite(ctx, deps.DB.Pool(), wpimport.KeyPrefix(dump.Site.URL))
	if err != nil {
		return err
	}
	report.ApplySiteState(state)
	if f.scanOnly {
		run, err := st.StartRun(ctx, wpimport.RunScanned, storedOptions(f))
		if err != nil {
			return err
		}
		if err := st.FinishRun(ctx, run.ID, wpimport.RunScanned, report, nil); err != nil {
			return err
		}
		if f.asJSON {
			return printJSON(out, report)
		}
		fmt.Fprint(out, report.Summary())
		return nil
	}
	fmt.Fprint(out, report.Summary())

	opts := wpimport.Options{
		AllowPrivate: f.allowPrivate, Client: client, MediaConcurrency: f.mediaConc, NoPermalinks: f.noPerma,
		KeepEdited: f.keepEdited, Progress: say, Stored: storedOptions(f),
	}
	if f.auditURL != "" {
		// The audit target is the operator's own new site: trusted, unlike the
		// source, so --allow-private-hosts does not decide whether it may be local.
		opts.Audit = wpimport.HTTPProber{Base: f.auditURL, Client: safefetch.NewClient(safefetch.ClientOptions{AllowPrivate: true, Timeout: 30 * time.Second})}
	}
	runner, err := wpimport.NewRunner(deps, st, dump, opts)
	if err != nil {
		return err
	}
	start := time.Now()
	res, err := runner.Run(ctx, resume)
	if res != nil {
		printImportResult(out, res, time.Since(start), f.asJSON)
	}
	if err != nil {
		if res == nil { // the run could not start: nothing to resume
			return fmt.Errorf("import failed to start: %w", err)
		}
		return fmt.Errorf("import failed (resume with --resume %s): %w", res.RunID, err)
	}
	problems := res.Failed + len(res.Errors) + len(res.WPContentLeft) + len(res.URLConflicts)
	if res.Audit != nil {
		problems += len(res.Audit.Failures)
	}
	if problems > 0 {
		return fmt.Errorf("import finished with %d problems (run %s): see the report above", problems, res.RunID)
	}
	return nil
}

func storedOptions(f *wpFlags) map[string]any {
	o := map[string]any{"url": f.url, "user": f.user, "allow_private": f.allowPrivate, "no_permalinks": f.noPerma}
	if f.wxr != "" {
		o["wxr"] = filepath.Base(f.wxr)
	}
	return o
}

func printImportResult(out io.Writer, r *wpimport.Result, took time.Duration, asJSON bool) {
	if asJSON {
		_ = printJSON(out, r)
		return
	}
	fmt.Fprintf(out, "\nRun %s finished in %s\n", r.RunID, took.Round(time.Millisecond))
	fmt.Fprintf(out, "  items: %d created, %d updated, %d failed\n", r.Created, r.Updated, r.Failed)
	fmt.Fprintf(out, "  terms %d, authors %d, menus %d, redirects %d\n", r.Terms, r.Authors, r.Menus, r.Redirects)
	fmt.Fprintf(out, "  media: %d imported, %d reused, %d failed; %d links rewritten, %d media URLs rewritten\n", r.MediaImported, r.MediaReused, r.MediaFailed, r.LinksRewritten, r.MediaRewritten)
	for _, e := range r.Errors {
		fmt.Fprintf(out, "  error: %s %s %q: %s\n", e.Kind, e.ID, e.Title, e.Err)
	}
	fmt.Fprintf(out, "  target site: %s\n", r.SiteMode)
	for _, c := range r.URLConflicts {
		fmt.Fprintf(out, "  unresolved URL: %s: %s\n", c.URL, c.Reason)
	}
	if r.RedirectsRemoved > 0 {
		fmt.Fprintf(out, "  %d redirects of an earlier run removed\n", r.RedirectsRemoved)
	}
	for _, e := range r.OverwrittenEdits {
		fmt.Fprintf(out, "  overwrote edits of: %s\n", e)
	}
	for _, e := range r.KeptEdits {
		fmt.Fprintf(out, "  kept edits of: %s\n", e)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}
	for _, u := range r.WPContentLeft {
		fmt.Fprintf(out, "  wp-content URL left: %s\n", u)
	}
	if a := r.Audit; a != nil {
		fmt.Fprintf(out, "  URL audit: %d checked, %d answer 200, %d answer one 301, %d failed\n", a.Checked, a.OK, a.Redirected, len(a.Failures))
		for _, fl := range a.Failures {
			fmt.Fprintf(out, "    %s: %s\n", fl.URL, fl.Reason)
		}
	} else {
		fmt.Fprintln(out, "  URL audit skipped: pass --audit-url https://your-new-site to check every old URL")
	}
}
