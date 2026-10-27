// Command changelog creates and updates release entries on /changelog. Run from backend/:
//
//	go run ./tools/changelog refresh [--dry-run]
//	go run ./tools/changelog sync vX.Y.Z [--channel auto|canary|edge|stable] [--yanked|--unyank] [--summary-only] [--dry-run]
//	go run ./tools/changelog backfill --channel v0.25.0=stable,v0.26.0=stable,... [--dry-run]
//	go run ./tools/changelog notes vX.Y.Z
//
// refresh (and --channel auto) read the live channel manifests at
// HATAMOTO_RELEASES_URL (default HATAMOTO_URL + "/releases"): the badge is the
// furthest channel whose ramp has started, and yanked follows the manifests.
//
// Credentials: HATAMOTO_URL, HATAMOTO_API_KEY, HATAMOTO_CHANGELOG_SCHEMA_ID from the env, else the
// first existing of ./.hatamoto-changelog.env, then <git-common-dir>/../.hatamoto-changelog.env
// (KEY=VALUE lines). Never from argv. `notes` only reads git and needs no credentials.
//
// Exit codes: 0 ok, 1 failure, 2 usage or credentials.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usage = `usage: changelog <command> [flags]
  refresh  [--dry-run]   every tag: create missing entries, set channel and yanked from the live manifests
  sync     vX.Y.Z [--channel auto|canary|edge|stable] [--yanked|--unyank] [--summary-only] [--dry-run]
  backfill --channel v0.25.0=stable,v0.26.0=edge,... [--dry-run]
  notes    vX.Y.Z        print the grouped notes from git (no network)`

// env carries what tests replace. The zero value means the real process.
type env struct {
	Getenv    func(string) string
	ReadFile  func(string) ([]byte, error)
	Git       gitRunner
	HTTP      *http.Client
	CredPaths []string // nil: ./ then the main checkout
	Now       func() time.Time
}

func (e env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e env) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

func (e env) readFile(p string) ([]byte, error) {
	if e.ReadFile != nil {
		return e.ReadFile(p)
	}
	return os.ReadFile(p)
}

func (e env) git() gitRunner {
	if e.Git != nil {
		return e.Git
	}
	return execGit("")
}

func (e env) credPaths() []string {
	if e.CredPaths != nil {
		return e.CredPaths
	}
	const name = ".hatamoto-changelog.env"
	paths := []string{name}
	if out, err := e.git()("rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(strings.TrimSpace(out)), name))
	}
	return paths
}

// usageError makes run exit 2.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, env{}))
}

func run(args []string, stdout, stderr io.Writer, e env) int {
	err := dispatch(args, stdout, stderr, e)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(stderr, "changelog:", err)
		return 2
	}
	fmt.Fprintln(stderr, "changelog:", err)
	return 1
}

func dispatch(args []string, stdout, stderr io.Writer, e env) error {
	if len(args) == 0 {
		return usageError{"a command is required\n" + usage}
	}
	switch args[0] {
	case "sync":
		return cmdSync(args[1:], stdout, stderr, e)
	case "refresh":
		return cmdRefresh(args[1:], stdout, stderr, e)
	case "backfill":
		return cmdBackfill(args[1:], stdout, stderr, e)
	case "notes":
		return cmdNotes(args[1:], stdout, stderr, e)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return nil
	}
	return usageError{fmt.Sprintf("unknown command %q\n%s", args[0], usage)}
}

// parseFlags parses fs over args, letting positional arguments appear between
// flags (the flag package alone stops at the first one).
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageError{err.Error()}
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func validChannel(c string) error {
	if rankOf(c) < 0 {
		return usageError{fmt.Sprintf("--channel %q must be canary, edge or stable", c)}
	}
	return nil
}

// live reads the published channel manifests next to the site.
func (e env) live() (map[string]Live, error) {
	base := e.getenv("HATAMOTO_RELEASES_URL")
	if base == "" {
		creds, err := loadCreds(e.getenv, e.readFile, e.credPaths())
		if err != nil {
			return nil, usageError{err.Error()}
		}
		base = strings.TrimRight(creds.URL, "/") + "/releases"
	}
	return LiveChannels(e.HTTP, base, e.now())
}

func (e env) client() (*Client, error) {
	creds, err := loadCreds(e.getenv, e.readFile, e.credPaths())
	if err != nil {
		return nil, usageError{err.Error()}
	}
	if err := checkTransport(creds.URL); err != nil {
		return nil, usageError{err.Error()}
	}
	return &Client{Base: creds.URL, Key: creds.Key, SchemaID: creds.SchemaID, HTTP: e.HTTP}, nil
}

// checkTransport refuses to send the API key over plain http, except to the
// local machine.
func checkTransport(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid https URL", envURL)
	}
	switch u.Scheme {
	case "https":
		if u.Host != "" {
			return nil
		}
	case "http":
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
			return nil
		}
	}
	return fmt.Errorf("%s must be an https URL (http only for localhost or 127.0.0.1)", envURL)
}

func cmdSync(args []string, stdout, stderr io.Writer, e env) error {
	fs := newFlags("sync", stderr)
	channel := fs.String("channel", "", "auto, canary, edge or stable (required to create; else keeps the current value)")
	yanked := fs.Bool("yanked", false, "mark the release yanked")
	unyank := fs.Bool("unyank", false, "clear the yanked mark (without --yanked/--unyank it is kept)")
	summaryOnly := fs.Bool("summary-only", false, "write only the placeholder summary, no PR list")
	dry := fs.Bool("dry-run", false, "read only; print what would happen")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{"sync takes exactly one version\n" + usage}
	}
	if *yanked && *unyank {
		return usageError{"--yanked and --unyank are exclusive"}
	}
	var yank *bool
	if *yanked || *unyank {
		yank = yanked
	}
	if *channel == "auto" {
		live, err := e.live()
		if err != nil {
			return err
		}
		l, ok := live[pos[0]]
		if !ok || l.Channel == "" {
			return fmt.Errorf("%s is not live on any channel yet", pos[0])
		}
		*channel = l.Channel
		if yank == nil {
			yank = &l.Yanked
		}
	} else if *channel != "" {
		if err := validChannel(*channel); err != nil {
			return err
		}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	res, err := Sync(c, e.git(), Opts{Version: pos[0], Channel: *channel, Yanked: yank, SummaryOnly: *summaryOnly, DryRun: *dry})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s %s%s%s\n", pos[0], res.Action, dryNote(*dry), noteSuffix(res))
	return nil
}

func dryNote(dry bool) string {
	if dry {
		return " (dry-run)"
	}
	return ""
}

// parseChannelMap reads "v0.25.0=stable,v0.26.0=edge".
func parseChannelMap(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		v, ch, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, usageError{fmt.Sprintf("--channel entry %q must be vX.Y.Z=canary|edge|stable", part)}
		}
		if _, err := VersionSort(v); err != nil {
			return nil, usageError{err.Error()}
		}
		if err := validChannel(ch); err != nil {
			return nil, err
		}
		m[v] = ch
	}
	return m, nil
}

func cmdBackfill(args []string, stdout, stderr io.Writer, e env) error {
	fs := newFlags("backfill", stderr)
	channels := fs.String("channel", "", "per-version channels: v0.25.0=stable,v0.26.0=edge")
	dry := fs.Bool("dry-run", false, "read only; print what would happen")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageError{"backfill takes no arguments\n" + usage}
	}
	cm, err := parseChannelMap(*channels)
	if err != nil {
		return err
	}
	g := e.git()
	tags, err := SemverTags(g)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	var missing []string
	for _, t := range tags {
		have[t] = true
		if cm[t] == "" {
			missing = append(missing, t)
		}
	}
	for v := range cm {
		if !have[v] {
			return usageError{fmt.Sprintf("--channel names %s, which is not a tag", v)}
		}
	}
	if len(missing) > 0 {
		return usageError{"--channel has no entry for: " + strings.Join(missing, ", ")}
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	for _, t := range tags {
		_, hasPrev := PrevTag(g, t)
		res, err := Sync(c, g, Opts{Version: t, Channel: cm[t], SummaryOnly: !hasPrev, DryRun: *dry})
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		fmt.Fprintf(stdout, "%s %s%s%s\n", t, res.Action, dryNote(*dry), noteSuffix(res))
	}
	return nil
}

// cmdRefresh brings every released version in line with the live manifests:
// it creates missing entries and sets each entry's channel and yanked mark.
// Versions not live on any channel yet are skipped. Notes follow the usual
// rule (regenerated only while unedited).
func cmdRefresh(args []string, stdout, stderr io.Writer, e env) error {
	fs := newFlags("refresh", stderr)
	dry := fs.Bool("dry-run", false, "read only; print what would happen")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageError{"refresh takes no arguments\n" + usage}
	}
	g := e.git()
	tags, err := SemverTags(g)
	if err != nil {
		return err
	}
	live, err := e.live()
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	var failed []string
	for _, t := range tags {
		l, ok := live[t]
		if !ok || l.Channel == "" {
			fmt.Fprintf(stdout, "%s skip (not live on any channel, or halted)\n", t)
			continue
		}
		y := l.Yanked
		_, hasPrev := PrevTag(g, t)
		res, err := Sync(c, g, Opts{Version: t, Channel: l.Channel, Yanked: &y, SummaryOnly: !hasPrev, DryRun: *dry})
		if err != nil {
			// One bad version must not block the badges of the others.
			fmt.Fprintf(stderr, "changelog: %s: %v\n", t, err)
			failed = append(failed, t)
			continue
		}
		fmt.Fprintf(stdout, "%s %s [%s%s]%s%s\n", t, res.Action, l.Channel, yankedNote(l.Yanked), dryNote(*dry), noteSuffix(res))
	}
	if len(failed) > 0 {
		return fmt.Errorf("refresh failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// cmdNotes prints what the generator would write, without touching the network.
func cmdNotes(args []string, stdout, stderr io.Writer, e env) error {
	pos, err := parseFlags(newFlags("notes", stderr), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{"notes takes exactly one version\n" + usage}
	}
	n, err := generate(e.git(), Opts{Version: pos[0]})
	if err != nil {
		return err
	}
	for _, s := range []struct {
		title string
		items []string
	}{{"Features", n.Features}, {"Fixes", n.Fixes}, {"Docs", n.Docs}, {"Chores", n.Chores}} {
		if len(s.items) == 0 {
			continue
		}
		fmt.Fprintln(stdout, s.title)
		for _, it := range s.items {
			fmt.Fprintln(stdout, "  -", it)
		}
	}
	return nil
}

func noteSuffix(r Result) string {
	if r.Note == "" {
		return ""
	}
	return " (" + r.Note + ")"
}

func yankedNote(y bool) string {
	if y {
		return ", yanked"
	}
	return ""
}
