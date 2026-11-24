package app

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
)

const kitUsage = `  kit check <dir|file> [--package F] [--expect-name N]
                                           validate a kit without a key, a database or the network (docs/KITS.md)
  kit pack <dir> --key F [--out F] [--expect-name N]
                                           build a .hmp kit package, signed with key F
  kit apply <name|file|url> [--accept-comment-warnings] [--grant P,P] [--activate-theme] [--homepage] [--dry-run] [--dev]
                                           set a site up from a kit: plugins, theme, content types, pages, menus (docs/KITS.md)
  kit list                                 the kits applied to this site and the kits the registry offers`

// runKit runs the kit commands. check and pack need no database and no
// network; apply and list are in kit_apply.go.
func runKit(ctx context.Context, args []string, logger *slog.Logger, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return runKitCheck(args[1:], out)
		case "pack":
			return runKitPack(args[1:], out)
		case "apply":
			return runKitApply(ctx, args[1:], logger, out)
		case "list":
			return runKitList(ctx, args[1:], logger, out)
		}
	}
	return fmt.Errorf("usage: hatamoto kit check|pack|apply|list\n%s", kitUsage)
}

// loadKitPackage reads a kit from a directory (packed unsigned) or a .hmp
// file and parses it with the rules a new package meets.
func loadKitPackage(path string, key *packages.Key) (*packages.Package, []byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	var raw []byte
	if st.IsDir() {
		raw, err = packages.PackDir(path, key)
	} else {
		raw, err = readBounded(path, packages.DefaultLimits.Archive)
	}
	if err != nil {
		return nil, nil, err
	}
	p, err := packages.ParseNew(raw, packages.DefaultLimits)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.Kind != packages.KindKit {
		return nil, nil, fmt.Errorf("%s holds a %s, not a kit: use `hatamoto %s pack`", path, p.Kind, p.Kind)
	}
	return p, raw, nil
}

func expectKitName(p *packages.Package, want string) error {
	if want != "" && p.Name != want {
		return fmt.Errorf("the kit is named %q, expected %q", p.Name, want)
	}
	return nil
}

func runKitCheck(args []string, out io.Writer) error {
	var pkg, expect string
	pos, err := flagSet("kit check", args, func(fs *flag.FlagSet) {
		fs.StringVar(&pkg, "package", "", "a packed .hmp kit to check (instead of a directory)")
		fs.StringVar(&expect, "expect-name", "", "refuse a kit with another name")
	})
	if err != nil {
		return err
	}
	const usage = "usage: hatamoto kit check <dir|file> [--expect-name N]"
	switch {
	case pkg != "" && len(pos) == 0:
		pos = []string{pkg}
	case len(pos) != 1 || pkg != "":
		return errors.New(usage)
	}
	p, _, err := loadKitPackage(pos[0], nil)
	if err != nil {
		return err
	}
	if err := expectKitName(p, expect); err != nil {
		return err
	}
	k, err := p.Kit()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "kit %s %s: ok\n", p.Name, p.Version)
	missing := kit.Missing(k.Requires)
	for _, r := range k.Requires {
		switch {
		case !slices.Contains(kit.KnownFeatures, r):
			fmt.Fprintf(out, "note: requires %q, which this Hatamoto does not know\n", r)
		case slices.Contains(missing, r):
			fmt.Fprintf(out, "note: requires %q: needs a newer Hatamoto\n", r)
		}
	}
	return nil
}

func runKitPack(args []string, out io.Writer) error {
	var keyFile, outFile, expect string
	pos, err := flagSet("kit pack", args, func(fs *flag.FlagSet) {
		fs.StringVar(&keyFile, "key", os.Getenv("HATAMOTO_SIGNING_KEY"), "signing key file (from `keys generate`); env HATAMOTO_SIGNING_KEY")
		fs.StringVar(&outFile, "out", "", "output file (default <name>-<version>.hmp)")
		fs.StringVar(&expect, "expect-name", "", "refuse a kit with another name")
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 || keyFile == "" {
		return errors.New("usage: hatamoto kit pack <dir> --key F [--out F] [--expect-name N]")
	}
	key, err := loadKey(keyFile)
	if err != nil {
		return err
	}
	p, raw, err := loadKitPackage(pos[0], key)
	if err != nil {
		return err
	}
	if err := expectKitName(p, expect); err != nil {
		return err
	}
	if outFile == "" {
		outFile = p.Name + "-" + p.Version + packages.Extension
	}
	if err := os.WriteFile(outFile, raw, 0o644); err != nil {
		return err
	}
	sum := p.SHA256()
	fmt.Fprintf(out, "%s\n  kit %s v%s, %d bytes\n  sha256 %s\n  signed by %s\n", outFile, p.Name, p.Version, len(raw), hex.EncodeToString(sum[:]), key.ID)
	return nil
}
