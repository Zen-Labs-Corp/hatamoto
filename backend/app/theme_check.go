package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"hatamoto.dev/core/internal/packages"
)

// themeSource is where a theme's files come from: exactly one of a directory,
// an archive (with its pinned sha256) or an already packed .hmp.
type themeSource struct{ dir, archive, sha256, pkg string }

// themeCheckOpts are the flags `theme check` and `theme pack` share.
type themeCheckOpts struct {
	src                       themeSource
	registry                  bool
	expectName, screenshotOut string
}

var sha256Flag = regexp.MustCompile(`^[0-9a-f]{64}$`)

// bind defines the shared flags on fs. The source is one of the positional
// <dir>, --archive F --sha256 H, or --package F.
func (o *themeCheckOpts) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.src.archive, "archive", "", "read the theme from an author archive F (needs --sha256)")
	fs.StringVar(&o.src.sha256, "sha256", "", "pinned sha256 (64 lowercase hex) of the --archive")
	fs.StringVar(&o.src.pkg, "package", "", "read the theme from an already packed .hmp F")
	fs.BoolVar(&o.registry, "registry", false, "registry rules: a valid screenshot is required and the name may not be \"default\"")
	fs.StringVar(&o.expectName, "expect-name", "", "refuse unless theme.json names this theme")
	fs.StringVar(&o.screenshotOut, "screenshot-out", "", "with --registry, write the screenshot into directory D as <name>.<ext>")
}

// resolve picks the single source from pos (at most one directory) and checks
// the flag combination, before anything is read.
func (o *themeCheckOpts) resolve(pos []string, usage string) error {
	n := 0
	if len(pos) > 0 {
		o.src.dir = pos[0]
		n++
	}
	if o.src.archive != "" {
		n++
	}
	if o.src.pkg != "" {
		n++
	}
	switch {
	case len(pos) > 1 || n != 1:
		return errors.New(usage + "\n  give exactly one of <dir>, --archive F --sha256 H, --package F")
	case (o.src.archive == "") != (o.src.sha256 == ""):
		return errors.New("--archive and --sha256 go together")
	case o.src.sha256 != "" && !sha256Flag.MatchString(o.src.sha256):
		return errors.New("--sha256 must be 64 lowercase hex characters")
	case o.screenshotOut != "" && !o.registry:
		return errors.New("--screenshot-out needs --registry")
	}
	return nil
}

// buildThemeFiles reads the theme's files. A directory is walked exactly as
// `pack` walks it; an archive goes through packages.ReadArchive; a package is
// parsed and its files read back (it is re-read, not installed, so its
// signature is not checked).
func buildThemeFiles(src themeSource) (map[string][]byte, error) {
	switch {
	case src.archive != "":
		data, err := os.ReadFile(src.archive)
		if err != nil {
			return nil, err
		}
		return packages.ReadArchive(data, src.sha256, packages.DefaultLimits)
	case src.pkg != "":
		raw, err := os.ReadFile(src.pkg)
		if err != nil {
			return nil, err
		}
		p, err := packages.Parse(raw, packages.DefaultLimits)
		if err != nil {
			return nil, err
		}
		return packageFiles(p)
	}
	raw, err := packages.PackDir(src.dir, nil)
	if err != nil {
		return nil, err
	}
	p, err := packages.Parse(raw, packages.DefaultLimits)
	if err != nil {
		return nil, err
	}
	return packageFiles(p)
}

func packageFiles(p *packages.Package) (map[string][]byte, error) {
	files := map[string][]byte{}
	fsys := p.FS()
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || name == packages.SignatureFile {
			return err // a re-signed package gets a fresh signature
		}
		b, err := fs.ReadFile(fsys, name)
		files[name] = b
		return err
	})
	return files, err
}

// checkTheme parses files as a theme (an invalid screenshot is only refused with --registry)
// and applies the --registry and --expect-name rules. It returns the theme's
// meta and, with --registry, the screenshot bytes.
func checkTheme(files map[string][]byte, opts themeCheckOpts) (*packages.ThemeMeta, []byte, error) {
	zipped, err := packages.PackFiles(files, nil)
	if err != nil {
		return nil, nil, err
	}
	p, err := packages.Parse(zipped, packages.DefaultLimits)
	if err != nil {
		return nil, nil, err
	}
	if p.Kind != packages.KindTheme {
		return nil, nil, fmt.Errorf("this holds a %s, not a theme", p.Kind)
	}
	m, err := p.ThemeMeta()
	if err != nil {
		return nil, nil, err
	}
	if opts.expectName != "" && m.Name != opts.expectName {
		return nil, nil, fmt.Errorf("theme.json names %q, expected %q", m.Name, opts.expectName)
	}
	var shot []byte
	if opts.registry {
		if p.ScreenshotErr != nil {
			return nil, nil, fmt.Errorf("%s: %w", packages.ThemeManifest, p.ScreenshotErr)
		}
		if m.Name == "default" {
			return nil, nil, errors.New(`the name "default" is reserved`)
		}
		if m.Screenshot == "" {
			return nil, nil, errors.New("the registry needs a screenshot: set \"screenshot\" in theme.json to an image under assets/")
		}
		shot = files[m.Screenshot]
	}
	return &m, shot, nil
}

// writeScreenshot writes shot to DIR/<name><ext> and prints the one stdout
// line the registry builder reads.
func writeScreenshot(dir string, m *packages.ThemeMeta, shot []byte, out io.Writer) error {
	name := m.Name + packages.ScreenshotExt(m.Screenshot)
	if err := os.WriteFile(filepath.Join(dir, name), shot, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "screenshot: %s\n", name)
	return nil
}

// runThemeCheck validates a theme without a key and writes nothing, except
// the screenshot when --screenshot-out is given.
func runThemeCheck(args []string, out io.Writer) error {
	var o themeCheckOpts
	pos, err := flagSet("theme check", args, o.bind)
	if err != nil {
		return err
	}
	if err := o.resolve(pos, "usage: hatamoto theme check <dir> [--registry] [--expect-name N] [--screenshot-out D]"); err != nil {
		return err
	}
	files, err := buildThemeFiles(o.src)
	if err != nil {
		return err
	}
	m, shot, err := checkTheme(files, o)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "ok: theme %s v%s\n", m.Name, m.Version)
	if o.screenshotOut != "" {
		return writeScreenshot(o.screenshotOut, m, shot, out)
	}
	return nil
}
