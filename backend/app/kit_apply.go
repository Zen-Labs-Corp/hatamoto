package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/auditlog"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/server"
	"hatamoto.dev/core/internal/siteconfig"
)

// kitDeps wires the real services for the CLI. The address is the one the
// owner or operator set (the configured public base URL, else the one in
// Settings): a request-derived address is never used here.
func kitDeps(ctx context.Context, m *packages.Manager, db *database.DB, logger *slog.Logger) (kit.Deps, *comment.Service, error) {
	cfg, err := config.Load()
	if err != nil {
		return kit.Deps{}, nil, err
	}
	sites := siteconfig.NewStore(db, logger)
	repo := entity.NewPostgresEntityRepository(db)
	repo.SetAuditTx(auditlog.TxHook(logger))
	ents := entity.NewService(entity.NewPostgresSchemaRepository(db), repo, logger)
	ents.URLRegistry().SetLocales(sites.LocaleSource())
	secret, _ := serverSecret() // only the commenter keys use it; kits never read them
	comments := comment.New(db, secret, logger)
	address := func(ctx context.Context) string {
		if b := strings.TrimRight(strings.TrimSpace(cfg.PublicSite.BaseURL), "/"); b != "" {
			return b
		}
		if sc, err := sites.Get(ctx); err == nil {
			return strings.TrimRight(strings.TrimSpace(sc.PublicBaseURL), "/")
		}
		return ""
	}
	return kit.Wire(kit.Services{
		Manager: m, Entities: ents, Menus: menu.NewService(menu.NewPostgresRepository(db), logger),
		Site: sites, Comments: comments, SchemaComments: server.KitSchemaComments(comments, db, ents), Pool: db.Pool(), Address: address, Log: logger,
	}), comments, nil
}

// fetchKit resolves the argument: a registry name, an https URL or a file.
func fetchKit(ctx context.Context, m *packages.Manager, arg string, dev bool) ([]byte, packages.Source, error) {
	switch {
	case strings.HasPrefix(arg, "https://") || strings.HasPrefix(arg, "http://"):
		data, err := packages.FetchURL(ctx, &http.Client{Timeout: 2 * time.Minute}, arg, packages.DefaultLimits.Archive)
		return data, sourceFor(packages.SourceURL, dev), err
	case isPackageName(arg):
		data, err := m.FetchFromRegistry(ctx, packages.KindKit, arg)
		if errors.Is(err, packages.ErrNotListed) {
			return nil, "", fmt.Errorf("the registry has no kit %q", arg)
		}
		return data, packages.SourceRegistry, err
	}
	data, err := readBounded(arg, packages.DefaultLimits.Archive)
	return data, sourceFor(packages.SourceUpload, dev), err
}

func sourceFor(src packages.Source, dev bool) packages.Source {
	if dev {
		return packages.SourceDev
	}
	return src
}

// splitList reads a comma separated flag value.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runKitApply(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	var grant string
	var accept, activate, homepage, dry, dev bool
	pos, err := flagSet("kit apply", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&accept, "accept-comment-warnings", false, "turn comments (and the community policy) on after reading the warnings printed")
		fs.StringVar(&grant, "grant", "", "permissions to grant beyond what the kit grants itself: comma separated (site.scripts and ui.allow:* only when named here)")
		fs.BoolVar(&activate, "activate-theme", false, "make the kit's theme the active one (implied on a fresh site)")
		fs.BoolVar(&homepage, "homepage", false, "make the kit's home page the home page (implied on a fresh site)")
		fs.BoolVar(&dry, "dry-run", false, "print what would happen and change nothing")
		fs.BoolVar(&dev, "dev", false, "allow an unsigned dev kit (never in production)")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: hatamoto kit apply <name|file|url> [--accept-comment-warnings] [--grant P,P] [--activate-theme] [--homepage] [--dry-run] [--dev]")
	}
	m, db, _, why, err := packageManager(ctx, logger, dev)
	if err != nil {
		return err
	}
	defer db.Close()
	if dev && why != "" {
		return fmt.Errorf("%w: %s", packages.ErrDevDisabled, why)
	}
	ctx = packages.WithActor(ctx, packages.Actor{Email: "cli"})
	d, comments, err := kitDeps(ctx, m, db, logger)
	if err != nil {
		return err
	}

	raw, src, err := fetchKit(ctx, m, pos[0], dev)
	if err != nil {
		return err
	}
	p, origin, err := m.VerifyKit(ctx, raw, src)
	if err != nil {
		return err
	}
	pl, err := kit.BuildPlan(ctx, d, p, kit.Options{Origin: origin})
	if err != nil {
		return err
	}
	k := pl.Kit
	signed := "unsigned"
	if pl.Signer != "" {
		signed = fmt.Sprintf("%s (key %s)", pl.Tier, pl.Signer)
	}
	fmt.Fprintf(out, "kit %s %s  signed: %s\n", k.Name, k.Version, signed)
	printKitResolve(out, pl)
	if len(pl.Refusals) > 0 {
		for _, r := range pl.Refusals {
			fmt.Fprintf(out, "refused  %s\n", r)
		}
		return fmt.Errorf("kit %s was not applied: nothing was changed", k.Name)
	}

	wantComments := pl.Comments.Requested && !pl.Comments.AlreadyOn
	if wantComments {
		fmt.Fprintf(out, "warning  %s\nwarning  %s\n", pl.Comments.Warnings[0], pl.Comments.Warnings[1])
	}
	if pl.Policy.Available {
		fmt.Fprintf(out, "warning  %s\n", pl.Policy.Warning)
	}
	if (wantComments || pl.Policy.Available) && accept {
		fmt.Fprintln(out, "         accepted with --accept-comment-warnings")
	}
	if wantComments && accept && pl.Comments.NoAddress {
		fmt.Fprintf(out, "refused  %s\n", comment.NoAddressMsg)
		return fmt.Errorf("kit %s was not applied: %w", k.Name, kit.ErrNoAddress)
	}

	acc := kit.Accepts{
		Comments: accept, Policy: accept,
		ActivateTheme: activate || pl.Fresh, Homepage: homepage || pl.Fresh,
		Extra: pickExtra(pl, splitList(grant), out),
	}
	printKitPlan(out, pl, acc)
	if dry {
		fmt.Fprintln(out, "dry run: nothing was changed")
		return nil
	}

	opts := kit.ApplyOptions{Actor: "cli", EnableComments: func(ctx context.Context) error {
		set, err := comments.Settings(ctx)
		if err != nil {
			return err
		}
		was := set.Enabled
		set.Enabled = true
		if err := comments.SaveSettings(ctx, set, nil); err != nil {
			return err
		}
		auditCLI(ctx, db.Pool(), logger, "comments.enable", "comments", map[string]any{"old": was, "new": true, "via": "kit:" + k.Name})
		return nil
	}, PolicyOn: func(ctx context.Context, ch kit.PolicyChange) {
		auditCLI(ctx, db.Pool(), logger, "comments.policy", ch.SchemaID, map[string]any{"type": ch.Slug, "old": false, "new": true, "changed": []string{"policy"}, "via": "kit:" + k.Name})
	}}
	res, err := kit.Apply(ctx, d, pl, acc, opts)
	if err != nil {
		return err
	}
	for _, msg := range res.Messages {
		fmt.Fprintf(out, "note     %s\n", msg)
	}
	if wantComments && !accept {
		fmt.Fprintln(out, "comments left off: pass --accept-comment-warnings to turn them on")
	}
	if res.Total() == 0 && res.Filled == 0 && !res.Recorded {
		fmt.Fprintf(out, "kit %s %s is already applied: nothing to change (%d steps checked, 0 created)\n", k.Name, k.Version, res.Checked)
		return nil
	}
	created := map[string]int{}
	for what, n := range res.Created {
		created[what] = n
	}
	auditCLI(ctx, db.Pool(), logger, "kit.apply", "kit:"+k.Name, map[string]any{
		"version": k.Version, "created": created, "filled": res.Filled, "kept": len(res.Kept), "skipped": len(res.Skipped),
	})
	if res.Recorded {
		fmt.Fprintf(out, "recorded kit %s %s\n", k.Name, k.Version)
	}
	fmt.Fprintln(out, "applied. A running server loads the plugin within a few seconds; no restart is needed.")
	return nil
}

// pickExtra turns the --grant names into scoped owner ticks. "<plugin>:<perm>"
// grants one plugin; a bare name works only when exactly one plugin of the kit
// asks for it. A name no plugin offers, or a bare name several plugins ask
// for, is reported and dropped.
func pickExtra(pl *kit.Plan, want []string, out io.Writer) []string {
	offers := func(plugin, perm string) bool {
		return slices.ContainsFunc(pl.Plugins, func(st kit.PackageStep) bool {
			return st.Name == plugin && slices.Contains(st.Extra, perm)
		})
	}
	var picked []string
	for _, w := range want {
		if plugin, perm, ok := strings.Cut(w, ":"); ok && offers(plugin, perm) {
			picked = append(picked, w)
			continue
		}
		var owners []string
		for _, st := range pl.Plugins {
			if slices.Contains(st.Extra, w) {
				owners = append(owners, st.Name)
			}
		}
		switch len(owners) {
		case 0:
			fmt.Fprintf(out, "note     --grant %s ignored: no plugin of this kit asks for it\n", w)
		case 1:
			picked = append(picked, kit.ExtraKey(owners[0], w))
		default:
			fmt.Fprintf(out, "note     --grant %s ignored: %s all ask for it, name one as <plugin>:%s\n", w, strings.Join(owners, ", "), w)
		}
	}
	return picked
}

func printKitResolve(out io.Writer, pl *kit.Plan) {
	var parts []string
	for _, st := range pl.Plugins {
		parts = append(parts, fmt.Sprintf("plugin %s %s", st.Name, st.Version))
	}
	for _, st := range pl.Themes {
		parts = append(parts, fmt.Sprintf("theme %s %s", st.Name, st.Version))
	}
	if len(parts) > 0 {
		fmt.Fprintf(out, "resolve  %s ... ok\n", strings.Join(parts, ", "))
	}
}

func printKitPlan(out io.Writer, pl *kit.Plan, acc kit.Accepts) {
	for _, st := range pl.Plugins {
		if st.Action != kit.ActionInstall {
			fmt.Fprintf(out, "keep     plugin %s %s (already installed)\n", st.Name, st.Version)
			continue
		}
		granted := slices.Clone(st.Grant)
		var left []string
		for _, e := range st.Extra {
			if slices.Contains(acc.Extra, kit.ExtraKey(st.Name, e)) {
				granted = append(granted, e)
			} else {
				left = append(left, e)
			}
		}
		line := fmt.Sprintf("install  plugin %s %s", st.Name, st.Version)
		switch {
		case len(granted) > 0 && len(left) > 0:
			line += fmt.Sprintf(" (granted: %s; not granted: %s)", strings.Join(granted, ", "), strings.Join(left, ", "))
		case len(granted) > 0:
			line += fmt.Sprintf(" (granted: %s)", strings.Join(granted, ", "))
		case len(left) > 0:
			line += fmt.Sprintf(" (not granted: %s)", strings.Join(left, ", "))
		}
		fmt.Fprintln(out, line)
	}
	for _, st := range pl.Themes {
		verb := "install "
		if st.Action != kit.ActionInstall {
			verb = "keep    "
		}
		line := fmt.Sprintf("%s theme %s %s", verb, st.Name, st.Version)
		if st.Activate && acc.ActivateTheme {
			line += " (will be active)"
		}
		fmt.Fprintln(out, line)
	}
	for _, st := range pl.Schemas {
		printKitItem(out, "content type", st)
	}
	for _, st := range pl.Pages {
		printKitItem(out, "page", st)
	}
	for _, st := range pl.Menus {
		printKitItem(out, "menu", st)
	}
	if acc.Homepage && pl.Homepage.Page != "" {
		fmt.Fprintf(out, "switch   homepage %s\n", pl.Homepage.Page)
	}
	if pl.Comments.Requested && !pl.Comments.AlreadyOn && acc.Comments {
		fmt.Fprintln(out, "switch   comments on")
	}
	if len(pl.Policy.AlreadyOn) > 0 {
		fmt.Fprintf(out, "kept     community policy on %s (already on)\n", strings.Join(pl.Policy.AlreadyOn, ", "))
	}
	if pl.Policy.Available && acc.Policy {
		fmt.Fprintf(out, "switch   community policy on %s\n", strings.Join(pl.Policy.Requested, ", "))
	}
}

func printKitItem(out io.Writer, what string, st kit.ItemStep) {
	verb := string(st.Action)
	line := fmt.Sprintf("%-8s %s %s", verb, what, st.Name)
	if st.Note != "" {
		line += " (" + st.Note + ")"
	}
	fmt.Fprintln(out, line)
}

func runKitList(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	if pos, err := flagSet("kit list", args, func(*flag.FlagSet) {}); err != nil {
		return err
	} else if len(pos) != 0 {
		return errors.New("usage: hatamoto kit list")
	}
	m, db, _, _, err := packageManager(ctx, logger, false)
	if err != nil {
		return err
	}
	defer db.Close()
	d, _, err := kitDeps(ctx, m, db, logger)
	if err != nil {
		return err
	}
	rows, err := kit.AppliedStore{Pool: db.Pool()}.List(ctx)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	fmt.Fprintln(out, "applied")
	for _, a := range rows {
		applied[a.Name] = true
		fmt.Fprintf(out, "  %s  v%s  applied %s by %s\n", a.Name, a.Version, a.AppliedAt.Format("2006-01-02"), a.AppliedBy)
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	avail, err := kit.Available(packages.WithActor(ctx, packages.Actor{Email: "cli"}), d)
	if err != nil {
		fmt.Fprintln(out, "registry: unavailable")
		return nil
	}
	fmt.Fprintln(out, "available from the registry")
	for _, e := range avail {
		mark := ""
		if applied[e.Name] {
			mark = "  (applied)"
		}
		fmt.Fprintf(out, "  %s  v%s%s  %s\n", e.Name, e.Version, mark, e.Description)
	}
	if len(avail) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	return nil
}
