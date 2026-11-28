package app

import (
	"context"
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
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/audit"
	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/analytics"
	"hatamoto.dev/core/internal/auditctx"
	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/auditstate"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/portability"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/reviewflow"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
)

// opsCommands are the first words routed to runOps.
var opsCommands = map[string]bool{"entity": true, "redirect": true, "analytics": true, "export": true, "import": true, "backup": true}

const opsUsage = `  entity list [--schema S] [--status S] [--search Q] [--limit N]
  entity get <id>
  entity create --schema S --name N [--slug SLUG] [--attributes JSON | --attributes-file F] [--publish] [--as EMAIL]
  entity publish <id> [--as EMAIL]            entity unpublish <id> [--as EMAIL]
  redirect list [--search Q]
  redirect create --from /old --to /new [--status 301]
  redirect delete <id>
  analytics crawlers [--from D] [--to D] [--limit N]     AI-crawler fetches per page and agent
  analytics first-crawls [--from D] [--to D] [--limit N]
  analytics referrals [--from D] [--to D]                AI-assistant visits per day (D = YYYY-MM-DD)
  export <file.tar.gz> [--no-submissions]   full-site archive (see docs/EXPORT.md); "-" = stdout
  import <file.tar.gz> [--force]            into an empty database only; users need reset-password; --force: different server secret, drops commenter emails
  import wordpress (--url URL --user U --app-password P | --wxr FILE) [--scan-only] [--resume RUN] [--audit-url URL]
                                            import a WordPress site (docs/IMPORT-WORDPRESS.md)
  backup create [--config P]                full backup into $HATAMOTO_DATA/backups (needs DATABASE_URL and pg_dump); holds secrets
  backup list [--json] [--remote]           backups on disk, or in the off-site bucket with --remote (no database needed)
  backup delete <id>                        remove one backup
  backup restore <id|path.tar> [--dry-run] --admin-url DSN [--config-out P] [--force] [--identity F | --passphrase-stdin]   rebuild a clean machine from a backup
  backup restore --from-offsite <id> ...    same, downloading the backup from the off-site bucket (target in HATAMOTO_OFFSITE_*)`

// runOps runs the entity, redirect, analytics, export and import commands
// against DATABASE_URL, over the same services the API and MCP server use.
func runOps(ctx context.Context, args []string, _ *slog.Logger, out io.Writer) error {
	if args[0] == "backup" { // list and delete must work with the database down
		return runBackup(ctx, args[1:], out)
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Services log at info; keep stdout for the command's own output.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	switch args[0] {
	case "export", "import":
		if args[0] == "import" && len(args) > 1 && args[1] == "wordpress" {
			return runImportWordPress(ctx, db, cfg, args[2:], logger, out)
		}
		store, err := media.NewStoreFromEnv(os.Getenv, cfg.Server.MediaBaseURL)
		if err != nil {
			return err
		}
		secret, err := serverSecret()
		if err != nil {
			if args[0] == "import" {
				return fmt.Errorf("read the server secret: %w", err)
			}
			// An export is still useful without a check, as in the backup engine.
			fmt.Fprintf(os.Stderr, "warning: cannot read the server secret (%v); exporting without a secret check\n", err)
			secret = ""
		}
		if args[0] == "export" {
			return runExport(ctx, db, cfg.Server.MediaUploadDir, store, secret, args[1:], out)
		}
		return runImport(ctx, db, cfg.Server.MediaUploadDir, store, secret, args[1:], logger, out)
	case "entity":
		return runEntity(ctx, db, args[1:], out, logger)
	case "redirect":
		return runRedirect(ctx, db, args[1:], out, logger)
	case "analytics":
		return runAnalytics(ctx, db, args[1:], out)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func printJSON(out io.Writer, v any) error {
	b, err := jsonx.Indent(v)
	if err != nil {
		return err
	}
	_, err = out.Write(append(b, '\n'))
	return err
}

// flagSet parses args and returns the positional ones (flags may follow them).
func flagSet(name string, args []string, define func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	define(fs)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func runEntity(ctx context.Context, db *database.DB, args []string, out io.Writer, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: hatamoto entity list|get|create|publish|unpublish")
	}
	repo := entity.NewPostgresEntityRepository(db)
	repo.SetAuditTx(auditlog.TxHook(logger))
	svc := entity.NewService(entity.NewPostgresSchemaRepository(db), repo, logger)
	// A locale-neutral type defaults to all languages on a multi-language site.
	sites := siteconfig.NewStore(db, logger)
	svc.URLRegistry().SetLocales(sites.LocaleSource())
	setReviewGate(db, svc, sites, logger)
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		var schema, status, search string
		var limit int
		if _, err := flagSet("entity list", rest, func(fs *flag.FlagSet) {
			fs.StringVar(&schema, "schema", "", "schema slug")
			fs.StringVar(&status, "status", "", "draft, scheduled, published or archived")
			fs.StringVar(&search, "search", "", "name contains")
			fs.IntVar(&limit, "limit", 50, "rows")
		}); err != nil {
			return err
		}
		f := &entity.ListEntitiesFilter{Search: search}
		if schema != "" {
			sc, err := svc.GetSchemaBySlug(ctx, schema)
			if err != nil {
				return err
			}
			f.SchemaID = &sc.ID
		}
		if status != "" {
			st, err := entity.ParseStatus(status)
			if err != nil {
				return err
			}
			f.Status = &st
		}
		res, err := svc.ListEntitiesWithOffset(ctx, f, &entity.OffsetPage{Page: 1, Limit: max(1, min(limit, 100))})
		if err != nil {
			return err
		}
		schemaSlugs := map[uuid.UUID]string{}
		if all, err := svc.ListSchemas(ctx); err == nil {
			for _, sc := range all {
				schemaSlugs[sc.ID] = sc.Slug
			}
		}
		for _, e := range res.Items {
			schemaSlug := schemaSlugs[e.SchemaID]
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Status, schemaSlug, e.Slug, e.Name)
		}
		return nil
	case "get", "publish", "unpublish":
		var as string
		pos, err := flagSet("entity "+sub, rest, func(fs *flag.FlagSet) {
			fs.StringVar(&as, "as", "", "attribute the change to this user (email); their role applies")
		})
		if err != nil || len(pos) != 1 {
			return fmt.Errorf("usage: hatamoto entity %s <id> [--as EMAIL]", sub)
		}
		ctx, actor, err := resolveActor(ctx, db, as)
		if err != nil {
			return err
		}
		id, err := uuid.Parse(pos[0])
		if err != nil {
			return fmt.Errorf("%q is not an entity id", pos[0])
		}
		if sub != "get" {
			ctx = auditctx.With(ctx, cliActor(as, actor))
		}
		var e *entity.Entity
		switch sub {
		case "get":
			e, err = svc.GetEntity(ctx, id)
		case "publish":
			e, err = svc.UpdateEntityStatus(ctx, id, actor, entity.StatusPublished)
		default:
			e, err = svc.UpdateEntityStatus(ctx, id, actor, entity.StatusDraft)
		}
		if sentForReview(out, err) {
			return nil
		}
		if err != nil {
			return err
		}
		return printJSON(out, e)
	case "create":
		var schema, name, slug, attrs, attrsFile, as string
		var publish bool
		if _, err := flagSet("entity create", rest, func(fs *flag.FlagSet) {
			fs.StringVar(&schema, "schema", "", "schema slug (required)")
			fs.StringVar(&name, "name", "", "name (required)")
			fs.StringVar(&slug, "slug", "", "slug (default: from the name)")
			fs.StringVar(&attrs, "attributes", "", "attributes as a JSON object")
			fs.StringVar(&attrsFile, "attributes-file", "", "file with the attributes JSON object")
			fs.BoolVar(&publish, "publish", false, "publish right away (default: draft)")
			fs.StringVar(&as, "as", "", "attribute the change to this user (email); their role applies")
		}); err != nil {
			return err
		}
		ctx, actor, err := resolveActor(ctx, db, as)
		if err != nil {
			return err
		}
		if schema == "" || name == "" {
			return errors.New("--schema and --name are required")
		}
		ctx = auditctx.With(ctx, cliActor(as, actor))
		if attrsFile != "" {
			b, err := os.ReadFile(attrsFile)
			if err != nil {
				return err
			}
			attrs = string(b)
		}
		if strings.TrimSpace(attrs) == "" {
			attrs = "{}"
		}
		if !jsonx.Valid([]byte(attrs)) {
			return errors.New("attributes are not valid JSON (duplicate keys are rejected)")
		}
		sc, err := svc.GetSchemaBySlug(ctx, schema)
		if err != nil {
			return err
		}
		e, err := svc.CreateEntity(ctx, actor, &entity.CreateEntityInput{SchemaID: sc.ID, Name: name, Slug: slug, Attributes: []byte(attrs)})
		if err != nil {
			return err
		}
		if publish {
			pub, err := svc.UpdateEntityStatus(ctx, e.ID, actor, entity.StatusPublished)
			if sentForReview(io.Discard, err) {
				// The entry is created as a draft; its publish waits for a reviewer.
				if err := printJSON(out, e); err != nil {
					return err
				}
				sentForReview(out, err)
				return nil
			}
			if err != nil {
				return err
			}
			e = pub
		}
		return printJSON(out, e)
	}
	return fmt.Errorf("unknown entity command %q", sub)
}

// setReviewGate holds a change made --as a user for review like every other
// surface does: the entity service asks the gate only when a user is on the
// context, so a CLI call without --as (the system) is never held.
func setReviewGate(db *database.DB, svc *entity.Service, sites *siteconfig.Store, logger *slog.Logger) {
	hook := auditlog.TxHook(logger)
	// Only entity items are staged here, so the entity writer is the only one needed.
	eng := &changeset.Engine{DB: db, Logger: logger,
		Writers: map[changeset.Kind]changeset.KindWriter{changeset.KindEntity: changeset.EntityWriter{Svc: svc}},
		AuditTx: func(ctx context.Context, tx pgx.Tx, action, target string, detail map[string]any) error {
			ev := auditstate.Event{Actor: "cli", ActorKind: audit.KindCLI, Action: action, Target: target, Detail: detail}
			if a, ok := auditctx.From(ctx); ok {
				ev.ActorID, ev.Actor, ev.ActorKind, ev.KeyID = a.ID, a.Name, a.Kind, a.KeyID
			}
			return hook(ctx, tx, ev)
		}}
	svc.SetReviewGate(&reviewflow.Flow{Switch: sites, Engine: eng, Users: user.NewPostgresRepository(db), Logger: logger})
}

// sentForReview reports whether err is the outcome of a change that was staged
// for review, and says so. It is a success: the command exits 0.
func sentForReview(out io.Writer, err error) bool {
	var sent *entity.SentForReviewError
	if !errors.As(err, &sent) {
		return false
	}
	fmt.Fprintf(out, "Sent for review (change %d). It goes live when a reviewer approves it.\n", sent.ChangesetID)
	return true
}

// cliActor is the principal behind a CLI entity write: the --as user, or the
// plain "cli" actor when the change is unattributed.
func cliActor(as string, id *uuid.UUID) auditctx.Actor {
	a := auditctx.Actor{Name: "cli", Kind: audit.KindCLI, Via: "cli"}
	if id != nil {
		a.ID = id.String()
		a.Name = strings.TrimSpace(as)
	}
	return a
}

// resolveActor looks up the user a CLI change is made as. Empty means the
// system (unattributed, NULL), as before; a user's changes are checked against
// their role, through the returned context.
func resolveActor(ctx context.Context, db *database.DB, email string) (context.Context, *uuid.UUID, error) {
	if strings.TrimSpace(email) == "" {
		return ctx, nil, nil
	}
	u, err := user.NewPostgresRepository(db).GetByEmail(ctx, strings.TrimSpace(email))
	if err != nil || u == nil {
		return ctx, nil, fmt.Errorf("--as: no user with email %q", email)
	}
	return authzctx.With(ctx, authzsubject.ForSession(u.ID.String(), string(u.Role))), &u.ID, nil
}

func runRedirect(ctx context.Context, db *database.DB, args []string, out io.Writer, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: hatamoto redirect list|create|delete")
	}
	svc := redirect.NewService(db, logger)
	defer svc.Close()
	svc.SetAudit(func(ctx context.Context, action, target string, detail map[string]any) {
		if detail == nil {
			detail = map[string]any{}
		}
		detail["via"] = "cli"
		auditCLI(ctx, db.Pool(), logger, action, target, detail)
	})
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		var search string
		if _, err := flagSet("redirect list", rest, func(fs *flag.FlagSet) { fs.StringVar(&search, "search", "", "text in source or target") }); err != nil {
			return err
		}
		items, total, err := svc.ListRedirects(ctx, redirect.RedirectListParams{Search: search, Page: 1, PerPage: 200})
		if err != nil {
			return err
		}
		for _, r := range items {
			fmt.Fprintf(out, "%s\t%d\t%s -> %s\n", r.ID, r.StatusCode, r.SourcePath, r.TargetURL)
		}
		if total > len(items) {
			fmt.Fprintf(out, "(%d more not shown; narrow with --search)\n", total-len(items))
		}
		return nil
	case "create":
		var from, to string
		status := 301
		if _, err := flagSet("redirect create", rest, func(fs *flag.FlagSet) {
			fs.StringVar(&from, "from", "", "source path (required)")
			fs.StringVar(&to, "to", "", "target URL (required)")
			fs.IntVar(&status, "status", 301, "301, 302, 307 or 308")
		}); err != nil {
			return err
		}
		if from == "" || to == "" {
			return errors.New("--from and --to are required")
		}
		r, err := svc.CreateRedirect(ctx, &redirect.CreateRedirectInput{SourcePath: from, TargetURL: to, StatusCode: status})
		if err != nil {
			return err
		}
		return printJSON(out, r)
	case "delete":
		pos, err := flagSet("redirect delete", rest, func(*flag.FlagSet) {})
		if err != nil || len(pos) != 1 {
			return errors.New("usage: hatamoto redirect delete <id>")
		}
		id, err := uuid.Parse(pos[0])
		if err != nil {
			return fmt.Errorf("%q is not a redirect id", pos[0])
		}
		if err := svc.DeleteRedirect(ctx, id); err != nil {
			return err
		}
		fmt.Fprintln(out, "deleted", id)
		return nil
	}
	return fmt.Errorf("unknown redirect command %q", sub)
}

func runAnalytics(ctx context.Context, db *database.DB, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hatamoto analytics crawlers|first-crawls|referrals")
	}
	var from, to string
	var limit int
	if _, err := flagSet("analytics "+args[0], args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&from, "from", "", "first day, YYYY-MM-DD (default: 29 days before --to)")
		fs.StringVar(&to, "to", "", "last day, YYYY-MM-DD (default: today, UTC)")
		fs.IntVar(&limit, "limit", 20, "rows")
	}); err != nil {
		return err
	}
	rg, err := parseDayRange(from, to)
	if err != nil {
		return err
	}
	stats := agents.NewStatsRepository(db)
	page := agents.Page{Limit: max(1, min(limit, 100))}
	switch args[0] {
	case "crawlers":
		pages, _, err := stats.TopPages(ctx, rg, page)
		if err != nil {
			return err
		}
		byAgent, err := stats.Agents(ctx, rg)
		if err != nil {
			return err
		}
		return printJSON(out, map[string]any{"pages": pages, "agents": byAgent})
	case "first-crawls":
		rows, _, err := stats.FirstCrawls(ctx, rg, page)
		if err != nil {
			return err
		}
		return printJSON(out, rows)
	case "referrals":
		days, err := analytics.NewReader(db).AIReferrals(ctx, rg.From, rg.To)
		if err != nil {
			return err
		}
		return printJSON(out, days)
	}
	return fmt.Errorf("unknown analytics command %q", args[0])
}

func parseDayRange(from, to string) (agents.Range, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	rg := agents.Range{From: today.AddDate(0, 0, -29), To: today}
	var err error
	if to != "" {
		if rg.To, err = time.Parse("2006-01-02", to); err != nil {
			return rg, errors.New("--to must be YYYY-MM-DD")
		}
		rg.From = rg.To.AddDate(0, 0, -29)
	}
	if from != "" {
		if rg.From, err = time.Parse("2006-01-02", from); err != nil {
			return rg, errors.New("--from must be YYYY-MM-DD")
		}
	}
	if rg.From.After(rg.To) {
		return rg, errors.New("--from is after --to")
	}
	return rg, nil
}

func mediaDirOr(dir string) string {
	if dir == "" {
		return "./uploads"
	}
	return dir
}

func runExport(ctx context.Context, db *database.DB, mediaDir string, store media.BlobStore, secret string, args []string, out io.Writer) error {
	var noSub bool
	pos, err := flagSet("export", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&noSub, "no-submissions", false, "leave out form submissions (visitor data)")
	})
	if err != nil || len(pos) != 1 {
		return errors.New("usage: hatamoto export <file.tar.gz> [--no-submissions]")
	}
	opts := portability.ExportOptions{MediaDir: mediaDirOr(mediaDir), Store: store, NoSubmissions: noSub, Secret: secret}
	if pos[0] == "-" {
		_, err := portability.Export(ctx, db.Pool(), os.Stdout, opts)
		return err
	}
	// Write beside the target and rename, so a failed export never leaves a
	// half archive under the final name.
	tmp, err := os.CreateTemp(filepath.Dir(pos[0]), ".export-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	man, err := portability.Export(ctx, db.Pool(), tmp, opts)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), pos[0]); err != nil {
		return err
	}
	var rows int64
	for _, n := range man.Rows {
		rows += n
	}
	fmt.Fprintf(out, "exported %d rows in %d tables and %d media files (%d bytes) to %s\n", rows, len(man.Rows), man.MediaFiles, man.MediaBytes, pos[0])
	return nil
}

func runImport(ctx context.Context, db *database.DB, mediaDir string, store media.BlobStore, secret string, args []string, logger *slog.Logger, out io.Writer) error {
	var force bool
	pos, err := flagSet("import", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&force, "force", false, "import an archive made with a different server secret and drop its unreadable commenter emails")
	})
	if err != nil || len(pos) != 1 {
		return errors.New("usage: hatamoto import <file.tar.gz> [--force]")
	}
	f := os.Stdin
	if pos[0] != "-" {
		if f, err = os.Open(pos[0]); err != nil {
			return err
		}
		defer f.Close()
	}
	// A new database may not be migrated yet.
	if err := migrateUpDirect(ctx, db, logger); err != nil {
		return err
	}
	res, err := portability.Import(ctx, db.Pool(), f, portability.ImportOptions{MediaDir: mediaDirOr(mediaDir), Store: store, Secret: secret, Force: force})
	if err != nil {
		return err
	}
	var rows int64
	for _, n := range res.Rows {
		rows += n
	}
	skipped := len(res.Warnings) // each warning is one skipped media file
	fmt.Fprintf(out, "imported %d rows in %d tables and %d media files", rows, len(res.Rows), res.MediaFiles-skipped)
	if skipped > 0 {
		fmt.Fprintf(out, " (%d skipped)", skipped)
	}
	fmt.Fprintln(out)
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	for _, n := range res.Notices {
		fmt.Fprintf(out, "warning: %s\n", n)
	}
	if res.Users > 0 {
		fmt.Fprintf(out, "%d users imported without passwords: set one with `hatamoto user reset-password --email <email>`\n", res.Users)
	}
	return nil
}
