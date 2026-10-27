package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// packageCommands are the first words routed to runPackages.
var packageCommands = map[string]bool{"keys": true, "theme": true, "plugin": true, "package": true, "registry": true, "apikey": true}

const packagesUsage = `  keys generate [--name N] [--dir D]       make an ed25519 signing key (N.key secret, N.pub public)
  theme pack <dir> [--key F] [--out F]     build a .hmp theme package, signed with key F
  theme pack (--archive F --sha256 H | --package F) [--registry] [--expect-name N] [--screenshot-out D] [--key F] [--out F]
                                           the same from an author archive or a packed .hmp (registry builders)
  theme check <dir> | --archive F --sha256 H | --package F [--registry] [--expect-name N] [--screenshot-out D]
                                           validate a theme without a key (--registry: the directory rules)
  plugin pack <dir> [--key F] [--out F]    build a .hmp plugin package (the dir holds plugin.json and plugin.wasm and/or widgets/)
  theme dev <dir> --dev [--no-activate] [--confirm]
                                           push a theme directory as an unsigned dev package and again on every change (never in production)
  registry index <dir> --key F [--out F] [--url-prefix P] [--valid-days N] [--meta F]
                                           list the .hmp files in <dir> in a registry index signed with key F (docs/PACKAGES.md)
  registry page <registry.json> [--out F] [--title T] [--intro T] [--write-url U] [--submit-url U] [--install-hint T] [--trusted-key K]
                                           render the plugins of a signed registry index as one static HTML page
  package install <file|url> [--grant P,P|all] [--source upload|url|dev] [--dev] [--confirm]
                                           install a package (docs/PACKAGES.md); a running server applies it within seconds
` + installOpsUsage

// runPackages runs the key, pack and install commands. keys and the pack
// commands need no database.
func runPackages(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	if ok, err := runInstallOps(ctx, args, logger, out); ok {
		return err
	}
	switch {
	case args[0] == "keys" && len(args) > 1 && args[1] == "generate":
		return runKeysGenerate(args[2:], out)
	case args[0] == "theme" && len(args) > 1 && args[1] == "check":
		return runThemeCheck(args[2:], out)
	case (args[0] == "theme" || args[0] == "plugin") && len(args) > 1 && args[1] == "pack":
		return runPack(packages.Kind(args[0]), args[2:], out)
	case args[0] == "theme" && len(args) > 1 && args[1] == "dev":
		return runThemeDev(ctx, args[2:], logger, out)
	case args[0] == "registry" && len(args) > 1 && args[1] == "index":
		return runRegistryIndex(args[2:], out)
	case args[0] == "registry" && len(args) > 1 && args[1] == "page":
		return runRegistryPage(args[2:], out)
	case args[0] == "package" && len(args) > 1 && args[1] == "install":
		return runPackageInstall(ctx, args[2:], logger, out)
	}
	return fmt.Errorf("unknown command %q\n%s", strings.Join(args, " "), packagesUsage)
}

func runKeysGenerate(args []string, out io.Writer) error {
	var name, dir string
	if _, err := flagSet("keys generate", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "hatamoto-signing", "file name of the key pair")
		fs.StringVar(&dir, "dir", ".", "directory to write to")
	}); err != nil {
		return err
	}
	k, err := packages.GenerateKey()
	if err != nil {
		return err
	}
	priv, pub := filepath.Join(dir, name+".key"), filepath.Join(dir, name+".pub")
	secret, _ := json.MarshalIndent(k, "", "  ")
	public, _ := json.MarshalIndent(packages.Key{ID: k.ID, Public: k.Public}, "", "  ")
	if err := createNew(priv, append(secret, '\n'), 0o600); err != nil {
		return err
	}
	if err := createNew(pub, append(public, '\n'), 0o644); err != nil {
		os.Remove(priv) // do not leave half a key pair behind
		return err
	}
	fmt.Fprintf(out, "key id:      %s\nsecret key:  %s (keep it private; it signs your packages)\npublic key:  %s\n\nTo trust packages signed with it, set on the server:\n  HATAMOTO_TRUSTED_KEYS=%s\n", k.ID, priv, pub, k.Public)
	return nil
}

func runRegistryIndex(args []string, out io.Writer) error {
	var keyFile, outFile, prefix, metaFile string
	var days int
	pos, err := flagSet("registry index", args, func(fs *flag.FlagSet) {
		fs.StringVar(&keyFile, "key", os.Getenv("HATAMOTO_SIGNING_KEY"), "signing key file; env HATAMOTO_SIGNING_KEY")
		fs.StringVar(&outFile, "out", "registry.json", "output file")
		fs.StringVar(&prefix, "url-prefix", "packages/", "prefix of each package URL (relative to the index, or an https URL)")
		fs.IntVar(&days, "valid-days", int(packages.DefaultIndexValidity/(24*time.Hour)), "days the signed index stays valid (at most 180)")
		fs.StringVar(&metaFile, "meta", "", "JSON file of package name to author, source, commit and tier (docs/PACKAGES.md)")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 || keyFile == "" {
		return errors.New("usage: hatamoto registry index <dir> --key F [--out F] [--url-prefix P] [--valid-days N] [--meta F]")
	}
	if days < 1 || days > 180 {
		return fmt.Errorf("--valid-days must be 1 to 180, not %d", days)
	}
	key, err := loadKey(keyFile)
	if err != nil {
		return err
	}
	var opts packages.BuildOptions
	if metaFile != "" {
		raw, err := os.ReadFile(metaFile)
		if err != nil {
			return err
		}
		if opts.Meta, err = packages.ParseMeta(raw); err != nil {
			return fmt.Errorf("%s: %w", metaFile, err)
		}
	}
	doc, err := packages.BuildIndexWith(pos[0], prefix, *key, time.Now(), time.Duration(days)*24*time.Hour, opts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outFile, doc, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s signed by %s, valid %d days\n", outFile, key.ID, days)
	return nil
}

// createNew writes a file that must not exist yet (O_EXCL: no overwrite, no
// following a link planted between a check and the write).
func createNew(path string, b []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists; refusing to overwrite a key", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func loadKey(path string) (*packages.Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k packages.Key
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := k.PrivateKey(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &k, nil
}

func runPack(kind packages.Kind, args []string, out io.Writer) error {
	var keyFile, outFile string
	var tc themeCheckOpts
	pos, err := flagSet(string(kind)+" pack", args, func(fs *flag.FlagSet) {
		if kind == packages.KindTheme {
			tc.bind(fs)
		}
		fs.StringVar(&keyFile, "key", os.Getenv("HATAMOTO_SIGNING_KEY"), "signing key file (from `keys generate`); env HATAMOTO_SIGNING_KEY")
		fs.StringVar(&outFile, "out", "", "output file (default <name>-<version>.hmp)")
	})
	if err != nil {
		return err
	}
	themeSrc := kind == packages.KindTheme && (tc.src.archive != "" || tc.src.pkg != "" || tc.registry || tc.expectName != "" || tc.screenshotOut != "")
	if themeSrc {
		if err := tc.resolve(pos, fmt.Sprintf("usage: hatamoto %s pack <dir> [--key F] [--out F]", kind)); err != nil {
			return err
		}
	} else if len(pos) != 1 {
		return fmt.Errorf("usage: hatamoto %s pack <dir> [--key F] [--out F]", kind)
	}
	var key *packages.Key
	if keyFile != "" {
		if key, err = loadKey(keyFile); err != nil {
			return err
		}
	}
	var raw, shot []byte
	var meta *packages.ThemeMeta
	if themeSrc {
		files, err := buildThemeFiles(tc.src)
		if err != nil {
			return err
		}
		if meta, shot, err = checkTheme(files, tc); err != nil {
			return err
		}
		if raw, err = packages.PackFiles(files, key); err != nil {
			return err
		}
	} else if raw, err = packages.PackDir(pos[0], key); err != nil {
		return err
	}
	p, err := packages.Parse(raw, packages.DefaultLimits)
	if err != nil {
		return err
	}
	if p.Kind != kind {
		return fmt.Errorf("%s holds a %s, not a %s: use `hatamoto %s pack`", pos[0], p.Kind, kind, p.Kind)
	}
	if outFile == "" {
		outFile = p.Name + "-" + p.Version + packages.Extension
	}
	if err := os.WriteFile(outFile, raw, 0o644); err != nil {
		return err
	}
	sum := p.SHA256()
	fmt.Fprintf(out, "%s\n  %s %s v%s, %d bytes\n  sha256 %s\n", outFile, p.Kind, p.Name, p.Version, len(raw), hex.EncodeToString(sum[:]))
	if key != nil {
		fmt.Fprintf(out, "  signed by %s\n", key.ID)
	} else {
		fmt.Fprintln(out, "  UNSIGNED: servers install it only when the owner allows unsigned packages (pass --key to sign)")
	}
	if themeSrc && tc.screenshotOut != "" {
		return writeScreenshot(tc.screenshotOut, meta, shot, out)
	}
	return nil
}

// devAllowed decides whether this CLI run may write dev packages. The answer
// is the server's: ENVIRONMENT=production in this shell refuses, and so does a
// database whose server last booted as production (it records that in
// hatamoto_package_config). Beyond that a dev package needs an explicit --dev
// or ENVIRONMENT=development, because the CLI's own default environment is
// development and must not decide this silently.
func devAllowed(shellEnv, serverEnv string, devFlag bool) (bool, string) {
	switch {
	case shellEnv == "production":
		return false, "ENVIRONMENT=production refuses dev packages"
	case serverEnv == "production":
		return false, "this database belongs to a production server, which refuses dev packages"
	case !devFlag && shellEnv != "development":
		return false, "dev packages need an explicit --dev flag (or ENVIRONMENT=development)"
	}
	return true, ""
}

// packageManager is a Manager that writes the database; the running server
// applies the change. Its render engine only checks themes (it is not the
// server's). dev is true when dev packages may be written, otherwise why not.
func packageManager(ctx context.Context, logger *slog.Logger, devFlag bool) (*packages.Manager, *database.DB, bool, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, false, "", err
	}
	db, err := openDB(ctx)
	if err != nil {
		return nil, nil, false, "", err
	}
	trust, err := packages.ParseTrustSet(cfg.Packages.TrustedKeyList())
	if err != nil {
		db.Close()
		return nil, nil, false, "", fmt.Errorf("HATAMOTO_TRUSTED_KEYS: %w", err)
	}
	var serverEnv string
	if pc, err := packages.NewStore(db.Pool()).Config(ctx); err == nil {
		serverEnv = pc.ServerEnvironment
	}
	dev, why := devAllowed(os.Getenv("ENVIRONMENT"), serverEnv, devFlag)
	eng, err := render.New(render.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		db.Close()
		return nil, nil, false, "", err
	}
	registerInstalledPluginTemplates(ctx, packages.NewStore(db.Pool()), logger)
	m := packages.NewManager(packages.Options{Pool: db.Pool(), Trust: trust, AllowDev: dev, Engine: eng, Logger: logger})
	return m, db, dev, why, nil
}

// registerInstalledPluginTemplates gives the CLI's render engine what the
// running server has: the template funcs, partials and sections of every
// enabled plugin, so a theme that calls plugin_<name>_<func> is checked, and
// its section coverage counted, as in Admin > Themes. The CLI runs no plugin
// code: a func renders empty, which is enough to parse and smoke-render.
func registerInstalledPluginTemplates(ctx context.Context, st *packages.Store, logger *slog.Logger) {
	rows, err := st.List(ctx)
	if err != nil {
		logger.Warn("plugin templates not registered", "error", err)
		return
	}
	stub := func(...any) string { return "" }
	for _, row := range rows {
		if row.Kind != packages.KindPlugin || !row.Enabled || row.State != packages.StateInstalled {
			continue
		}
		var man sdk.Manifest
		if err := json.Unmarshal(row.Manifest, &man); err != nil {
			continue
		}
		if ok, _ := man.Compatible(); !ok {
			continue
		}
		pkg, _, err := st.Load(ctx, packages.KindPlugin, row.Name)
		if err != nil {
			logger.Warn("plugin templates not registered", "plugin", row.Name, "error", err)
			continue
		}
		assets, err := pluginhost.AssetsFromFS(pkg.FS())
		if err != nil {
			logger.Warn("plugin assets not read", "plugin", row.Name, "error", err)
		}
		render.RemovePlugin(man.Name)
		for _, fn := range man.Funcs {
			_ = render.RegisterPluginFunc(man.Name, fn, stub)
		}
		for name, src := range assets.Partials {
			_ = render.RegisterPluginPartial(man.Name, name, src)
		}
		for typ, src := range assets.Sections {
			_ = render.RegisterPluginSection(man.Name, typ, src)
		}
	}
}

// confirmHint tells the owner how to answer a ConfirmationRequired.
func confirmHint(err error) error {
	var need *packages.ConfirmationRequired
	if errors.As(err, &need) {
		return fmt.Errorf("%w\nre-run with --confirm if you mean it", err)
	}
	return err
}

func runPackageInstall(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	var grant, source string
	var devFlag, confirm bool
	pos, err := flagSet("package install", args, func(fs *flag.FlagSet) {
		fs.StringVar(&grant, "grant", "", "permissions to grant a plugin: comma separated, or all")
		fs.StringVar(&source, "source", "", "upload (a file, the default), url or dev (needs --dev)")
		fs.BoolVar(&devFlag, "dev", false, "allow a dev package (never in production)")
		fs.BoolVar(&confirm, "confirm", false, "replace anyway: an older version, another signer, or a theme that loses sections")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: hatamoto package install <file|url> [--grant P,P|all]")
	}
	var data []byte
	if strings.HasPrefix(pos[0], "https://") || strings.HasPrefix(pos[0], "http://") {
		source = "url"
		c := &http.Client{Timeout: 2 * time.Minute}
		if data, err = packages.FetchURL(ctx, c, pos[0], packages.DefaultLimits.Archive); err != nil {
			return err
		}
	} else {
		if source == "" {
			source = "upload"
		}
		if data, err = readBounded(pos[0], packages.DefaultLimits.Archive); err != nil {
			return err
		}
	}
	p, err := packages.Parse(data, packages.DefaultLimits)
	if err != nil {
		return err
	}
	m, db, _, why, err := packageManager(ctx, logger, devFlag)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx = packages.WithActor(ctx, packages.Actor{Email: "cli"})
	if packages.Source(source) == packages.SourceDev && why != "" {
		return fmt.Errorf("%w: %s", packages.ErrDevDisabled, why)
	}
	req := packages.InstallRequest{Source: packages.Source(source), Confirm: confirm}
	if p.Kind == packages.KindPlugin && grant != "" {
		man, _ := p.PluginManifest()
		req.Grant = []string{}
		if grant == "all" {
			req.Grant = man.Permissions
		} else {
			req.Grant = strings.Split(grant, ",")
		}
	}
	if _, err := m.Install(ctx, data, req); err != nil {
		return confirmHint(err)
	}
	fmt.Fprintf(out, "installed %s %s v%s\n", p.Kind, p.Name, p.Version)
	switch p.Kind {
	case packages.KindPlugin:
		man, _ := p.PluginManifest()
		if len(man.Permissions) > 0 && req.Grant == nil {
			fmt.Fprintf(out, "it asks for: %s\ngrant them in Admin > Plugins (or install again with --grant)\n", strings.Join(man.Permissions, ", "))
		}
	case packages.KindTheme:
		fmt.Fprintln(out, "activate it in Admin > Themes")
	}
	fmt.Fprintln(out, "a running server applies it within a few seconds; no restart is needed")
	return nil
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, max)
	}
	return b, nil
}

// runThemeDev pushes a theme directory as an unsigned dev package and again
// whenever a file in it changes. Development only: a production server neither
// accepts nor loads dev packages.
func runThemeDev(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	var noActivate, devFlag, confirm bool
	pos, err := flagSet("theme dev", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&noActivate, "no-activate", false, "install the theme but leave the active theme alone")
		fs.BoolVar(&devFlag, "dev", false, "confirm this is a development site (or set ENVIRONMENT=development)")
		fs.BoolVar(&confirm, "confirm", false, "replace anyway: an older or signed theme of the same name, or sections the content uses")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: hatamoto theme dev <dir> --dev [--no-activate] [--confirm]")
	}
	dir := pos[0]
	m, db, dev, why, err := packageManager(ctx, logger, devFlag)
	if err != nil {
		return err
	}
	defer db.Close()
	if !dev {
		return fmt.Errorf("theme dev is for development: %s. Build a signed package with `hatamoto theme pack` instead", why)
	}
	ctx = packages.WithActor(ctx, packages.Actor{Email: "cli"})

	push := func(first bool) {
		raw, err := packages.PackDir(dir, nil)
		var p *packages.Package
		if err == nil {
			p, err = packages.Parse(raw, packages.DefaultLimits)
		}
		if err == nil && p.Kind != packages.KindTheme {
			err = fmt.Errorf("%s holds a %s; theme dev takes a theme", dir, p.Kind)
		}
		if err == nil {
			_, err = m.Install(ctx, raw, packages.InstallRequest{Source: packages.SourceDev, Confirm: confirm})
			err = confirmHint(err)
		}
		if err == nil && first && !noActivate {
			err = m.Activate(ctx, p.Name, true)
		}
		if err != nil {
			fmt.Fprintf(out, "%s  NOT PUSHED: %v\n", time.Now().Format("15:04:05"), err)
			return
		}
		fmt.Fprintf(out, "%s  pushed %s v%s (dev, unsigned)\n", time.Now().Format("15:04:05"), p.Name, p.Version)
	}
	fmt.Fprintf(out, "watching %s; Ctrl-C to stop\n", dir)
	push(true)
	last := dirFingerprint(dir)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if cur := dirFingerprint(dir); cur != last {
				last = cur
				push(false)
			}
		}
	}
}

// dirFingerprint is a hash of every file's path, size and modification time.
func dirFingerprint(dir string) string {
	var names []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				names = append(names, fmt.Sprintf("%s\x00%d\x00%d", p, fi.Size(), fi.ModTime().UnixNano()))
			}
		}
		return nil
	})
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		io.WriteString(h, n+"\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
