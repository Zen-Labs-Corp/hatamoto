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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/sdk"
)

// The commands below let an installer script finish a site without the admin:
// activate a theme, mint an API key, set plugin settings, see what is
// installed. Each does what its admin screen does, through the same services.
// They write the database; a running server picks the change up on its own
// (see docs/CLI.md).

const installOpsUsage = `  theme activate <name|default> [--force]  make an installed theme live (same checks as Admin > Themes; --force skips the missing-sections check)
  apikey create --name N --scopes a,b [--out FILE] [--as EMAIL] [--description D] [--rate-limit N]
                                           create an API key; the key goes to FILE (mode 0600, never overwritten) or, without --out, to stdout once
  plugin settings set <plugin> key=value...   (key=@FILE or key=- reads the value from a file or stdin, so a secret stays off the command line)
                                           validate against the plugin's settings schema and save (other settings and write-only values are kept)
  package list                             installed plugins and themes`

// runInstallOps runs the installer commands; ok is false when args are not one
// of them.
func runInstallOps(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) (ok bool, err error) {
	switch {
	case args[0] == "theme" && len(args) > 1 && args[1] == "activate":
		return true, runThemeActivate(ctx, args[2:], logger, out)
	case args[0] == "apikey" && len(args) > 1 && args[1] == "create":
		return true, runAPIKeyCreate(ctx, args[2:], logger, out, os.Stderr)
	case args[0] == "plugin" && len(args) > 2 && args[1] == "settings" && args[2] == "set":
		return true, runPluginSettingsSet(ctx, args[3:], os.Stdin, out)
	case args[0] == "package" && len(args) > 1 && args[1] == "list":
		return true, runPackageList(ctx, args[2:], out)
	}
	return false, nil
}

func runThemeActivate(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	var force bool
	pos, err := flagSet("theme activate", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&force, "force", false, "activate even when the content uses section types the theme lacks")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: hatamoto theme activate <name|default> [--force]")
	}
	m, db, _, _, err := packageManager(ctx, logger, false)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx = packages.WithActor(ctx, packages.Actor{Email: "cli"})
	if err := m.Activate(ctx, pos[0], force); err != nil {
		var missing *packages.MissingSectionsError
		if errors.As(err, &missing) {
			var b strings.Builder
			for _, ms := range missing.Missing {
				fmt.Fprintf(&b, "\n  %s (%d use(s))", ms.Type, ms.Uses)
			}
			return fmt.Errorf("%w:%s\nre-run with --force to activate it anyway", err, b.String())
		}
		return err
	}
	fmt.Fprintf(out, "theme %s is now live\n", pos[0])
	fmt.Fprintln(out, "a running server applies it within a few seconds; no restart is needed")
	return nil
}

// auditCLI writes one hatamoto_admin_audit row for a change made from the
// CLI. The detail must never hold a secret.
func auditCLI(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, action, target string, detail map[string]any) {
	raw, _ := json.Marshal(detail)
	if _, err := pool.Exec(context.WithoutCancel(ctx),
		`INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES (NULL, 'cli', $1, $2, $3)`,
		action, target, raw); err != nil {
		logger.Error("audit write failed", "action", action, "error", err)
	}
}

func runAPIKeyCreate(ctx context.Context, args []string, logger *slog.Logger, out, info io.Writer) error {
	var name, scopes, outFile, as, desc string
	var rate int
	pos, err := flagSet("apikey create", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "key name (required)")
		fs.StringVar(&scopes, "scopes", "", "comma separated scopes, e.g. read:entities,write:entities (required)")
		fs.StringVar(&outFile, "out", "", "write the key to this new file (mode 0600) instead of stdout")
		fs.StringVar(&as, "as", "", "email of the user the key acts as (default: the oldest owner)")
		fs.StringVar(&desc, "description", "", "what the key is for")
		fs.IntVar(&rate, "rate-limit", 0, "requests per minute (default as in the admin)")
	})
	if err != nil {
		return err
	}
	if len(pos) != 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(scopes) == "" {
		return errors.New("usage: hatamoto apikey create --name N --scopes a,b [--out FILE] [--as EMAIL]")
	}
	var scopeList []auth.APIKeyScope
	for _, s := range strings.Split(scopes, ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopeList = append(scopeList, auth.APIKeyScope(s))
		}
	}
	if len(scopeList) == 0 {
		return errors.New("--scopes names no scope (e.g. read:entities,write:entities)")
	}
	for _, s := range scopeList {
		if !auth.IsValidScope(string(s)) {
			return fmt.Errorf("unknown scope %q (valid: %s)", s, validScopeList())
		}
	}

	// The file is claimed before the key exists: a key that cannot be
	// delivered must not be created.
	var f *os.File
	if outFile != "" {
		if f, err = os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err != nil {
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("%s exists; refusing to overwrite it", outFile)
			}
			return err
		}
		defer f.Close()
	}
	created := false
	defer func() {
		if f != nil && !created {
			os.Remove(outFile)
		}
	}()

	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	creator, err := apiKeyCreator(ctx, db, as)
	if err != nil {
		return err
	}
	key, err := auth.NewAPIKeyService(db, logger).Create(ctx, creator, &auth.CreateAPIKeyInput{
		Name: strings.TrimSpace(name), Description: desc, Scopes: scopeList, RateLimit: rate,
	})
	if err != nil {
		return err
	}
	// Audited before delivery: the key exists from here on, whatever happens to the file.
	auditCLI(ctx, db.Pool(), logger, "apikey.create", "apikey:"+key.ID.String(), map[string]any{"name": key.Name, "scopes": key.Scopes, "prefix": key.KeyPrefix})
	if f != nil {
		if _, err = f.WriteString(key.PlainKey + "\n"); err == nil {
			err = f.Close()
		}
		if err != nil {
			return fmt.Errorf("writing %s (the key %s was created; revoke it in Admin > API keys): %w", outFile, key.ID, err)
		}
	}
	created = true
	if f != nil {
		fmt.Fprintf(out, "created API key %s (%s, scopes %s)\nkey written to %s (mode 0600)\n", key.ID, key.Name, scopes, outFile)
		return nil
	}
	// Machine readable: the key alone on stdout; the rest is for the person.
	fmt.Fprintf(info, "created API key %s (%s, scopes %s); this is the only time it is shown\n", key.ID, key.Name, scopes)
	fmt.Fprintln(out, key.PlainKey)
	return nil
}

func validScopeList() string {
	var s []string
	for _, v := range auth.ValidScopes() {
		s = append(s, string(v))
	}
	return strings.Join(s, ", ")
}

// apiKeyCreator is the user the key acts as. A key acts with its creator's
// authority and a key without a creator can do nothing, so there must be one.
func apiKeyCreator(ctx context.Context, db *database.DB, email string) (*uuid.UUID, error) {
	if strings.TrimSpace(email) != "" {
		return resolveActor(ctx, db, email)
	}
	var id uuid.UUID
	err := db.Pool().QueryRow(ctx, `SELECT id FROM hatamoto_users WHERE role = 'owner' ORDER BY created_at, id LIMIT 1`).Scan(&id)
	if err != nil {
		return nil, errors.New("no owner to act as: create one with `hatamoto user create` or pass --as EMAIL")
	}
	return &id, nil
}

func runPluginSettingsSet(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) < 2 {
		//lint:ignore ST1005 the usage line is the same text as the help, which ends in "..."
		return errors.New("usage: hatamoto plugin settings set <plugin> key=value...")
	}
	plugin := args[0]
	pairs, err := resolveSettingSources(args[1:], stdin)
	if err != nil {
		return err
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	row, err := packages.NewStore(db.Pool()).Get(ctx, packages.KindPlugin, plugin)
	if err != nil {
		if errors.Is(err, packages.ErrNotFound) {
			return fmt.Errorf("plugin %q is not installed", plugin)
		}
		return err
	}
	var man sdk.Manifest
	if err := json.Unmarshal(row.Manifest, &man); err != nil {
		return err
	}
	if len(man.SettingsSchema) == 0 {
		return fmt.Errorf("plugin %q declares no settings", plugin)
	}
	store := pluginhost.NewSettingsStore(db.Pool())
	old, err := store.Get(ctx, plugin)
	if err != nil {
		return err
	}
	merged, keys, err := applySettingPairs(man.SettingsSchema, old, pairs)
	if err != nil {
		return err
	}
	compiled, err := pluginhost.CompileSchema(man.SettingsSchema)
	if err != nil {
		return err
	}
	if fields := pluginhost.Validate(compiled, pluginhost.MergeDefaults(man.SettingsSchema, merged)); len(fields) > 0 {
		var b strings.Builder
		for _, f := range fields {
			fmt.Fprintf(&b, "\n  %s: %s", f.Path, f.Message)
		}
		return fmt.Errorf("settings of %s are not valid:%s", plugin, b.String())
	}
	if err := store.Put(ctx, plugin, merged); err != nil {
		return err
	}
	// Key names only: a value may be a secret.
	auditCLI(ctx, db.Pool(), slog.New(slog.NewTextHandler(os.Stderr, nil)), "plugin.settings", "plugin:"+plugin, map[string]any{"keys": keys})
	fmt.Fprintf(out, "saved %s settings: %s\n", plugin, strings.Join(keys, ", "))
	return nil
}

// applySettingPairs sets key=value pairs over the stored settings. A value is
// read as the property's schema type (string stays text; number, integer,
// boolean, array and object are JSON); a property without a type is JSON when
// it parses, text otherwise. Write-only values the pairs do not mention are
// kept, as the admin does. It returns the new object and the keys it set.
func applySettingPairs(schema []byte, old json.RawMessage, pairs []string) (json.RawMessage, []string, error) {
	var props struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &props)
	obj := map[string]json.RawMessage{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &obj); err != nil || obj == nil {
			obj = map[string]json.RawMessage{}
		}
	}
	var keys []string
	for _, p := range pairs {
		k, v, found := strings.Cut(p, "=")
		if !found || k == "" {
			return nil, nil, fmt.Errorf("%q is not key=value", p)
		}
		prop, known := props.Properties[k]
		if !known && len(props.Properties) > 0 {
			return nil, nil, fmt.Errorf("the plugin has no setting %q (known: %s)", k, knownKeys(props.Properties))
		}
		raw, err := settingValue(prop.Type, v)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", k, err)
		}
		obj[k] = raw
		keys = append(keys, k)
	}
	body, _ := json.Marshal(obj)
	return pluginhost.PreserveWriteOnly(schema, old, body), keys, nil
}

// resolveSettingSources replaces key=@FILE with the file's content and
// key=- with stdin (at most one), so a secret need not be on the command line,
// where any local user can read it. One trailing newline is dropped.
func resolveSettingSources(pairs []string, stdin io.Reader) ([]string, error) {
	out := make([]string, len(pairs))
	usedStdin := false
	for i, p := range pairs {
		k, v, found := strings.Cut(p, "=")
		out[i] = p
		if found && strings.HasPrefix(v, "@@") {
			out[i] = k + "=" + v[1:] // @@x is the literal value @x
			continue
		}
		if !found || (v != "-" && !strings.HasPrefix(v, "@")) {
			continue
		}
		var b []byte
		var err error
		if v == "-" {
			if usedStdin {
				return nil, errors.New("only one key can be read from stdin (key=-)")
			}
			usedStdin = true
			b, err = io.ReadAll(io.LimitReader(stdin, 1<<20))
		} else {
			b, err = readCapped(v[1:])
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[i] = k + "=" + strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	}
	return out, nil
}

// readCapped reads a file of at most 1 MiB, the limit stdin has.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err == nil && len(b) > 1<<20 {
		err = errors.New("file is larger than 1 MiB")
	}
	return b, err
}

func knownKeys[T any](m map[string]T) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// settingValue reads v as JSON of the schema type typ ("string", "integer",
// ...; a list of types or none counts as untyped).
func settingValue(typ any, v string) (json.RawMessage, error) {
	t, _ := typ.(string)
	switch t {
	case "string":
		b, _ := json.Marshal(v)
		return b, nil
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return nil, fmt.Errorf("%q is not an integer", v)
		}
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", v)
		}
	case "boolean":
		if v != "true" && v != "false" {
			return nil, fmt.Errorf("%q is not true or false", v)
		}
	case "array", "object":
		if !json.Valid([]byte(v)) {
			return nil, fmt.Errorf("%q is not valid JSON", v)
		}
	default:
		if !json.Valid([]byte(v)) {
			b, _ := json.Marshal(v)
			return b, nil
		}
	}
	return json.RawMessage(v), nil
}

func runPackageList(ctx context.Context, args []string, out io.Writer) error {
	var asJSON bool
	pos, err := flagSet("package list", args, func(fs *flag.FlagSet) { fs.BoolVar(&asJSON, "json", false, "print JSON") })
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return errors.New("usage: hatamoto package list [--json]")
	}
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	st := packages.NewStore(db.Pool())
	rows, err := st.List(ctx)
	if err != nil {
		return err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return err
	}
	type item struct {
		Kind      string    `json:"kind"`
		Name      string    `json:"name"`
		Version   string    `json:"version"`
		Enabled   bool      `json:"enabled"`
		Active    bool      `json:"active,omitempty"`
		Signed    bool      `json:"signed"`
		Signer    string    `json:"signer,omitempty"`
		Source    string    `json:"source"`
		State     string    `json:"state"`
		Installed time.Time `json:"installed_at"`
	}
	items := make([]item, 0, len(rows))
	for _, r := range rows {
		items = append(items, item{Kind: string(r.Kind), Name: r.Name, Version: r.Version, Enabled: r.Enabled,
			Active: r.Kind == packages.KindTheme && r.Name == cfg.ActiveTheme, Signed: r.Signed(), Signer: r.SignerKeyID,
			Source: string(r.Source), State: r.State, Installed: r.InstalledAt})
	}
	if asJSON {
		return printJSON(out, map[string]any{"packages": items, "active_theme": cfg.ActiveTheme, "allow_unsigned": cfg.AllowUnsigned})
	}
	for _, it := range items {
		flags := []string{it.Source}
		if it.Kind == "theme" && it.Active {
			flags = append(flags, "active")
		}
		if it.Kind == "plugin" && !it.Enabled {
			flags = append(flags, "disabled")
		}
		if it.Signed {
			flags = append(flags, "signed "+it.Signer)
		} else {
			flags = append(flags, "unsigned")
		}
		if it.State != packages.StateInstalled {
			flags = append(flags, it.State)
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", it.Kind, it.Name, it.Version, strings.Join(flags, ", "))
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "no packages installed")
	}
	return nil
}
