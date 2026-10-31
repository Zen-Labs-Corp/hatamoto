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
	"sort"
	"strings"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/localeconv"
)

const localesUsage = `  locales convert --suggest | --plan FILE [--apply] | --revert ID [--force] | --history  [--json | --text]
                                   convert region page trees to core locales (dry run by default; exit 0 ok, 2 blocking errors, 1 usage or IO)`

const localesConvertUsageLine = "usage: hatamoto locales convert --suggest | --plan FILE [--apply] | --revert ID [--force] | --history [--json | --text]"

// contentPollNote is printed after a write. The server polls the content
// change log every 3 seconds (1 second in development, see
// Server.contentEpochEvery) and then drops its cached site config, URL
// registry and rendered pages.
const contentPollNote = "running servers pick the change up within the content poll interval (3 seconds, 1 second in development); no restart is needed"

// localesOpts are the flags of `hatamoto locales convert`.
type localesOpts struct {
	Suggest bool
	Plan    string
	Apply   bool
	Revert  int64
	Force   bool
	History bool
	JSON    bool
	Text    bool
}

func (o *localesOpts) revert() bool { return o.Revert != 0 }

func parseLocalesOpts(args []string) (*localesOpts, error) {
	var o localesOpts
	pos, err := flagSet("locales convert", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&o.Suggest, "suggest", false, "read the data and print a plan, write nothing")
		fs.StringVar(&o.Plan, "plan", "", "plan JSON `file`: dry run, or apply with --apply")
		fs.BoolVar(&o.Apply, "apply", false, "apply the plan (needs --plan)")
		fs.Int64Var(&o.Revert, "revert", 0, "undo conversion `ID`")
		fs.BoolVar(&o.Force, "force", false, "revert even when rows were edited since (only with --revert)")
		fs.BoolVar(&o.History, "history", false, "list conversions")
		fs.BoolVar(&o.JSON, "json", false, "print JSON (default)")
		fs.BoolVar(&o.Text, "text", false, "print readable text")
	})
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, errors.New(localesConvertUsageLine)
	}
	modes := 0
	for _, on := range []bool{o.Suggest, o.Plan != "", o.revert(), o.History} {
		if on {
			modes++
		}
	}
	switch {
	case modes != 1:
		return nil, errors.New("give exactly one of --suggest, --plan, --revert, --history\n" + localesConvertUsageLine)
	case o.Apply && o.Plan == "":
		return nil, errors.New("--apply needs --plan FILE")
	case o.Force && !o.revert():
		return nil, errors.New("--force is only valid with --revert")
	case o.JSON && o.Text:
		return nil, errors.New("--json and --text exclude each other")
	case o.Revert < 0:
		return nil, errors.New("--revert: ID must be a positive number")
	}
	return &o, nil
}

// runLocalesConvert parses the flags and runs the conversion against databaseURL.
func runLocalesConvert(ctx context.Context, databaseURL string, args []string, out, errOut io.Writer) error {
	o, err := parseLocalesOpts(args)
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Database.URL = databaseURL
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return localesConvert(ctx, localeconv.NewService(db.Pool(), quiet), o, out, errOut)
}

// localesConvert runs the chosen mode against svc. Blocking errors are
// printed and returned as exitError{2}; any other failure is a plain error
// (exit 1).
func localesConvert(ctx context.Context, svc localeconv.Service, o *localesOpts, out, errOut io.Writer) error {
	var err error
	switch {
	case o.Suggest:
		err = localesSuggest(ctx, svc, o, out, errOut)
	case o.Plan != "":
		err = localesPlan(ctx, svc, o, out)
	case o.revert():
		err = localesRevert(ctx, svc, o, out)
	default:
		err = localesHistory(ctx, svc, o, out)
	}
	var be *localeconv.BlockingError
	if errors.As(err, &be) {
		if perr := printBlocking(be, o, out); perr != nil {
			return perr
		}
		return exitError{2}
	}
	return err
}

func localesSuggest(ctx context.Context, svc localeconv.Service, o *localesOpts, out, errOut io.Writer) error {
	plan, warns, err := svc.Suggest(ctx)
	if err != nil {
		return err
	}
	for _, w := range warns {
		fmt.Fprintf(errOut, "warning: %s: %s\n", w.Code, w.Message)
	}
	return writeJSON(out, plan)
}

func localesPlan(ctx context.Context, svc localeconv.Service, o *localesOpts, out io.Writer) error {
	f, err := os.Open(o.Plan)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	defer f.Close()
	plan, err := localeconv.ParsePlan(f)
	if err != nil {
		return err
	}
	if !o.Apply {
		rep, err := svc.DryRun(ctx, plan)
		if err != nil {
			return err
		}
		if err := printReport(out, rep, o); err != nil {
			return err
		}
		if !rep.OK {
			return exitError{2}
		}
		return nil
	}
	rec, rep, err := svc.Apply(ctx, plan, localeconv.ApplyOpts{By: "cli"})
	if err != nil {
		return err
	}
	if o.Text {
		printReportText(out, rep)
		fmt.Fprintf(out, "\napplied conversion %d: %d rows\nrevert with: hatamoto locales convert --revert %d\n%s\n", rec.ID, rec.Rows, rec.ID, contentPollNote)
		return nil
	}
	return writeJSON(out, map[string]any{"record": rec, "report": rep, "note": contentPollNote})
}

func localesRevert(ctx context.Context, svc localeconv.Service, o *localesOpts, out io.Writer) error {
	res, err := svc.Revert(ctx, o.Revert, o.Force, "cli")
	if err != nil {
		return err
	}
	if o.Text {
		fmt.Fprintf(out, "reverted conversion %d: %d rows restored, %d stranded\n", res.ConversionID, res.Restored, res.Stranded)
		for _, i := range res.Forced {
			fmt.Fprintf(out, "  overwritten: %s: %s\n", i.Code, i.Message)
		}
		for _, i := range res.StrandedItems {
			fmt.Fprintf(out, "  stranded: %s: %s\n", i.Code, i.Message)
		}
		fmt.Fprintln(out, contentPollNote)
		return nil
	}
	return writeJSON(out, map[string]any{"result": res, "note": contentPollNote})
}

func localesHistory(ctx context.Context, svc localeconv.Service, o *localesOpts, out io.Writer) error {
	items, err := svc.History(ctx)
	if err != nil {
		return err
	}
	if !o.Text {
		if items == nil {
			items = []localeconv.HistoryItem{}
		}
		return writeJSON(out, items)
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "no conversions")
		return nil
	}
	for _, h := range items {
		fmt.Fprintf(out, "%d  %s  %s  by %s  rows %d  locales %s\n", h.ID, h.AppliedAt.UTC().Format("2006-01-02 15:04:05"), h.Status, h.AppliedBy, h.Rows, strings.Join(h.Locales, ","))
	}
	return nil
}

func printBlocking(be *localeconv.BlockingError, o *localesOpts, out io.Writer) error {
	if be.Report != nil {
		return printReport(out, be.Report, o)
	}
	if !o.Text {
		issues := be.Issues
		if issues == nil {
			issues = []localeconv.Issue{}
		}
		return writeJSON(out, map[string]any{"ok": false, "errors": issues})
	}
	fmt.Fprintln(out, "blocked")
	for _, i := range be.Issues {
		fmt.Fprintf(out, "  error: %s: %s\n", i.Code, i.Message)
	}
	return nil
}

func printReport(out io.Writer, rep *localeconv.Report, o *localesOpts) error {
	if o.Text {
		printReportText(out, rep)
		return nil
	}
	return writeJSON(out, rep)
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printReportText(out io.Writer, r *localeconv.Report) {
	status := "ok to apply"
	if !r.OK {
		status = "blocked"
	}
	fmt.Fprintf(out, "dry run: %s\nplan %s\ndata %s\n", status, r.PlanSHA256, r.DataFingerprint)
	for _, i := range r.Errors {
		fmt.Fprintf(out, "  error: %s: %s\n", i.Code, i.Message)
	}
	for _, i := range r.Warnings {
		fmt.Fprintf(out, "  warning: %s: %s\n", i.Code, i.Message)
	}
	fmt.Fprintln(out, "\nlocales:")
	for _, l := range r.Locales {
		fmt.Fprintf(out, "  %s /%s  home %s -> %s  %s  total %d\n", l.Tag, l.Segment, l.Home.OldPath, l.Home.NewPath, countsText(l.CountsBySchema), l.Total)
	}
	fmt.Fprintf(out, "  shared  %s  total %d\n", countsText(r.Shared.CountsBySchema), r.Shared.Total)
	fmt.Fprintf(out, "\npath changes: %d\n", r.PathChanges.Count)
	for _, p := range r.PathChanges.Items {
		fmt.Fprintf(out, "  %s: %s -> %s\n", p.ID, p.Old, p.New)
	}
	fmt.Fprintf(out, "renamed collisions: %d\n", len(r.RenamedCollisions))
	for _, c := range r.RenamedCollisions {
		fmt.Fprintf(out, "  %s %s: %s -> %s (%s)\n", c.Schema, c.ID, c.OldSlug, c.NewSlug, c.Reason)
	}
	fmt.Fprintf(out, "groups: %d formed, %d singletons, %d conflicts\n", r.Groups.Formed, r.Groups.Singletons, len(r.Groups.Conflicts))
	fmt.Fprintf(out, "overrides: %d sitemap group removed, kept keys [%s]\n", len(r.Overrides.SitemapGroupRemoved), strings.Join(r.Overrides.KeptKeys, ", "))
	fmt.Fprintf(out, "redirects under segments: %d\n", r.Redirects.Count)
	for _, u := range r.UnmatchedValues {
		fmt.Fprintf(out, "unmatched: %s=%s (%d)\n", u.Attribute, u.Value, u.Count)
	}
	s := r.SiteSettings
	fmt.Fprintf(out, "site settings: language %q -> %q, locales %d -> %d, prefix_all %t -> %t, x_default %q -> %q\n",
		s.Language[0], s.Language[1], s.LocalesCount[0], s.LocalesCount[1], s.PrefixAll[0], s.PrefixAll[1], s.XDefault[0], s.XDefault[1])
}

func countsText(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}
