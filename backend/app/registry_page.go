package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/packages"
)

func runRegistryPage(args []string, out io.Writer) error { return registryPage(args, out, time.Now) }

// registryPage renders the plugins of a signed registry.json as a static HTML
// page. The index must verify against the first-party key, HATAMOTO_TRUSTED_KEYS
// and any --trusted-key (the keys a server trusts); nothing is written
// otherwise.
func registryPage(args []string, out io.Writer, now func() time.Time) error {
	var outFile string
	var opts packages.PageOptions
	var extraKeys []string
	pos, err := flagSet("registry page", args, func(fs *flag.FlagSet) {
		fs.StringVar(&outFile, "out", "plugins/index.html", "output file")
		fs.StringVar(&opts.Title, "title", "", "page title (default Plugins)")
		fs.StringVar(&opts.Intro, "intro", "", "text under the title")
		fs.StringVar(&opts.WriteURL, "write-url", "", "https link for a \"Write a plugin\" button (hidden when empty)")
		fs.StringVar(&opts.SubmitURL, "submit-url", "", "https link for a \"Submit a plugin\" button (hidden when empty)")
		fs.StringVar(&opts.InstallHint, "install-hint", "", "line shown under the list (hidden when empty)")
		fs.Func("trusted-key", "base64 public key to trust besides the server defaults (repeatable)", func(v string) error {
			extraKeys = append(extraKeys, v)
			return nil
		})
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: hatamoto registry page <registry.json> [--out F] [--title T] [--intro T] [--write-url U] [--submit-url U] [--install-hint T] [--trusted-key K]")
	}
	doc, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	list := config.LoadPackageConfig().TrustedKeyList()
	for _, k := range extraKeys {
		list += " " + k
	}
	trust, err := packages.ParseTrustSet(list)
	if err != nil {
		return err
	}
	idx, err := packages.ParseIndex(doc, trust, now())
	if err != nil {
		return fmt.Errorf("%s: %w", pos[0], err)
	}
	html, err := packages.RenderPage(idx, opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outFile), ".page-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(html); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), outFile); err != nil {
		return err
	}
	n := 0
	for _, e := range idx.Packages {
		if e.Kind == packages.KindPlugin {
			n++
		}
	}
	fmt.Fprintf(out, "wrote %d plugins to %s\n", n, outFile)
	return nil
}
